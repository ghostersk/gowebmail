// Package auth handles OAuth2 flows for Gmail and Outlook.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/microsoft"
)

// ---- Gmail OAuth2 ----

// GmailScopes are the OAuth2 scopes required for full Gmail access.
var GmailScopes = []string{
	"https://mail.google.com/",       // Full IMAP+SMTP access
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

// OutlookScopes are required for Outlook/Microsoft 365 mail access.
var OutlookScopes = []string{
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/SMTP.Send",
	"offline_access",
	"openid",
	"profile",
	"email",
}

// NewOutlookConfig creates an OAuth2 config for Microsoft/Outlook.
func NewOutlookConfig(clientID, clientSecret, tenantID, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       OutlookScopes,
		Endpoint:     microsoft.AzureADEndpoint(tenantID),
	}
}

// MicrosoftUserInfo holds data from Microsoft Graph /me endpoint.
type MicrosoftUserInfo struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

// Email returns the best available email address.
func (m *MicrosoftUserInfo) Email() string {
	if m.Mail != "" {
		return m.Mail
	}
	return m.UserPrincipalName
}

// GetMicrosoftUserInfo fetches user info from Microsoft Graph.
func GetMicrosoftUserInfo(ctx context.Context, token *oauth2.Token, cfg *oauth2.Config) (*MicrosoftUserInfo, error) {
	client := cfg.Client(ctx, token)
	resp, err := client.Get("https://graph.microsoft.com/v1.0/me")
	if err != nil {
		return nil, fmt.Errorf("graph /me request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph /me returned %d", resp.StatusCode)
	}
	var info MicrosoftUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
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

	var cfg *oauth2.Config
	switch provider {
	case "gmail":
		cfg = NewGmailConfig(googleClientID, googleClientSecret, baseURL+"/auth/gmail/callback")
	case "outlook":
		cfg = NewOutlookConfig(msClientID, msClientSecret, msTenantID, baseURL+"/auth/outlook/callback")
	default:
		return "", "", time.Time{}, fmt.Errorf("not an OAuth provider: %s", provider)
	}

	tok, err := RefreshToken(ctx, cfg, refreshToken)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return tok.AccessToken, tok.RefreshToken, tok.Expiry, nil
}
