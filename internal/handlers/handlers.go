package handlers

import (
	"log"

	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/pgp"
	"github.com/ghostersk/gowebmail/internal/syncer"
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

	// Shared unlocked-PGP-key cache: populated by APIHandler.PGPUnlock, cleared by
	// AuthHandler.Logout. No TTL — memory-bounded by active sessions (see internal/pgp.Cache).
	pgpCache := pgp.NewCache()

	return &Handlers{
		Auth:  &AuthHandler{db: database, cfg: cfg, renderer: renderer, syncer: sc, pgpCache: pgpCache},
		App:   &AppHandler{db: database, cfg: cfg, renderer: renderer},
		API:   &APIHandler{db: database, cfg: cfg, syncer: sc, pgpCache: pgpCache},
		Admin: &AdminHandler{db: database, cfg: cfg, renderer: renderer},
	}
}
