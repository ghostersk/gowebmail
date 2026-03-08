package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
	"github.com/gorilla/mux"
)

// AdminHandler handles /admin/* routes (all require admin role).
type AdminHandler struct {
	db       *db.DB
	cfg      *config.Config
	renderer *Renderer
}

func (h *AdminHandler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (h *AdminHandler) writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ShowAdmin serves the admin SPA shell for all /admin/* routes.
func (h *AdminHandler) ShowAdmin(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "admin", nil)
}

// ---- User Management ----

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListUsers()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	// Sanitize: strip password hash
	type safeUser struct {
		ID          int64           `json:"id"`
		Email       string          `json:"email"`
		Username    string          `json:"username"`
		Role        models.UserRole `json:"role"`
		IsActive    bool            `json:"is_active"`
		MFAEnabled  bool            `json:"mfa_enabled"`
		LastLoginAt interface{}     `json:"last_login_at"`
		CreatedAt   interface{}     `json:"created_at"`
	}
	result := make([]safeUser, 0, len(users))
	for _, u := range users {
		result = append(result, safeUser{
			ID: u.ID, Email: u.Email, Username: u.Username,
			Role: u.Role, IsActive: u.IsActive, MFAEnabled: u.MFAEnabled,
			LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
		})
	}
	h.writeJSON(w, result)
}

func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "username, email and password required")
		return
	}
	if len(req.Password) < 8 {
		h.writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	role := models.RoleUser
	if req.Role == "admin" {
		role = models.RoleAdmin
	}

	user, err := h.db.CreateUser(req.Username, req.Email, req.Password, role)
	if err != nil {
		h.writeError(w, http.StatusConflict, err.Error())
		return
	}

	adminID := middleware.GetUserID(r)
	h.db.WriteAudit(&adminID, models.AuditUserCreate,
		"created user: "+req.Email, middleware.ClientIP(r), r.UserAgent())

	h.writeJSON(w, map[string]interface{}{"id": user.ID, "ok": true})
}

func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	targetID, _ := strconv.ParseInt(vars["id"], 10, 64)

	var req struct {
		IsActive   *bool  `json:"is_active"`
		Password   string `json:"password"`
		Role       string `json:"role"`
		DisableMFA bool   `json:"disable_mfa"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.IsActive != nil {
		if err := h.db.SetUserActive(targetID, *req.IsActive); err != nil {
			h.writeError(w, http.StatusInternalServerError, "failed to update user")
			return
		}
	}
	if req.Password != "" {
		if len(req.Password) < 8 {
			h.writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		if err := h.db.UpdateUserPassword(targetID, req.Password); err != nil {
			h.writeError(w, http.StatusInternalServerError, "failed to update password")
			return
		}
	}
	if req.DisableMFA {
		if err := h.db.AdminDisableMFAByID(targetID); err != nil {
			h.writeError(w, http.StatusInternalServerError, "failed to disable MFA")
			return
		}
	}

	adminID := middleware.GetUserID(r)
	h.db.WriteAudit(&adminID, models.AuditUserUpdate,
		"updated user id:"+vars["id"], middleware.ClientIP(r), r.UserAgent())

	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *AdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	targetID, _ := strconv.ParseInt(vars["id"], 10, 64)

	adminID := middleware.GetUserID(r)
	if targetID == adminID {
		h.writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}

	if err := h.db.DeleteUser(targetID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}

	h.db.WriteAudit(&adminID, models.AuditUserDelete,
		"deleted user id:"+vars["id"], middleware.ClientIP(r), r.UserAgent())
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ---- Audit Log ----

func (h *AdminHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	page := 1
	pageSize := 100
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	eventFilter := r.URL.Query().Get("event")

	result, err := h.db.ListAuditLogs(page, pageSize, eventFilter)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to fetch logs")
		return
	}
	h.writeJSON(w, result)
}

// ---- App Settings ----

func (h *AdminHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := config.GetSettings()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to read settings")
		return
	}
	h.writeJSON(w, settings)
}

func (h *AdminHandler) SetSettings(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetUserID(r)

	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	changed, err := config.SetSettings(updates)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save settings")
		return
	}

	if len(changed) > 0 {
		detail := "changed config keys: " + strings.Join(changed, ", ")
		h.db.WriteAudit(&adminID, models.AuditConfigChange,
			detail, middleware.ClientIP(r), r.UserAgent())
	}

	h.writeJSON(w, map[string]interface{}{
		"ok":      true,
		"changed": changed,
	})
}
