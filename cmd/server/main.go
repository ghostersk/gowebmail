package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ghostersk/gowebmail"
	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/handlers"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/syncer"

	"github.com/gorilla/mux"
)

func main() {
	// ── CLI admin commands (run without starting the HTTP server) ──────────
	// Usage:
	//   ./gowebmail --list-admin              list all admin usernames
	//   ./gowebmail --pw <username> <pass>    reset an admin's password
	//   ./gowebmail --mfa-off <username>      disable MFA for an admin
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--list-admin":
			runListAdmins()
			return
		case "--pw":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: gowebmail --pw <username> \"<password>\"")
				os.Exit(1)
			}
			runResetPassword(args[1], args[2])
			return
		case "--mfa-off":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: gowebmail --mfa-off <username>")
				os.Exit(1)
			}
			runDisableMFA(args[1])
			return
		case "--help", "-h":
			printHelp()
			return
		}
	}

	// ── Normal server startup ──────────────────────────────────────────────
	staticFS, err := fs.Sub(gowebmail.WebFS, "web/static")
	if err != nil {
		log.Fatalf("embed static fs: %v", err)
	}
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

	// Custom error handlers for non-API paths
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		middleware.ServeErrorPage(w, req, http.StatusNotFound, "Page Not Found", "The page you're looking for doesn't exist or has been moved.")
	})
	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		middleware.ServeErrorPage(w, req, http.StatusMethodNotAllowed, "Method Not Allowed", "This request method is not supported for this URL.")
	})

	// Static files
	r.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))),
	)
	r.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := gowebmail.WebFS.ReadFile("web/static/img/favicon.png")
		if err != nil {
			log.Printf("favicon error: %v", err)
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	})
	// Public auth routes
	auth := r.PathPrefix("/auth").Subrouter()
	auth.HandleFunc("/login", h.Auth.ShowLogin).Methods("GET")
	auth.Handle("/login", middleware.BruteForceProtect(database, cfg, http.HandlerFunc(h.Auth.Login))).Methods("POST")
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
	adminUI.HandleFunc("/security", h.Admin.ShowAdmin).Methods("GET")

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
	api.HandleFunc("/accounts/detect", h.API.DetectMailSettings).Methods("POST")
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
	api.HandleFunc("/messages/{id:[0-9]+}/download.eml", h.API.DownloadEML).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}/attachments", h.API.ListAttachments).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}/attachments/{att_id:[0-9]+}", h.API.DownloadAttachment).Methods("GET")
	api.HandleFunc("/messages/{id:[0-9]+}", h.API.DeleteMessage).Methods("DELETE")
	api.HandleFunc("/messages/starred", h.API.StarredMessages).Methods("GET")

	// Remote content whitelist
	api.HandleFunc("/remote-content-whitelist", h.API.GetRemoteContentWhitelist).Methods("GET")
	api.HandleFunc("/remote-content-whitelist", h.API.AddRemoteContentWhitelist).Methods("POST")

	// Send
	api.HandleFunc("/send", h.API.SendMessage).Methods("POST")
	api.HandleFunc("/reply", h.API.ReplyMessage).Methods("POST")
	api.HandleFunc("/forward", h.API.ForwardMessage).Methods("POST")
	api.HandleFunc("/forward-attachment", h.API.ForwardAsAttachment).Methods("POST")
	api.HandleFunc("/draft", h.API.SaveDraft).Methods("POST")

	// Folders
	api.HandleFunc("/folders", h.API.ListFolders).Methods("GET")
	api.HandleFunc("/folders/{account_id:[0-9]+}", h.API.ListAccountFolders).Methods("GET")
	api.HandleFunc("/folders/{id:[0-9]+}/sync", h.API.SyncFolder).Methods("POST")
	api.HandleFunc("/folders/{id:[0-9]+}/visibility", h.API.SetFolderVisibility).Methods("PUT")
	api.HandleFunc("/folders/{id:[0-9]+}/count", h.API.CountFolderMessages).Methods("GET")
	api.HandleFunc("/folders/{id:[0-9]+}/move-to/{toId:[0-9]+}", h.API.MoveFolderContents).Methods("POST")
	api.HandleFunc("/folders/{id:[0-9]+}/empty", h.API.EmptyFolder).Methods("POST")
	api.HandleFunc("/folders/{id:[0-9]+}/mark-all-read", h.API.MarkFolderAllRead).Methods("POST")
	api.HandleFunc("/folders/{id:[0-9]+}", h.API.DeleteFolder).Methods("DELETE")
	api.HandleFunc("/accounts/{account_id:[0-9]+}/enable-all-sync", h.API.EnableAllFolderSync).Methods("POST")
	api.HandleFunc("/poll", h.API.PollUnread).Methods("GET")
	api.HandleFunc("/new-messages", h.API.NewMessagesSince).Methods("GET")

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
	adminAPI.HandleFunc("/ip-blocks", h.Admin.ListIPBlocks).Methods("GET")
	adminAPI.HandleFunc("/ip-blocks", h.Admin.AddIPBlock).Methods("POST")
	adminAPI.HandleFunc("/ip-blocks/{ip}", h.Admin.RemoveIPBlock).Methods("DELETE")
	adminAPI.HandleFunc("/login-attempts", h.Admin.ListLoginAttempts).Methods("GET")

	// Periodically purge expired IP blocks
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			database.PurgeExpiredBlocks()
		}
	}()

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
		log.Printf("GoWebMail listening on %s", cfg.ListenAddr)
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

// ── CLI helpers ────────────────────────────────────────────────────────────

func openDB() (*db.DB, func()) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	database, err := db.New(cfg.DBPath, cfg.EncryptionKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	return database, func() { database.Close() }
}

func runListAdmins() {
	database, close := openDB()
	defer close()

	admins, err := database.AdminListAdmins()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(admins) == 0 {
		fmt.Println("No admin accounts found.")
		return
	}
	fmt.Printf("%-24s  %-36s  %s\n", "USERNAME", "EMAIL", "MFA")
	fmt.Printf("%-24s  %-36s  %s\n", "--------", "-----", "---")
	for _, a := range admins {
		mfaStatus := "off"
		if a.MFAEnabled {
			mfaStatus = "ON"
		}
		fmt.Printf("%-24s  %-36s  %s\n", a.Username, a.Email, mfaStatus)
	}
}

func runResetPassword(username, password string) {
	if len(password) < 8 {
		fmt.Fprintln(os.Stderr, "Error: password must be at least 8 characters")
		os.Exit(1)
	}
	database, close := openDB()
	defer close()

	if err := database.AdminResetPassword(username, password); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Password updated for admin '%s'.\n", username)
}

func runDisableMFA(username string) {
	database, close := openDB()
	defer close()

	if err := database.AdminDisableMFA(username); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("MFA disabled for admin '%s'. They can now log in with password only.\n", username)
}

func printHelp() {
	fmt.Print(`GoWebMail — Admin CLI

Usage:
  gowebmail                          Start the mail server
  gowebmail --list-admin             List all admin accounts (username, email, MFA status)
  gowebmail --pw <username> <pass>   Reset password for an admin account
  gowebmail --mfa-off <username>     Disable MFA for an admin account

Examples:
  ./gowebmail --list-admin
  ./gowebmail --pw admin "NewSecurePass123"
  ./gowebmail --mfa-off admin

Note: These commands only work on admin accounts.
      Regular user management is done through the web UI.
      Requires the same environment variables as the server (DB_PATH, ENCRYPTION_KEY, etc).
`)
}
