package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ghostersk/gowebmail/internal/middleware"
)

func (h *APIHandler) ListSignatures(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	sigs, err := h.db.ListSignatures(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list signatures")
		return
	}
	h.writeJSON(w, sigs)
}

func (h *APIHandler) CreateSignature(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Name        string `json:"name"`
		ContentHTML string `json:"content_html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		h.writeError(w, http.StatusBadRequest, "name required")
		return
	}
	id, err := h.db.CreateSignature(userID, req.Name, req.ContentHTML)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create signature")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) UpdateSignature(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	var req struct {
		Name        string `json:"name"`
		ContentHTML string `json:"content_html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		h.writeError(w, http.StatusBadRequest, "name required")
		return
	}
	existing, err := h.db.GetSignature(userID, id)
	if err != nil || existing == nil {
		h.writeError(w, http.StatusNotFound, "signature not found")
		return
	}
	if err := h.db.UpdateSignature(userID, id, req.Name, req.ContentHTML); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update signature")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) DeleteSignature(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeleteSignature(userID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete signature")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

// SetSignatureDefaults sets which signature (if any) is default-for-new / default-for-reply
// on one account. A ProviderID of 0 in the request clears that default.
func (h *APIHandler) SetSignatureDefaults(w http.ResponseWriter, r *http.Request) {
	accountID := pathInt64(r, "id")
	if !h.ownAccount(w, r, accountID) {
		return
	}
	var req struct {
		DefaultNewID   int64 `json:"default_new_id"`
		DefaultReplyID int64 `json:"default_reply_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// A signature id of 0 is "clear this default" — otherwise verify the user actually owns it.
	userID := middleware.GetUserID(r)
	if req.DefaultNewID > 0 {
		if s, err := h.db.GetSignature(userID, req.DefaultNewID); err != nil || s == nil {
			h.writeError(w, http.StatusBadRequest, "invalid default_new_id")
			return
		}
	}
	if req.DefaultReplyID > 0 {
		if s, err := h.db.GetSignature(userID, req.DefaultReplyID); err != nil || s == nil {
			h.writeError(w, http.StatusBadRequest, "invalid default_reply_id")
			return
		}
	}
	if err := h.db.SetSignatureDefaults(accountID, req.DefaultNewID, req.DefaultReplyID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to set defaults")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}
