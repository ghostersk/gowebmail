package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/yourusername/gomail/config"
	"github.com/yourusername/gomail/internal/db"
	"github.com/yourusername/gomail/internal/email"
	"github.com/yourusername/gomail/internal/middleware"
	"github.com/yourusername/gomail/internal/models"
	"github.com/yourusername/gomail/internal/syncer"
)

// APIHandler handles all /api/* JSON endpoints.
type APIHandler struct {
	db      *db.DB
	cfg     *config.Config
	syncer  *syncer.Scheduler
}

func (h *APIHandler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (h *APIHandler) writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ---- Provider availability ----

// GetProviders returns which OAuth providers are configured and enabled.
func (h *APIHandler) GetProviders(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]bool{
		"gmail":   h.cfg.GoogleClientID != "" && h.cfg.GoogleClientSecret != "",
		"outlook": h.cfg.MicrosoftClientID != "" && h.cfg.MicrosoftClientSecret != "",
	})
}

// ---- Accounts ----

type safeAccount struct {
	ID           int64                  `json:"id"`
	Provider     models.AccountProvider `json:"provider"`
	EmailAddress string                 `json:"email_address"`
	DisplayName  string                 `json:"display_name"`
	IMAPHost     string                 `json:"imap_host,omitempty"`
	IMAPPort     int                    `json:"imap_port,omitempty"`
	SMTPHost     string                 `json:"smtp_host,omitempty"`
	SMTPPort     int                    `json:"smtp_port,omitempty"`
	LastError    string                 `json:"last_error,omitempty"`
	Color        string                 `json:"color"`
	LastSync     string                 `json:"last_sync"`
}

func toSafeAccount(a *models.EmailAccount) safeAccount {
	lastSync := ""
	if !a.LastSync.IsZero() {
		lastSync = a.LastSync.Format("2006-01-02T15:04:05Z")
	}
	return safeAccount{
		ID: a.ID, Provider: a.Provider, EmailAddress: a.EmailAddress,
		DisplayName: a.DisplayName, IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort,
		SMTPHost: a.SMTPHost, SMTPPort: a.SMTPPort,
		LastError: a.LastError, Color: a.Color, LastSync: lastSync,
	}
}

func (h *APIHandler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accounts, err := h.db.ListAccountsByUser(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}
	result := make([]safeAccount, 0, len(accounts))
	for _, a := range accounts {
		result = append(result, toSafeAccount(a))
	}
	h.writeJSON(w, result)
}

func (h *APIHandler) AddAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email        string `json:"email"`
		DisplayName  string `json:"display_name"`
		Password     string `json:"password"`
		IMAPHost     string `json:"imap_host"`
		IMAPPort     int    `json:"imap_port"`
		SMTPHost     string `json:"smtp_host"`
		SMTPPort     int    `json:"smtp_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "email and password required")
		return
	}
	if req.IMAPHost == "" {
		h.writeError(w, http.StatusBadRequest, "IMAP host required")
		return
	}
	if req.IMAPPort == 0 {
		req.IMAPPort = 993
	}
	if req.SMTPPort == 0 {
		req.SMTPPort = 587
	}

	userID := middleware.GetUserID(r)
	accounts, _ := h.db.ListAccountsByUser(userID)
	colors := []string{"#4A90D9", "#EA4335", "#34A853", "#FBBC04", "#FF6D00", "#9C27B0"}
	color := colors[len(accounts)%len(colors)]

	account := &models.EmailAccount{
		UserID: userID, Provider: models.ProviderIMAPSMTP,
		EmailAddress: req.Email, DisplayName: req.DisplayName,
		AccessToken:  req.Password,
		IMAPHost: req.IMAPHost, IMAPPort: req.IMAPPort,
		SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort,
		Color: color, IsActive: true,
	}

	if err := h.db.CreateAccount(account); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	uid := userID
	h.db.WriteAudit(&uid, models.AuditAccountAdd, "imap:"+req.Email, middleware.ClientIP(r), r.UserAgent())

	// Trigger an immediate sync in background
	go h.syncer.SyncAccountNow(account.ID)

	h.writeJSON(w, map[string]interface{}{"id": account.ID, "ok": true})
}

func (h *APIHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "id")
	account, err := h.db.GetAccount(accountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "account not found")
		return
	}
	h.writeJSON(w, toSafeAccount(account))
}

func (h *APIHandler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "id")

	account, err := h.db.GetAccount(accountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "account not found")
		return
	}

	var req struct {
		DisplayName  string `json:"display_name"`
		Password     string `json:"password"`
		IMAPHost     string `json:"imap_host"`
		IMAPPort     int    `json:"imap_port"`
		SMTPHost     string `json:"smtp_host"`
		SMTPPort     int    `json:"smtp_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.DisplayName != "" {
		account.DisplayName = req.DisplayName
	}
	if req.Password != "" {
		account.AccessToken = req.Password
	}
	if req.IMAPHost != "" {
		account.IMAPHost = req.IMAPHost
	}
	if req.IMAPPort > 0 {
		account.IMAPPort = req.IMAPPort
	}
	if req.SMTPHost != "" {
		account.SMTPHost = req.SMTPHost
	}
	if req.SMTPPort > 0 {
		account.SMTPPort = req.SMTPPort
	}

	if err := h.db.UpdateAccount(account); err != nil {
		h.writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		IMAPHost string `json:"imap_host"`
		IMAPPort int    `json:"imap_port"`
		SMTPHost string `json:"smtp_host"`
		SMTPPort int    `json:"smtp_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.IMAPPort == 0 {
		req.IMAPPort = 993
	}

	testAccount := &models.EmailAccount{
		Provider:     models.ProviderIMAPSMTP,
		EmailAddress: req.Email,
		AccessToken:  req.Password,
		IMAPHost:     req.IMAPHost,
		IMAPPort:     req.IMAPPort,
		SMTPHost:     req.SMTPHost,
		SMTPPort:     req.SMTPPort,
	}

	if err := email.TestConnection(testAccount); err != nil {
		h.writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// DetectMailSettings tries common IMAP/SMTP combinations for a domain and returns
// the first working combination, or sensible defaults if nothing connects.
func (h *APIHandler) DetectMailSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		h.writeError(w, http.StatusBadRequest, "email required")
		return
	}
	at := strings.Index(req.Email, "@")
	if at < 0 {
		h.writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	domain := req.Email[at+1:]

	type candidate struct {
		host string
		port int
	}
	imapCandidates := []candidate{
		{"imap." + domain, 993},
		{"mail." + domain, 993},
		{"imap." + domain, 143},
		{"mail." + domain, 143},
	}
	smtpCandidates := []candidate{
		{"smtp." + domain, 587},
		{"mail." + domain, 587},
		{"smtp." + domain, 465},
		{"mail." + domain, 465},
		{"smtp." + domain, 25},
	}

	type result struct {
		IMAPHost string `json:"imap_host"`
		IMAPPort int    `json:"imap_port"`
		SMTPHost string `json:"smtp_host"`
		SMTPPort int    `json:"smtp_port"`
		Detected bool   `json:"detected"`
	}

	res := result{
		IMAPHost: "imap." + domain,
		IMAPPort: 993,
		SMTPHost: "smtp." + domain,
		SMTPPort: 587,
	}

	// Try IMAP candidates (TCP dial only, no auth needed to detect)
	for _, c := range imapCandidates {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", c.host, c.port), 4*time.Second)
		if err == nil {
			conn.Close()
			res.IMAPHost = c.host
			res.IMAPPort = c.port
			res.Detected = true
			break
		}
	}

	// Try SMTP candidates
	for _, c := range smtpCandidates {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", c.host, c.port), 4*time.Second)
		if err == nil {
			conn.Close()
			res.SMTPHost = c.host
			res.SMTPPort = c.port
			res.Detected = true
			break
		}
	}

	h.writeJSON(w, res)
}

func (h *APIHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "id")
	if err := h.db.DeleteAccount(accountID, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	uid := userID
	h.db.WriteAudit(&uid, models.AuditAccountDel, strconv.FormatInt(accountID, 10), middleware.ClientIP(r), r.UserAgent())
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) SyncAccount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "id")

	account, err := h.db.GetAccount(accountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "account not found")
		return
	}

	synced, err := h.syncer.SyncAccountNow(accountID)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true, "synced": synced})
}

func (h *APIHandler) SyncFolder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")

	folder, err := h.db.GetFolderByID(folderID)
	if err != nil || folder == nil {
		h.writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	account, err := h.db.GetAccount(folder.AccountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "folder not found")
		return
	}

	synced, err := h.syncer.SyncFolderNow(folder.AccountID, folderID)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true, "synced": synced})
}

func (h *APIHandler) SetFolderVisibility(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")
	var req struct {
		IsHidden    bool `json:"is_hidden"`
		SyncEnabled bool `json:"sync_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := h.db.SetFolderVisibility(folderID, userID, req.IsHidden, req.SyncEnabled); err != nil {
		h.writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) SetAccountSyncSettings(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "id")
	var req struct {
		SyncDays int    `json:"sync_days"`
		SyncMode string `json:"sync_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := h.db.SetAccountSyncSettings(accountID, userID, req.SyncDays, req.SyncMode); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) SetComposePopup(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Popup bool `json:"compose_popup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := h.db.SetComposePopup(userID, req.Popup); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ---- Messages ----

func (h *APIHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", 50)
	accountID := queryInt64(r, "account_id", 0)
	folderID := queryInt64(r, "folder_id", 0)

	var folderIDs []int64
	if folderID > 0 {
		folderIDs = []int64{folderID}
	}

	result, err := h.db.ListMessages(userID, folderIDs, accountID, page, pageSize)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	h.writeJSON(w, result)
}

func (h *APIHandler) UnifiedInbox(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", 50)

	// Get all inbox folder IDs for this user
	folders, err := h.db.GetFoldersByUser(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get folders")
		return
	}
	var inboxIDs []int64
	for _, f := range folders {
		if f.FolderType == "inbox" {
			inboxIDs = append(inboxIDs, f.ID)
		}
	}

	result, err := h.db.ListMessages(userID, inboxIDs, 0, page, pageSize)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	h.writeJSON(w, result)
}

func (h *APIHandler) GetMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	msg, err := h.db.GetMessage(messageID, userID)
	if err != nil || msg == nil {
		h.writeError(w, http.StatusNotFound, "message not found")
		return
	}
	h.db.MarkMessageRead(messageID, userID, true)
	h.writeJSON(w, msg)
}

func (h *APIHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	var req struct{ Read bool `json:"read"` }
	json.NewDecoder(r.Body).Decode(&req)
	h.db.MarkMessageRead(messageID, userID, req.Read)
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) ToggleStar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	starred, err := h.db.ToggleMessageStar(messageID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to toggle star")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true, "starred": starred})
}

func (h *APIHandler) MoveMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	var req struct{ FolderID int64 `json:"folder_id"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FolderID == 0 {
		h.writeError(w, http.StatusBadRequest, "folder_id required")
		return
	}
	if err := h.db.MoveMessage(messageID, userID, req.FolderID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "move failed")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	if err := h.db.DeleteMessage(messageID, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ---- Send / Reply / Forward ----

func (h *APIHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	h.handleSend(w, r, "new")
}
func (h *APIHandler) ReplyMessage(w http.ResponseWriter, r *http.Request) {
	h.handleSend(w, r, "reply")
}
func (h *APIHandler) ForwardMessage(w http.ResponseWriter, r *http.Request) {
	h.handleSend(w, r, "forward")
}

func (h *APIHandler) handleSend(w http.ResponseWriter, r *http.Request, mode string) {
	userID := middleware.GetUserID(r)
	var req models.ComposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	account, err := h.db.GetAccount(req.AccountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusBadRequest, "account not found")
		return
	}

	if err := email.SendMessageFull(context.Background(), account, &req); err != nil {
		log.Printf("SMTP send failed account=%d user=%d: %v", req.AccountID, userID, err)
		h.db.WriteAudit(&userID, models.AuditAppError,
			fmt.Sprintf("send failed account:%d – %v", req.AccountID, err),
			middleware.ClientIP(r), r.UserAgent())
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ---- Folders ----

func (h *APIHandler) ListFolders(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folders, err := h.db.GetFoldersByUser(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get folders")
		return
	}
	if folders == nil {
		folders = []*models.Folder{}
	}
	h.writeJSON(w, folders)
}

func (h *APIHandler) ListAccountFolders(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	accountID := pathInt64(r, "account_id")
	account, err := h.db.GetAccount(accountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "account not found")
		return
	}
	folders, err := h.db.ListFoldersByAccount(accountID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get folders")
		return
	}
	if folders == nil {
		folders = []*models.Folder{}
	}
	h.writeJSON(w, folders)
}

// ---- Search ----

func (h *APIHandler) Search(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		h.writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", 50)

	result, err := h.db.SearchMessages(userID, q, page, pageSize)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	h.writeJSON(w, result)
}

// ---- Sync interval (per-user) ----

func (h *APIHandler) GetSyncInterval(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	interval, err := h.db.GetUserSyncInterval(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get sync interval")
		return
	}
	h.writeJSON(w, map[string]int{"sync_interval": interval})
}

func (h *APIHandler) SetSyncInterval(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		SyncInterval int `json:"sync_interval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.SyncInterval != 0 && (req.SyncInterval < 1 || req.SyncInterval > 60) {
		h.writeError(w, http.StatusBadRequest, "sync_interval must be 0 (manual) or 1-60 minutes")
		return
	}
	if err := h.db.SetUserSyncInterval(userID, req.SyncInterval); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update sync interval")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ---- Helpers ----

func pathInt64(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(mux.Vars(r)[key], 10, 64)
	return v
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func queryInt64(r *http.Request, key string, def int64) int64 {
	if v := r.URL.Query().Get(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return def
}

// ---- Message headers (for troubleshooting) ----

func (h *APIHandler) GetMessageHeaders(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	msg, err := h.db.GetMessage(messageID, userID)
	if err != nil || msg == nil {
		h.writeError(w, http.StatusNotFound, "message not found")
		return
	}
	// Return a simplified set of headers we store
	headers := map[string]string{
		"Message-ID": msg.MessageID,
		"From":       fmt.Sprintf("%s <%s>", msg.FromName, msg.FromEmail),
		"To":         msg.ToList,
		"Cc":         msg.CCList,
		"Bcc":        msg.BCCList,
		"Reply-To":   msg.ReplyTo,
		"Subject":    msg.Subject,
		"Date":       msg.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"),
	}
	h.writeJSON(w, map[string]interface{}{"headers": headers})
}

// ---- Remote content whitelist ----

func (h *APIHandler) GetRemoteContentWhitelist(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	list, err := h.db.GetRemoteContentWhitelist(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get whitelist")
		return
	}
	if list == nil {
		list = []string{}
	}
	h.writeJSON(w, map[string]interface{}{"whitelist": list})
}

func (h *APIHandler) AddRemoteContentWhitelist(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Sender string `json:"sender"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Sender == "" {
		h.writeError(w, http.StatusBadRequest, "sender required")
		return
	}
	if err := h.db.AddRemoteContentWhitelist(userID, req.Sender); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to add to whitelist")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}
