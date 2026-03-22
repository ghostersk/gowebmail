package handlers

import (
	"net/http"

	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/db"
)

// AppHandler serves the main app pages using the shared Renderer.
type AppHandler struct {
	db       *db.DB
	cfg      *config.Config
	renderer *Renderer
}

func (h *AppHandler) Index(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "app", nil)
}

// ViewMessage renders a single message in a full browser tab.
func (h *AppHandler) ViewMessage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "message", nil)
}

// ComposePage renders the compose form in a full browser tab.
func (h *AppHandler) ComposePage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, "compose", nil)
}
