package handlers

import (
	"log"

	"github.com/yourusername/gomail/config"
	"github.com/yourusername/gomail/internal/db"
	"github.com/yourusername/gomail/internal/syncer"
)

type Handlers struct {
	Auth  *AuthHandler
	App   *AppHandler
	API   *APIHandler
	Admin *AdminHandler
}

func New(database *db.DB, cfg *config.Config, sc *syncer.Scheduler) *Handlers {
	renderer, err := NewRenderer()
	if err != nil {
		log.Fatalf("failed to load templates: %v", err)
	}

	return &Handlers{
		Auth:  &AuthHandler{db: database, cfg: cfg, renderer: renderer},
		App:   &AppHandler{db: database, cfg: cfg, renderer: renderer},
		API:   &APIHandler{db: database, cfg: cfg, syncer: sc},
		Admin: &AdminHandler{db: database, cfg: cfg, renderer: renderer},
	}
}
