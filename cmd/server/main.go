package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourusername/gomail/config"
	"github.com/yourusername/gomail/internal/db"
	"github.com/yourusername/gomail/internal/handlers"
	"github.com/yourusername/gomail/internal/middleware"
	"github.com/yourusername/gomail/internal/syncer"

	"github.com/gorilla/mux"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load: %v", err)
	}

	database, err := db.New(cfg.DBPath, cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	sc := syncer.New(database)
	sc.Start()
	defer sc.Stop()

	r := mux.NewRouter()
	h := handlers.New(database, cfg, sc)

	r.Use(middleware.Logger)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS)
	r.Use(cfg.HostCheckMiddleware)

	// Static files
	r.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir("./web/static/"))),
	)

	// Public auth routes
	auth := r.PathPrefix("/auth").Subrouter()
	auth.HandleFunc("/login", h.Auth.ShowLogin).Methods("GET")
	auth.HandleFunc("/login", h.Auth.Login).Methods("POST")
	auth.HandleFunc("/logout", h.Auth.Logout).Methods("POST")

	// MFA (session exists but mfa_verified=0)
	mfaR := r.PathPrefix("/auth/mfa").Subrouter()
	mfaR.Use(middleware.RequireAuth(database, cfg))
	mfaR.HandleFunc("", h.Auth.ShowMFA).Methods("GET")
	mfaR.HandleFunc("/verify", h.Auth.VerifyMFA).Methods("POST")

	// OAuth callbacks (require auth to associate with user)
	oauthR := r.PathPrefix("/auth").Subrouter()
	oauthR.Use(middleware.RequireAuth(database, cfg))
	oauthR.HandleFunc("/gmail/connect", h.Auth.GmailConnect).Methods("GET")
	oauthR.HandleFunc("/gmail/callback", h.Auth.GmailCallback).Methods("GET")
	oauthR.HandleFunc("/outlook/connect", h.Auth.OutlookConnect).Methods("GET")
	oauthR.HandleFunc("/outlook/callback", h.Auth.OutlookCallback).Methods("GET")

	// App
	app := r.PathPrefix("").Subrouter()
	app.Use(middleware.RequireAuth(database, cfg))
	app.HandleFunc("/", h.App.Index).Methods("GET")

	// Admin UI
	adminUI := r.PathPrefix("/admin").Subrouter()
	adminUI.Use(middleware.RequireAuth(database, cfg))
	adminUI.Use(middleware.RequireAdmin)
	adminUI.HandleFunc("", h.Admin.ShowAdmin).Methods("GET")
	adminUI.HandleFunc("/", h.Admin.ShowAdmin).Methods("GET")
	adminUI.HandleFunc("/settings", h.Admin.ShowAdmin).Methods("GET")
	adminUI.HandleFunc("/audit", h.Admin.ShowAdmin).Methods("GET")

	// API
	api := r.PathPrefix("/api").Subrouter()
	api.Use(middleware.RequireAuth(database, cfg))
	api.Use(middleware.JSONContentType)

	// Profile / auth
	api.HandleFunc("/me", h.Auth.Me).Methods("GET")
	api.HandleFunc("/change-password", h.Auth.ChangePassword).Methods("POST")
	api.HandleFunc("/mfa/setup", h.Auth.MFASetupBegin).Methods("POST")
	api.HandleFunc("/mfa/confirm", h.Auth.MFASetupConfirm).Methods("POST")
	api.HandleFunc("/mfa/disable", h.Auth.MFADisable).Methods("POST")

	// Providers (which OAuth providers are configured)
	api.HandleFunc("/providers", h.API.GetProviders).Methods("GET")

	// Accounts
	api.HandleFunc("/accounts", h.API.ListAccounts).Methods("GET")
	api.HandleFunc("/accounts", h.API.AddAccount).Methods("POST")
	api.HandleFunc("/accounts/test", h.API.TestConnection).Methods("POST")
	api.HandleFunc("/accounts/{id:[0-9]+}", h.API.GetAccount).Methods("GET")
	api.HandleFunc("/accounts/{id:[0-9]+}", h.API.UpdateAccount).Methods("PUT")
	api.HandleFunc("/accounts/{id:[0-9]+}", h.API.DeleteAccount).Methods("DELETE")
	api.HandleFunc("/accounts/{id:[0-9]+}/sync", h.API.SyncAccount).Methods("POST")
	api.HandleFunc("/accounts/{id:[0-9]+}/sync-settings", h.API.SetAccountSyncSettings).Methods("PUT")

	// Messages
	api.HandleFunc("/messages", h.API.ListMessages).Methods("GET")
	api.HandleFunc("/messages/unified", h.API.UnifiedInbox).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}", h.API.GetMessage).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}/read", h.API.MarkRead).Methods("PUT")
	api.HandleFunc("/messages/{id:[0-9]+}/star", h.API.ToggleStar).Methods("PUT")
	api.HandleFunc("/messages/{id:[0-9]+}/move", h.API.MoveMessage).Methods("PUT")
	api.HandleFunc("/messages/{id:[0-9]+}/headers", h.API.GetMessageHeaders).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}", h.API.DeleteMessage).Methods("DELETE")

	// Remote content whitelist
	api.HandleFunc("/remote-content-whitelist", h.API.GetRemoteContentWhitelist).Methods("GET")
	api.HandleFunc("/remote-content-whitelist", h.API.AddRemoteContentWhitelist).Methods("POST")

	// Send
	api.HandleFunc("/send", h.API.SendMessage).Methods("POST")
	api.HandleFunc("/reply", h.API.ReplyMessage).Methods("POST")
	api.HandleFunc("/forward", h.API.ForwardMessage).Methods("POST")

	// Folders
	api.HandleFunc("/folders", h.API.ListFolders).Methods("GET")
	api.HandleFunc("/folders/{account_id:[0-9]+}", h.API.ListAccountFolders).Methods("GET")
	api.HandleFunc("/folders/{id:[0-9]+}/sync", h.API.SyncFolder).Methods("POST")
	api.HandleFunc("/folders/{id:[0-9]+}/visibility", h.API.SetFolderVisibility).Methods("PUT")

	api.HandleFunc("/sync-interval", h.API.GetSyncInterval).Methods("GET")
	api.HandleFunc("/sync-interval", h.API.SetSyncInterval).Methods("PUT")
	api.HandleFunc("/compose-popup", h.API.SetComposePopup).Methods("PUT")

	// Search
	api.HandleFunc("/search", h.API.Search).Methods("GET")

	// Admin API
	adminAPI := r.PathPrefix("/api/admin").Subrouter()
	adminAPI.Use(middleware.RequireAuth(database, cfg))
	adminAPI.Use(middleware.JSONContentType)
	adminAPI.Use(middleware.RequireAdmin)
	adminAPI.HandleFunc("/users", h.Admin.ListUsers).Methods("GET")
	adminAPI.HandleFunc("/users", h.Admin.CreateUser).Methods("POST")
	adminAPI.HandleFunc("/users/{id:[0-9]+}", h.Admin.UpdateUser).Methods("PUT")
	adminAPI.HandleFunc("/users/{id:[0-9]+}", h.Admin.DeleteUser).Methods("DELETE")
	adminAPI.HandleFunc("/audit", h.Admin.ListAuditLogs).Methods("GET")
	adminAPI.HandleFunc("/settings", h.Admin.GetSettings).Methods("GET")
	adminAPI.HandleFunc("/settings", h.Admin.SetSettings).Methods("PUT")

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("GoMail listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
