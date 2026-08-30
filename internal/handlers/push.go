package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/ghostersk/gowebmail/internal/middleware"
)

// GetVAPIDPublicKey exposes the server's VAPID public key so the frontend can pass it to
// PushManager.subscribe({applicationServerKey: ...}).
func (h *APIHandler) GetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]string{"public_key": h.cfg.VAPIDPublicKey})
}

// SubscribePush stores a browser/WebView's PushSubscription (from
// PushManager.subscribe().toJSON()) so background new-mail push can reach it.
func (h *APIHandler) SubscribePush(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 8*1024))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var sub webpush.Subscription
	if err := json.Unmarshal(body, &sub); err != nil || sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		h.writeError(w, http.StatusBadRequest, "invalid push subscription")
		return
	}
	if err := h.db.UpsertPushSubscription(userID, sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save subscription")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// UnsubscribePush removes a subscription by endpoint (sent when the user disables the
// notifications toggle, or the browser reports the subscription changed/expired).
func (h *APIHandler) UnsubscribePush(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 8*1024))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Endpoint == "" {
		h.writeError(w, http.StatusBadRequest, "endpoint required")
		return
	}
	if err := h.db.DeletePushSubscription(userID, req.Endpoint); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to remove subscription")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}
