package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
)

var validRuleFields = map[string]bool{
	"from": true, "to": true, "subject": true, "body": true, "has_attachment": true, "recipient_type": true,
}
var validRuleOps = map[string]bool{"contains": true, "equals": true, "starts_with": true}
var validRuleActions = map[string]bool{
	"move_to_folder": true, "delete": true, "mark_read": true, "mark_as_spam": true, "forward": true, "auto_reply": true,
}

// ownAccount verifies accountID belongs to the current user, writing a 404 and returning false if not.
func (h *APIHandler) ownAccount(w http.ResponseWriter, r *http.Request, accountID int64) bool {
	userID := middleware.GetUserID(r)
	account, err := h.db.GetAccount(accountID)
	if err != nil || account == nil || account.UserID != userID {
		h.writeError(w, http.StatusNotFound, "account not found")
		return false
	}
	return true
}

// ---- Rules ----

func (h *APIHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	rules, err := h.db.ListRules(accountID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list rules")
		return
	}
	h.writeJSON(w, rules)
}

func validateRule(r *models.Rule) string {
	if strings.TrimSpace(r.Name) == "" {
		return "name required"
	}
	if len(r.Conditions) == 0 {
		return "at least one condition required"
	}
	for _, c := range r.Conditions {
		if !validRuleFields[c.Field] {
			return "invalid condition field: " + c.Field
		}
		if !validRuleOps[c.Op] {
			return "invalid condition op: " + c.Op
		}
		if strings.TrimSpace(c.Value) == "" {
			return "condition value required"
		}
	}
	if r.MatchType != "any" {
		r.MatchType = "all"
	}
	if !validRuleActions[r.Action] {
		return "invalid action: " + r.Action
	}
	if r.Action == "move_to_folder" && strings.TrimSpace(r.ActionValue) == "" {
		return "folder name required for move_to_folder"
	}
	if r.Action == "forward" && !strings.Contains(r.ActionValue, "@") {
		return "valid forward address required"
	}
	if r.Action == "auto_reply" && strings.TrimSpace(r.ActionValue) == "" {
		return "auto-reply subject required"
	}
	return ""
}

func (h *APIHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	var req models.Rule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.AccountID == 0 || !h.ownAccount(w, r, req.AccountID) {
		if req.AccountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	if msg := validateRule(&req); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	id, err := h.db.CreateRule(&req)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create rule")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id := pathInt64(r, "id")
	var req models.Rule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.AccountID == 0 || !h.ownAccount(w, r, req.AccountID) {
		if req.AccountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	existing, err := h.db.GetRule(req.AccountID, id)
	if err != nil || existing == nil {
		h.writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if msg := validateRule(&req); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	req.ID = id
	if err := h.db.UpdateRule(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update rule")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id := pathInt64(r, "id")
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	if err := h.db.DeleteRule(accountID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete rule")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}
