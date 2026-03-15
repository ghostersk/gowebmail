// Package auth handles OAuth2 flows for Gmail and Outlook.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ghostersk/gowebmail/internal/logger"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/microsoft"
)

// ---- Gmail OAuth2 ----

// GmailScopes are the OAuth2 scopes required for full Gmail access.
var GmailScopes = []string{
	"https://mail.google.com/", // Full IMAP+SMTP access
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// NewGmailConfig creates an OAuth2 config for Gmail.
func NewGmailConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       GmailScopes,
		Endpoint:     google.Endpoint,
	}
}

// GoogleUserInfo holds the data returned by Google's userinfo endpoint.
type GoogleUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// GetGoogleUserInfo fetches the authenticated user's email and name.
func GetGoogleUserInfo(ctx context.Context, token *oauth2.Token, cfg *oauth2.Config) (*GoogleUserInfo, error) {
	client := cfg.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned %d", resp.StatusCode)
	}
	var info GoogleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ---- Microsoft / Outlook OAuth2 ----

// OutlookAuthScopes are used for the Microsoft 365 / Outlook work & school OAuth flow.
// Uses https://outlook.office.com/ prefix so the resulting token has the correct
// audience for IMAP XOAUTH2 authentication.
var OutlookAuthScopes = []string{
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/SMTP.Send",
	"offline_access",
	"openid",
	"email",
}

// NewOutlookConfig creates the OAuth2 config for the authorization flow.
func NewOutlookConfig(clientID, clientSecret, tenantID, redirectURL string) *oauth2.Config {
	if tenantID == "" {
		tenantID = "consumers"
	}
	// "consumers" forces the Azure AD v2.0 endpoint for personal accounts
	// and returns a proper JWT Bearer token (aud=https://outlook.office.com).
	// "common" routes personal accounts through login.live.com which returns
	// a v1.0 opaque token (starts with EwA) that IMAP XOAUTH2 rejects.
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       OutlookAuthScopes,
		Endpoint:     microsoft.AzureADEndpoint(tenantID),
	}
}

// ExchangeForIMAPToken takes the refresh_token obtained from the Graph-scoped
// authorization and exchanges it for an access token scoped to the Outlook
// resource (aud=https://outlook.office.com), which the IMAP server requires.
// The two-step approach is necessary because:
//   - Azure personal app registrations only expose bare Graph scope names in their UI
//   - The IMAP server rejects tokens whose aud is graph.microsoft.com
//   - Using the refresh_token against the Outlook resource produces a correct token
func ExchangeForIMAPToken(ctx context.Context, clientID, clientSecret, tenantID, refreshToken string) (*oauth2.Token, error) {
	if tenantID == "" {
		tenantID = "consumers"
	}
	tokenURL := "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token"

	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("refresh_token", refreshToken)
	params.Set("scope", "https://outlook.office.com/IMAP.AccessAsUser.All https://outlook.office.com/SMTP.Send offline_access")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build IMAP token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("IMAP token request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode IMAP token response: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("microsoft IMAP token error: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("microsoft returned empty IMAP access token")
	}

	// Log first 30 chars and whether it looks like a JWT (3 dot-separated parts)
	preview := result.AccessToken
	if len(preview) > 30 {
		preview = preview[:30] + "..."
	}
	parts := strings.Count(result.AccessToken, ".") + 1
	logger.Debug("[oauth:outlook:exchange] got token with %d parts: %s (scope=%s)",
		parts, preview, params.Get("scope"))

	expiry := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return &oauth2.Token{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		Expiry:       expiry,
	}, nil
}

// MicrosoftUserInfo holds user info extracted from the Microsoft ID token.
type MicrosoftUserInfo struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"` // Graph field
	Name              string `json:"name"`        // ID token claim
	Mail              string `json:"mail"`
	EmailClaim        string `json:"email"` // ID token claim
	UserPrincipalName string `json:"userPrincipalName"`
	PreferredUsername string `json:"preferred_username"` // ID token claim
}

// Email returns the best available email address.
func (m *MicrosoftUserInfo) Email() string {
	if m.Mail != "" {
		return m.Mail
	}
	if m.EmailClaim != "" {
		return m.EmailClaim
	}
	if m.PreferredUsername != "" {
		return m.PreferredUsername
	}
	return m.UserPrincipalName
}

// BestName returns the best available display name.
func (m *MicrosoftUserInfo) BestName() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Name
}

// GetMicrosoftUserInfo extracts user info from the OAuth2 token's ID token JWT.
// This avoids calling graph.microsoft.com/v1.0/me which requires a Graph-scoped
// token — but our token is scoped to outlook.office.com for IMAP/SMTP access.
// The ID token is issued alongside the access token and contains email/name claims.
func GetMicrosoftUserInfo(ctx context.Context, token *oauth2.Token, cfg *oauth2.Config) (*MicrosoftUserInfo, error) {
	idToken, _ := token.Extra("id_token").(string)
	if idToken == "" {
		return nil, fmt.Errorf("no id_token in Microsoft token response")
	}

	// JWT structure: header.payload.signature — decode the payload only
	parts := strings.SplitN(idToken, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed id_token: expected 3 parts, got %d", len(parts))
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("id_token base64 decode: %w", err)
	}

	var info MicrosoftUserInfo
	if err := json.Unmarshal(decoded, &info); err != nil {
		return nil, fmt.Errorf("id_token JSON decode: %w", err)
	}

	if info.Email() == "" {
		return nil, fmt.Errorf("id_token contains no usable email address (raw claims: %s)", string(decoded))
	}
	return &info, nil
}

// ---- Outlook Personal (Graph API) ----

// OutlookPersonalScopes are used for personal outlook.com accounts.
// These use Microsoft Graph which correctly issues JWT tokens for personal accounts.
// Mail is accessed via Graph REST API instead of IMAP.
var OutlookPersonalScopes = []string{
	"https://graph.microsoft.com/Mail.ReadWrite",
	"https://graph.microsoft.com/Mail.Send",
	"https://graph.microsoft.com/User.Read",
	"offline_access",
	"openid",
	"email",
}

// NewOutlookPersonalConfig creates OAuth2 config for personal outlook.com accounts.
// Uses consumers tenant to force Azure AD v2.0 endpoint and get JWT tokens.
func NewOutlookPersonalConfig(clientID, clientSecret, tenantID, redirectURL string) *oauth2.Config {
	if tenantID == "" {
		tenantID = "consumers"
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       OutlookPersonalScopes,
		Endpoint:     microsoft.AzureADEndpoint(tenantID),
	}
}

// ---- Token refresh helpers ----

// IsTokenExpired reports whether the token expires within a 60-second buffer.
func IsTokenExpired(expiry time.Time) bool {
	if expiry.IsZero() {
		return false
	}
	return time.Now().Add(60 * time.Second).After(expiry)
}

// RefreshToken attempts to exchange a refresh token for a new access token.
func RefreshToken(ctx context.Context, cfg *oauth2.Config, refreshToken string) (*oauth2.Token, error) {
	ts := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return ts.Token()
}

// RefreshAccountToken refreshes the OAuth token for a Gmail or Outlook account.
// Pass the credentials for both providers; the correct ones are selected based
// on provider ("gmail" or "outlook").
func RefreshAccountToken(ctx context.Context,
	provider, refreshToken, baseURL,
	googleClientID, googleClientSecret,
	msClientID, msClientSecret, msTenantID string,
) (accessToken, newRefresh string, expiry time.Time, err error) {

	switch provider {
	case "gmail":
		cfg := NewGmailConfig(googleClientID, googleClientSecret, baseURL+"/auth/gmail/callback")
		tok, err := RefreshToken(ctx, cfg, refreshToken)
		if err != nil {
			return "", "", time.Time{}, err
		}
		return tok.AccessToken, tok.RefreshToken, tok.Expiry, nil
	case "outlook":
		cfg := NewOutlookConfig(msClientID, msClientSecret, msTenantID, baseURL+"/auth/outlook/callback")
		tok, err := RefreshToken(ctx, cfg, refreshToken)
		if err != nil {
			return "", "", time.Time{}, err
		}
		rt := tok.RefreshToken
		if rt == "" {
			rt = refreshToken
		}
		return tok.AccessToken, rt, tok.Expiry, nil
	case "outlook_personal":
		// Personal outlook.com accounts use Graph API scopes — standard refresh works
		cfg := NewOutlookPersonalConfig(msClientID, msClientSecret, msTenantID,
			baseURL+"/auth/outlook-personal/callback")
		tok, err := RefreshToken(ctx, cfg, refreshToken)
		if err != nil {
			return "", "", time.Time{}, err
		}
		rt := tok.RefreshToken
		if rt == "" {
			rt = refreshToken
		}
		return tok.AccessToken, rt, tok.Expiry, nil
	default:
		return "", "", time.Time{}, fmt.Errorf("not an OAuth provider: %s", provider)
	}
}
