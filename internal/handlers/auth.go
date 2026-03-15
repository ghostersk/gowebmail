package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/ghostersk/gowebmail/config"
	goauth "github.com/ghostersk/gowebmail/internal/auth"
	"github.com/ghostersk/gowebmail/internal/crypto"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/mfa"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"

	"golang.org/x/oauth2"
)

// AuthHandler handles login, register, logout, MFA, and OAuth2 connect flows.
type AuthHandler struct {
	db       *db.DB
	cfg      *config.Config
	renderer *Renderer
}

// ---- Login ----

func (h *AuthHandler) ShowLogin(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "login", nil)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	ip := middleware.ClientIP(r)
	ua := r.UserAgent()

	if username == "" || password == "" {
		http.Redirect(w, r, "/auth/login?error=missing_fields", http.StatusFound)
		return
	}

	// Accept login by username or email
	user, err := h.db.GetUserByUsername(username)
	if err != nil || user == nil {
		user, err = h.db.GetUserByEmail(username)
	}
	if err != nil || user == nil || !user.IsActive {
		h.db.WriteAudit(nil, models.AuditLoginFail, "unknown user: "+username, ip, ua)
		http.Redirect(w, r, "/auth/login?error=invalid_credentials", http.StatusFound)
		return
	}

	// Per-user IP access check — evaluated before password to avoid timing leaks
	switch h.db.CheckUserIPAccess(user.ID, ip) {
	case "deny":
		h.db.WriteAudit(&user.ID, models.AuditLoginFail, "IP not in allow-list: "+ip, ip, ua)
		http.Redirect(w, r, "/auth/login?error=location_not_authorized", http.StatusFound)
		return
	case "skip_brute":
		// Signal the BruteForceProtect middleware to skip failure counting for this user/IP
		w.Header().Set("X-Skip-Brute", "1")
	}

	if err := crypto.CheckPassword(password, user.PasswordHash); err != nil {
		uid := user.ID
		h.db.WriteAudit(&uid, models.AuditLoginFail, "bad password for: "+username, ip, ua)
		http.Redirect(w, r, "/auth/login?error=invalid_credentials", http.StatusFound)
		return
	}

	token, _ := h.db.CreateSession(user.ID, 7*24*time.Hour)
	h.setSessionCookie(w, token)
	h.db.TouchLastLogin(user.ID)

	uid := user.ID
	h.db.WriteAudit(&uid, models.AuditLogin, "login from "+ip, ip, ua)

	if user.MFAEnabled {
		http.Redirect(w, r, "/auth/mfa", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("gomail_session")
	if err == nil {
		userID := middleware.GetUserID(r)
		if userID > 0 {
			h.db.WriteAudit(&userID, models.AuditLogout, "", middleware.ClientIP(r), r.UserAgent())
		}
		h.db.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "gomail_session", Value: "", MaxAge: -1, Path: "/",
		Secure: h.cfg.SecureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// ---- MFA ----

func (h *AuthHandler) ShowMFA(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "mfa", nil)
}

func (h *AuthHandler) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	code := r.FormValue("code")
	ip := middleware.ClientIP(r)
	ua := r.UserAgent()

	user, err := h.db.GetUserByID(userID)
	if err != nil || user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	if !mfa.Validate(user.MFASecret, code) {
		h.db.WriteAudit(&userID, models.AuditMFAFail, "bad TOTP code", ip, ua)
		http.Redirect(w, r, "/auth/mfa?error=invalid_code", http.StatusFound)
		return
	}

	cookie, _ := r.Cookie("gomail_session")
	h.db.SetSessionMFAVerified(cookie.Value)
	h.db.WriteAudit(&userID, models.AuditMFASuccess, "", ip, ua)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ---- MFA Setup (user settings) ----

func (h *AuthHandler) MFASetupBegin(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	user, err := h.db.GetUserByID(userID)
	if err != nil || user == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	secret, err := mfa.GenerateSecret()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate secret")
		return
	}

	if err := h.db.SetMFAPending(userID, secret); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to store pending secret")
		return
	}

	qr := mfa.QRCodeURL("GoWebMail", user.Email, secret)
	otpURL := mfa.OTPAuthURL("GoWebMail", user.Email, secret)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"qr_url":  qr,
		"otp_url": otpURL,
		"secret":  secret,
	})
}

func (h *AuthHandler) MFASetupConfirm(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Code string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	user, _ := h.db.GetUserByID(userID)
	if user == nil || user.MFAPending == "" {
		writeJSONError(w, http.StatusBadRequest, "no pending MFA setup")
		return
	}

	if !mfa.Validate(user.MFAPending, req.Code) {
		writeJSONError(w, http.StatusBadRequest, "invalid code — try again")
		return
	}

	if err := h.db.EnableMFA(userID, user.MFAPending); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to enable MFA")
		return
	}

	h.db.WriteAudit(&userID, models.AuditMFAEnable, "", middleware.ClientIP(r), r.UserAgent())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (h *AuthHandler) MFADisable(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Code string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	user, _ := h.db.GetUserByID(userID)
	if user == nil || !user.MFAEnabled {
		writeJSONError(w, http.StatusBadRequest, "MFA not enabled")
		return
	}

	if !mfa.Validate(user.MFASecret, req.Code) {
		writeJSONError(w, http.StatusBadRequest, "invalid code")
		return
	}

	h.db.DisableMFA(userID)
	h.db.WriteAudit(&userID, models.AuditMFADisable, "", middleware.ClientIP(r), r.UserAgent())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// ---- Change password ----

func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	user, _ := h.db.GetUserByID(userID)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if crypto.CheckPassword(req.CurrentPassword, user.PasswordHash) != nil {
		writeJSONError(w, http.StatusBadRequest, "current password incorrect")
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if err := h.db.UpdateUserPassword(userID, req.NewPassword); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// ---- Me ----

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	user, _ := h.db.GetUserByID(userID)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            user.ID,
		"email":         user.Email,
		"username":      user.Username,
		"role":          user.Role,
		"mfa_enabled":   user.MFAEnabled,
		"compose_popup": user.ComposePopup,
		"sync_interval": user.SyncInterval,
	})
}

// ---- Gmail OAuth2 ----

func (h *AuthHandler) GmailConnect(w http.ResponseWriter, r *http.Request) {
	if h.cfg.GoogleClientID == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "Google OAuth2 not configured.")
		return
	}
	userID := middleware.GetUserID(r)
	state := encodeOAuthState(userID, "gmail")
	cfg := goauth.NewGmailConfig(h.cfg.GoogleClientID, h.cfg.GoogleClientSecret, h.cfg.GoogleRedirectURL)
	url := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *AuthHandler) GmailCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	userID, provider := decodeOAuthState(state)
	if userID == 0 || provider != "gmail" {
		http.Redirect(w, r, "/?error=oauth_state_mismatch", http.StatusFound)
		return
	}
	oauthCfg := goauth.NewGmailConfig(h.cfg.GoogleClientID, h.cfg.GoogleClientSecret, h.cfg.GoogleRedirectURL)
	token, err := oauthCfg.Exchange(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/?error=oauth_exchange_failed", http.StatusFound)
		return
	}
	userInfo, err := goauth.GetGoogleUserInfo(r.Context(), token, oauthCfg)
	if err != nil {
		http.Redirect(w, r, "/?error=userinfo_failed", http.StatusFound)
		return
	}
	colors := []string{"#EA4335", "#4285F4", "#34A853", "#FBBC04", "#FF6D00", "#9C27B0"}
	accounts, _ := h.db.ListAccountsByUser(userID)
	color := colors[len(accounts)%len(colors)]
	account := &models.EmailAccount{
		UserID: userID, Provider: models.ProviderGmail,
		EmailAddress: userInfo.Email, DisplayName: userInfo.Name,
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		TokenExpiry: token.Expiry, Color: color, IsActive: true,
	}
	created, err := h.db.UpsertOAuthAccount(account)
	if err != nil {
		http.Redirect(w, r, "/?error=account_save_failed", http.StatusFound)
		return
	}
	uid := userID
	action := "gmail:" + userInfo.Email
	if !created {
		action = "gmail-reconnect:" + userInfo.Email
	}
	h.db.WriteAudit(&uid, models.AuditAccountAdd, action, middleware.ClientIP(r), r.UserAgent())
	http.Redirect(w, r, "/?connected=gmail", http.StatusFound)
}

// ---- Outlook OAuth2 ----

func (h *AuthHandler) OutlookConnect(w http.ResponseWriter, r *http.Request) {
	if h.cfg.MicrosoftClientID == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "Microsoft OAuth2 not configured.")
		return
	}
	userID := middleware.GetUserID(r)
	state := encodeOAuthState(userID, "outlook")
	cfg := goauth.NewOutlookConfig(h.cfg.MicrosoftClientID, h.cfg.MicrosoftClientSecret,
		h.cfg.MicrosoftTenantID, h.cfg.MicrosoftRedirectURL)
	// ApprovalForce + prompt=consent ensures Microsoft always returns a refresh_token,
	// even when the user has previously authorized the app.
	url := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("prompt", "consent"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *AuthHandler) OutlookCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	userID, provider := decodeOAuthState(state)
	if userID == 0 || provider != "outlook" {
		http.Redirect(w, r, "/?error=oauth_state_mismatch", http.StatusFound)
		return
	}
	oauthCfg := goauth.NewOutlookConfig(h.cfg.MicrosoftClientID, h.cfg.MicrosoftClientSecret,
		h.cfg.MicrosoftTenantID, h.cfg.MicrosoftRedirectURL)
	token, err := oauthCfg.Exchange(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/?error=oauth_exchange_failed", http.StatusFound)
		return
	}
	userInfo, err := goauth.GetMicrosoftUserInfo(r.Context(), token, oauthCfg)
	if err != nil {
		http.Redirect(w, r, "/?error=userinfo_failed", http.StatusFound)
		return
	}
	accounts, _ := h.db.ListAccountsByUser(userID)
	colors := []string{"#0078D4", "#EA4335", "#34A853", "#FBBC04", "#FF6D00", "#9C27B0"}
	color := colors[len(accounts)%len(colors)]
	account := &models.EmailAccount{
		UserID: userID, Provider: models.ProviderOutlook,
		EmailAddress: userInfo.Email(), DisplayName: userInfo.DisplayName,
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		TokenExpiry: token.Expiry, Color: color, IsActive: true,
	}
	created, err := h.db.UpsertOAuthAccount(account)
	if err != nil {
		http.Redirect(w, r, "/?error=account_save_failed", http.StatusFound)
		return
	}
	uid := userID
	action := "outlook:" + userInfo.Email()
	if !created {
		action = "outlook-reconnect:" + userInfo.Email()
	}
	h.db.WriteAudit(&uid, models.AuditAccountAdd, action, middleware.ClientIP(r), r.UserAgent())
	http.Redirect(w, r, "/?connected=outlook", http.StatusFound)
}

// ---- Helpers ----

type oauthStatePayload struct {
	UserID   int64  `json:"u"`
	Provider string `json:"p"`
	Nonce    string `json:"n"`
}

func encodeOAuthState(userID int64, provider string) string {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	payload := oauthStatePayload{UserID: userID, Provider: provider,
		Nonce: base64.URLEncoding.EncodeToString(nonce)}
	b, _ := json.Marshal(payload)
	return base64.URLEncoding.EncodeToString(b)
}

func decodeOAuthState(state string) (int64, string) {
	b, err := base64.URLEncoding.DecodeString(state)
	if err != nil {
		return 0, ""
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return 0, ""
	}
	return payload.UserID, payload.Provider
}

func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: "gomail_session", Value: token, Path: "/",
		MaxAge: 7 * 24 * 3600, Secure: h.cfg.SecureCookie,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ---- Profile Updates ----

func (h *AuthHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	user, err := h.db.GetUserByID(userID)
	if err != nil || user == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Field    string `json:"field"`    // "email" | "username"
		Value    string `json:"value"`
		Password string `json:"password"` // current password required for confirmation
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Value == "" {
		writeJSONError(w, http.StatusBadRequest, "value required")
		return
	}
	if req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "current password required to confirm profile changes")
		return
	}
	if err := crypto.CheckPassword(req.Password, user.PasswordHash); err != nil {
		writeJSONError(w, http.StatusForbidden, "incorrect password")
		return
	}

	switch req.Field {
	case "email":
		// Check uniqueness
		existing, _ := h.db.GetUserByEmail(req.Value)
		if existing != nil && existing.ID != userID {
			writeJSONError(w, http.StatusConflict, "email already in use")
			return
		}
		if err := h.db.UpdateUserEmail(userID, req.Value); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to update email")
			return
		}
	case "username":
		existing, _ := h.db.GetUserByUsername(req.Value)
		if existing != nil && existing.ID != userID {
			writeJSONError(w, http.StatusConflict, "username already in use")
			return
		}
		if err := h.db.UpdateUserUsername(userID, req.Value); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to update username")
			return
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "field must be 'email' or 'username'")
		return
	}

	ip := middleware.ClientIP(r)
	h.db.WriteAudit(&userID, models.AuditUserUpdate, "profile update: "+req.Field, ip, r.UserAgent())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// ---- Per-User IP Rules ----

func (h *AuthHandler) GetUserIPRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	rule, err := h.db.GetUserIPRule(userID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "db error")
		return
	}
	if rule == nil {
		rule = &db.UserIPRule{UserID: userID, Mode: "disabled", IPList: ""}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rule)
}

func (h *AuthHandler) SetUserIPRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Mode   string `json:"mode"`    // "disabled" | "brute_skip" | "allow_only"
		IPList string `json:"ip_list"` // comma-separated
	}
	json.NewDecoder(r.Body).Decode(&req)

	validModes := map[string]bool{"disabled": true, "brute_skip": true, "allow_only": true}
	if !validModes[req.Mode] {
		writeJSONError(w, http.StatusBadRequest, "mode must be disabled, brute_skip, or allow_only")
		return
	}

	// Validate IPs
	for _, rawIP := range db.SplitIPList(req.IPList) {
		if net.ParseIP(rawIP) == nil {
			writeJSONError(w, http.StatusBadRequest, "invalid IP address: "+rawIP)
			return
		}
	}

	if req.Mode == "disabled" {
		h.db.DeleteUserIPRule(userID)
	} else {
		if err := h.db.SetUserIPRule(userID, req.Mode, req.IPList); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save rule")
			return
		}
	}

	ip := middleware.ClientIP(r)
	h.db.WriteAudit(&userID, models.AuditUserUpdate, "IP rule updated: "+req.Mode, ip, r.UserAgent())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
