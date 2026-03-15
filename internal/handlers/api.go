package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/auth"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/email"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
	"github.com/ghostersk/gowebmail/internal/syncer"
	"github.com/gorilla/mux"
)

// APIHandler handles all /api/* JSON endpoints.
type APIHandler struct {
	db     *db.DB
	cfg    *config.Config
	syncer *syncer.Scheduler
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
	SyncDays     int                    `json:"sync_days"`
	SyncMode     string                 `json:"sync_mode"`
	SortOrder    int                    `json:"sort_order"`
	LastError    string                 `json:"last_error,omitempty"`
	Color        string                 `json:"color"`
	LastSync     string                 `json:"last_sync"`
	TokenExpired bool                   `json:"token_expired,omitempty"`
}

func toSafeAccount(a *models.EmailAccount) safeAccount {
	lastSync := ""
	if !a.LastSync.IsZero() {
		lastSync = a.LastSync.Format("2006-01-02T15:04:05Z")
	}
	tokenExpired := false
	if (a.Provider == models.ProviderGmail || a.Provider == models.ProviderOutlook) && auth.IsTokenExpired(a.TokenExpiry) {
		tokenExpired = true
	}
	return safeAccount{
		ID: a.ID, Provider: a.Provider, EmailAddress: a.EmailAddress,
		DisplayName: a.DisplayName, IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort,
		SMTPHost: a.SMTPHost, SMTPPort: a.SMTPPort,
		SyncDays: a.SyncDays, SyncMode: a.SyncMode, SortOrder: a.SortOrder,
		LastError: a.LastError, Color: a.Color, LastSync: lastSync,
		TokenExpired: tokenExpired,
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
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		IMAPHost    string `json:"imap_host"`
		IMAPPort    int    `json:"imap_port"`
		SMTPHost    string `json:"smtp_host"`
		SMTPPort    int    `json:"smtp_port"`
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
		AccessToken: req.Password,
		IMAPHost:    req.IMAPHost, IMAPPort: req.IMAPPort,
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
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		IMAPHost    string `json:"imap_host"`
		IMAPPort    int    `json:"imap_port"`
		SMTPHost    string `json:"smtp_host"`
		SMTPPort    int    `json:"smtp_port"`
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

func (h *APIHandler) CountFolderMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")
	count, err := h.db.CountFolderMessages(folderID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "count failed")
		return
	}
	h.writeJSON(w, map[string]int{"count": count})
}

func (h *APIHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")

	// Look up folder before deleting so we have its path and account
	folder, err := h.db.GetFolderByID(folderID)
	if err != nil || folder == nil {
		h.writeError(w, http.StatusNotFound, "folder not found")
		return
	}

	// Delete on IMAP server first
	account, err := h.db.GetAccount(folder.AccountID)
	if err == nil && account != nil {
		if imapClient, cerr := email.Connect(context.Background(), account); cerr == nil {
			_ = imapClient.DeleteMailbox(folder.FullPath)
			imapClient.Close()
		}
	}

	// Delete from local DB
	if err := h.db.DeleteFolder(folderID, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) MoveFolderContents(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	fromID := pathInt64(r, "id")
	toID := pathInt64(r, "toId")
	moved, err := h.db.MoveFolderContents(fromID, toID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "move failed")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true, "moved": moved})
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

func (h *APIHandler) SetAccountSortOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Order []int64 `json:"order"` // account IDs in desired display order
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Order) == 0 {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := h.db.UpdateAccountSortOrder(userID, req.Order); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save order")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) GetUIPrefs(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	prefs, err := h.db.GetUIPrefs(userID)
	if err != nil {
		prefs = "{}"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(prefs))
}

func (h *APIHandler) SetUIPrefs(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil || len(body) == 0 {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Validate it's valid JSON before storing
	var check map[string]interface{}
	if err := json.Unmarshal(body, &check); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.db.SetUIPrefs(userID, string(body)); err != nil {
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

	// Lazy attachment backfill: if has_attachment=true but no rows in attachments table
	// (message was synced before attachment parsing was added), fetch from IMAP now and save.
	if msg.HasAttachment && len(msg.Attachments) == 0 {
		if uid, folderPath, account, iErr := h.db.GetMessageIMAPInfo(messageID, userID); iErr == nil && uid != 0 && account != nil {
			if c, cErr := email.Connect(context.Background(), account); cErr == nil {
				if raw, rErr := c.FetchRawByUID(folderPath, uid); rErr == nil {
					_, _, atts := email.ParseMIMEFull(raw)
					if len(atts) > 0 {
						h.db.SaveAttachmentMeta(messageID, atts)
						if fresh, fErr := h.db.GetAttachmentsByMessage(messageID, userID); fErr == nil {
							msg.Attachments = fresh
						}
					}
				}
				c.Close()
			}
		}
	}

	h.writeJSON(w, msg)
}

func (h *APIHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	var req struct {
		Read bool `json:"read"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Update local DB first
	h.db.MarkMessageRead(messageID, userID, req.Read)

	// Enqueue IMAP op — drained by background worker with retry
	uid, folderPath, account, err := h.db.GetMessageIMAPInfo(messageID, userID)
	if err == nil && uid != 0 && account != nil {
		val := "0"
		if req.Read {
			val = "1"
		}
		h.db.EnqueueIMAPOp(&db.PendingIMAPOp{
			AccountID: account.ID, OpType: "flag_read",
			RemoteUID: uid, FolderPath: folderPath, Extra: val,
		})
		h.syncer.TriggerAccountSync(account.ID)
	}
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
	uid, folderPath, account, ierr := h.db.GetMessageIMAPInfo(messageID, userID)
	if ierr == nil && uid != 0 && account != nil {
		val := "0"
		if starred {
			val = "1"
		}
		h.db.EnqueueIMAPOp(&db.PendingIMAPOp{
			AccountID: account.ID, OpType: "flag_star",
			RemoteUID: uid, FolderPath: folderPath, Extra: val,
		})
		h.syncer.TriggerAccountSync(account.ID)
	}
	h.writeJSON(w, map[string]bool{"ok": true, "starred": starred})
}

func (h *APIHandler) MoveMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	var req struct {
		FolderID int64 `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FolderID == 0 {
		h.writeError(w, http.StatusBadRequest, "folder_id required")
		return
	}

	// Get IMAP info before changing folder_id in DB
	uid, srcPath, account, imapErr := h.db.GetMessageIMAPInfo(messageID, userID)
	destFolder, _ := h.db.GetFolderByID(req.FolderID)

	// Update local DB
	if err := h.db.MoveMessage(messageID, userID, req.FolderID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "move failed")
		return
	}

	// Enqueue IMAP move
	if imapErr == nil && uid != 0 && account != nil && destFolder != nil {
		h.db.EnqueueIMAPOp(&db.PendingIMAPOp{
			AccountID: account.ID, OpType: "move",
			RemoteUID: uid, FolderPath: srcPath, Extra: destFolder.FullPath,
		})
		h.syncer.TriggerAccountSync(account.ID)
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")

	// Get IMAP info before deleting from DB
	uid, folderPath, account, imapErr := h.db.GetMessageIMAPInfo(messageID, userID)

	// Delete from local DB
	if err := h.db.DeleteMessage(messageID, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}

	// Enqueue IMAP delete
	if imapErr == nil && uid != 0 && account != nil {
		h.db.EnqueueIMAPOp(&db.PendingIMAPOp{
			AccountID: account.ID, OpType: "delete",
			RemoteUID: uid, FolderPath: folderPath,
		})
		h.syncer.TriggerAccountSync(account.ID)
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
func (h *APIHandler) ForwardAsAttachment(w http.ResponseWriter, r *http.Request) {
	h.handleSend(w, r, "forward-attachment")
}

func (h *APIHandler) handleSend(w http.ResponseWriter, r *http.Request, mode string) {
	userID := middleware.GetUserID(r)

	var req models.ComposeRequest

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// Parse multipart form (attachments present)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}
		metaStr := r.FormValue("meta")
		if err := json.NewDecoder(strings.NewReader(metaStr)).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid meta JSON")
			return
		}
		if r.MultipartForm != nil {
			for _, fheaders := range r.MultipartForm.File {
				for _, fh := range fheaders {
					f, err := fh.Open()
					if err != nil {
						continue
					}
					data, _ := io.ReadAll(f)
					f.Close()
					fileCT := fh.Header.Get("Content-Type")
					if fileCT == "" {
						fileCT = "application/octet-stream"
					}
					req.Attachments = append(req.Attachments, models.Attachment{
						Filename:    fh.Filename,
						ContentType: fileCT,
						Size:        int64(len(data)),
						Data:        data,
					})
				}
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
	}

	account, err := h.db.GetAccount(req.AccountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusBadRequest, "account not found")
		return
	}

	// Forward-as-attachment: fetch original message as EML and attach it
	if mode == "forward-attachment" && req.ForwardFromID > 0 {
		origMsg, _ := h.db.GetMessage(req.ForwardFromID, userID)
		if origMsg != nil {
			uid, folderPath, origAccount, iErr := h.db.GetMessageIMAPInfo(req.ForwardFromID, userID)
			if iErr == nil && uid != 0 && origAccount != nil {
				if c, cErr := email.Connect(context.Background(), origAccount); cErr == nil {
					if raw, rErr := c.FetchRawByUID(folderPath, uid); rErr == nil {
						safe := sanitizeFilename(origMsg.Subject)
						if safe == "" {
							safe = "message"
						}
						req.Attachments = append(req.Attachments, models.Attachment{
							Filename:    safe + ".eml",
							ContentType: "message/rfc822",
							Data:        raw,
						})
					}
					c.Close()
				}
			}
		}
	}

	account = h.ensureAccountTokenFresh(account)
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

	// Try to fetch real raw headers from IMAP server
	rawHeaders := ""
	uid, folderPath, account, iErr := h.db.GetMessageIMAPInfo(messageID, userID)
	if iErr == nil && uid != 0 && account != nil {
		if c, cErr := email.Connect(context.Background(), account); cErr == nil {
			defer c.Close()
			if raw, rErr := c.FetchRawByUID(folderPath, uid); rErr == nil {
				// Extract only the header section (before first blank line)
				rawStr := string(raw)
				if idx := strings.Index(rawStr, "\r\n\r\n"); idx != -1 {
					rawHeaders = rawStr[:idx+2]
				} else if idx := strings.Index(rawStr, "\n\n"); idx != -1 {
					rawHeaders = rawStr[:idx+1]
				} else {
					rawHeaders = rawStr
				}
			} else {
				log.Printf("FetchRawByUID for headers msg=%d: %v", messageID, rErr)
			}
		} else {
			log.Printf("Connect for headers msg=%d: %v", messageID, cErr)
		}
	}

	// Fallback: reconstruct from stored fields
	if rawHeaders == "" {
		var b strings.Builder
		order := []string{"Date", "From", "To", "Cc", "Bcc", "Reply-To", "Subject", "Message-ID"}
		for _, k := range order {
			if v := headers[k]; v != "" {
				fmt.Fprintf(&b, "%s: %s\r\n", k, v)
			}
		}
		rawHeaders = b.String()
	}

	h.writeJSON(w, map[string]interface{}{"headers": headers, "raw": rawHeaders})
}

func (h *APIHandler) StarredMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	result, err := h.db.ListStarredMessages(userID, page, pageSize)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list starred")
		return
	}
	h.writeJSON(w, result)
}

func (h *APIHandler) DownloadEML(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	msg, err := h.db.GetMessage(messageID, userID)
	if err != nil || msg == nil {
		h.writeError(w, http.StatusNotFound, "message not found")
		return
	}

	// Try to fetch raw from IMAP first
	uid, folderPath, account, iErr := h.db.GetMessageIMAPInfo(messageID, userID)
	if iErr == nil && uid != 0 && account != nil {
		if c, cErr := email.Connect(context.Background(), account); cErr == nil {
			defer c.Close()
			if raw, rErr := c.FetchRawByUID(folderPath, uid); rErr == nil {
				safe := sanitizeFilename(msg.Subject) + ".eml"
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safe))
				w.Header().Set("Content-Type", "message/rfc822")
				w.Write(raw)
				return
			}
		}
	}

	// Fallback: reconstruct from stored fields
	var buf strings.Builder
	buf.WriteString("Date: " + msg.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\r\n")
	buf.WriteString(fmt.Sprintf("From: %s <%s>\r\n", msg.FromName, msg.FromEmail))
	if msg.ToList != "" {
		buf.WriteString("To: " + msg.ToList + "\r\n")
	}
	if msg.CCList != "" {
		buf.WriteString("Cc: " + msg.CCList + "\r\n")
	}
	buf.WriteString("Subject: " + msg.Subject + "\r\n")
	if msg.MessageID != "" {
		buf.WriteString("Message-ID: " + msg.MessageID + "\r\n")
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	if msg.BodyHTML != "" {
		boundary := "GoMailBoundary"
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(msg.BodyText + "\r\n")
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		buf.WriteString(msg.BodyHTML + "\r\n")
		buf.WriteString("--" + boundary + "--\r\n")
	} else {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(msg.BodyText)
	}
	safe := sanitizeFilename(msg.Subject) + ".eml"
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safe))
	w.Header().Set("Content-Type", "message/rfc822")
	w.Write([]byte(buf.String()))
}

func sanitizeFilename(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			out.WriteRune('_')
		} else {
			out.WriteRune(r)
		}
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "message"
	}
	if len(result) > 80 {
		result = result[:80]
	}
	return result
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

// ---- Empty folder (Trash/Spam) ----

func (h *APIHandler) EmptyFolder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")

	// Verify folder is trash or spam before allowing bulk delete
	folder, err := h.db.GetFolderByID(folderID)
	if err != nil || folder == nil {
		h.writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	if folder.FolderType != "trash" && folder.FolderType != "spam" {
		h.writeError(w, http.StatusBadRequest, "can only empty trash or spam folders")
		return
	}

	n, err := h.db.EmptyFolder(folderID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to empty folder")
		return
	}
	h.db.UpdateFolderCounts(folderID)
	h.writeJSON(w, map[string]interface{}{"ok": true, "deleted": n})
}

// ---- Enable sync for all folders of an account ----

func (h *APIHandler) EnableAllFolderSync(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	vars := mux.Vars(r)
	accountIDStr := vars["account_id"]
	var accountID int64
	fmt.Sscanf(accountIDStr, "%d", &accountID)
	if accountID == 0 {
		h.writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	n, err := h.db.EnableAllFolderSync(accountID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to enable sync")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true, "enabled": n})
}

// ---- Long-poll for unread counts + new message detection ----
// GET /api/poll?since=<lastKnownMessageID>
// Returns immediately with current counts; client polls every ~20s.

func (h *APIHandler) PollUnread(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var sinceID int64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &sinceID)

	inboxUnread, totalUnread, newestID, err := h.db.PollUnread(userID, sinceID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "poll failed")
		return
	}

	h.writeJSON(w, map[string]interface{}{
		"inbox_unread": inboxUnread,
		"total_unread": totalUnread,
		"newest_id":    newestID,
		"has_new":      newestID > sinceID && sinceID > 0,
	})
}

// ---- Get new messages since ID (for notification content) ----

func (h *APIHandler) NewMessagesSince(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var sinceID int64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &sinceID)
	if sinceID == 0 {
		h.writeJSON(w, map[string]interface{}{"messages": []interface{}{}})
		return
	}
	msgs, err := h.db.GetNewMessagesSince(userID, sinceID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	h.writeJSON(w, map[string]interface{}{"messages": msgs})
}

// ---- Attachment download ----

// DownloadAttachment fetches and streams a message attachment from IMAP.
func (h *APIHandler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")

	// Get attachment metadata from DB
	attachmentID := pathInt64(r, "att_id")
	att, err := h.db.GetAttachment(attachmentID, userID)
	if err != nil || att == nil {
		h.writeError(w, http.StatusNotFound, "attachment not found")
		return
	}

	_ = messageID // already verified via GetAttachment ownership check

	// Get IMAP info for the message
	uid, folderPath, account, iErr := h.db.GetMessageIMAPInfo(att.MessageID, userID)
	if iErr != nil || uid == 0 || account == nil {
		h.writeError(w, http.StatusNotFound, "message IMAP info not found")
		return
	}

	c, cErr := email.Connect(context.Background(), account)
	if cErr != nil {
		h.writeError(w, http.StatusBadGateway, "IMAP connect failed: "+cErr.Error())
		return
	}
	defer c.Close()

	// att.ContentID stores the MIME part path (set during parse)
	mimePartPath := att.ContentID
	if mimePartPath == "" {
		h.writeError(w, http.StatusNotFound, "attachment part path not stored")
		return
	}

	data, filename, ct, fetchErr := c.FetchAttachmentRaw(folderPath, uid, mimePartPath)
	if fetchErr != nil {
		h.writeError(w, http.StatusBadGateway, "fetch failed: "+fetchErr.Error())
		return
	}
	if filename == "" {
		filename = att.Filename
	}
	if ct == "" {
		ct = att.ContentType
	}
	if ct == "" {
		ct = "application/octet-stream"
	}

	safe := sanitizeFilename(filename)
	// For browser-viewable types, use inline disposition so they open in a new tab.
	// For everything else, force download.
	disposition := "attachment"
	ctLower := strings.ToLower(ct)
	if strings.HasPrefix(ctLower, "image/") ||
		strings.HasPrefix(ctLower, "text/") ||
		strings.HasPrefix(ctLower, "video/") ||
		strings.HasPrefix(ctLower, "audio/") ||
		ctLower == "application/pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, safe))
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ListAttachments returns stored attachment metadata for a message.
func (h *APIHandler) ListAttachments(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	messageID := pathInt64(r, "id")
	atts, err := h.db.GetAttachmentsByMessage(messageID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list attachments")
		return
	}
	if atts == nil {
		atts = []models.Attachment{}
	}
	// Strip raw data from response, keep metadata only
	type attMeta struct {
		ID          int64  `json:"id"`
		MessageID   int64  `json:"message_id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
	}
	result := make([]attMeta, len(atts))
	for i, a := range atts {
		result[i] = attMeta{a.ID, a.MessageID, a.Filename, a.ContentType, a.Size}
	}
	h.writeJSON(w, result)
}

// ---- Mark folder all read ----

func (h *APIHandler) MarkFolderAllRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	folderID := pathInt64(r, "id")

	ops, err := h.db.MarkFolderAllRead(folderID, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Enqueue all flag_read ops and trigger sync
	accountIDs := map[int64]bool{}
	for _, op := range ops {
		h.db.EnqueueIMAPOp(&db.PendingIMAPOp{
			AccountID: op.AccountID, OpType: "flag_read",
			RemoteUID: op.RemoteUID, FolderPath: op.FolderPath, Extra: "1",
		})
		accountIDs[op.AccountID] = true
	}
	for accID := range accountIDs {
		h.syncer.TriggerAccountSync(accID)
	}

	h.writeJSON(w, map[string]interface{}{"ok": true, "marked": len(ops)})
}

// ---- Save draft (IMAP APPEND to Drafts) ----

func (h *APIHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)

	var req models.ComposeRequest
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid form")
			return
		}
		json.NewDecoder(strings.NewReader(r.FormValue("meta"))).Decode(&req)
	} else {
		json.NewDecoder(r.Body).Decode(&req)
	}

	account, err := h.db.GetAccount(req.AccountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusBadRequest, "account not found")
		return
	}

	// Build the MIME message bytes
	var buf strings.Builder
	buf.WriteString("From: " + account.EmailAddress + "\r\n")
	if len(req.To) > 0 {
		buf.WriteString("To: " + strings.Join(req.To, ", ") + "\r\n")
	}
	buf.WriteString("Subject: " + req.Subject + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	buf.WriteString(req.BodyHTML)

	raw := []byte(buf.String())

	// Append to IMAP Drafts in background
	go func() {
		c, err := email.Connect(context.Background(), account)
		if err != nil {
			log.Printf("[draft] IMAP connect %s: %v", account.EmailAddress, err)
			return
		}
		defer c.Close()
		draftsFolder, err := c.AppendToDrafts(raw)
		if err != nil {
			log.Printf("[draft] AppendToDrafts %s: %v", account.EmailAddress, err)
			return
		}
		if draftsFolder != "" {
			// Trigger a sync of the drafts folder to pick up the saved draft
			h.syncer.TriggerAccountSync(account.ID)
		}
	}()

	h.writeJSON(w, map[string]bool{"ok": true})
}

// ensureAccountTokenFresh refreshes the OAuth access token for a Gmail/Outlook
// account if it is near expiry. Returns a pointer to the (possibly updated)
// account, or the original if no refresh was needed / possible.
func (h *APIHandler) ensureAccountTokenFresh(account *models.EmailAccount) *models.EmailAccount {
	if account.Provider != models.ProviderGmail && account.Provider != models.ProviderOutlook {
		return account
	}
	if !auth.IsTokenExpired(account.TokenExpiry) {
		return account
	}
	if account.RefreshToken == "" {
		log.Printf("[oauth:%s] token expired, no refresh token stored", account.EmailAddress)
		return account
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	accessTok, refreshTok, expiry, err := auth.RefreshAccountToken(
		ctx,
		string(account.Provider),
		account.RefreshToken,
		h.cfg.BaseURL,
		h.cfg.GoogleClientID, h.cfg.GoogleClientSecret,
		h.cfg.MicrosoftClientID, h.cfg.MicrosoftClientSecret, h.cfg.MicrosoftTenantID,
	)
	if err != nil {
		log.Printf("[oauth:%s] token refresh failed: %v", account.EmailAddress, err)
		return account
	}
	if err := h.db.UpdateAccountTokens(account.ID, accessTok, refreshTok, expiry); err != nil {
		log.Printf("[oauth:%s] failed to persist refreshed token: %v", account.EmailAddress, err)
		return account
	}
	refreshed, err := h.db.GetAccount(account.ID)
	if err != nil || refreshed == nil {
		return account
	}
	log.Printf("[oauth:%s] access token refreshed for send (expires %s)", account.EmailAddress, expiry.Format("2006-01-02 15:04 UTC"))
	return refreshed
}
