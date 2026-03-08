// Package middleware provides HTTP middleware for GoWebMail.
package middleware

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/geo"
	"github.com/ghostersk/gowebmail/internal/models"
	"github.com/ghostersk/gowebmail/internal/notify"
)

type contextKey string

const (
	UserIDKey   contextKey = "user_id"
	UserRoleKey contextKey = "user_role"
)

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src * data: blob: cid:; frame-src 'self' blob: data:;")
		next.ServeHTTP(w, r)
	})
}

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func JSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// RequireAuth validates the session, enforces MFA, injects user context.
func RequireAuth(database *db.DB, cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("gomail_session")
			if err != nil || cookie.Value == "" {
				redirectToLogin(w, r)
				return
			}

			userID, mfaVerified, err := database.GetSession(cookie.Value)
			if err != nil || userID == 0 {
				clearSessionCookie(w, cfg)
				redirectToLogin(w, r)
				return
			}

			user, err := database.GetUserByID(userID)
			if err != nil || user == nil || !user.IsActive {
				clearSessionCookie(w, cfg)
				redirectToLogin(w, r)
				return
			}

			// MFA gate: if enabled but not yet verified this session
			if user.MFAEnabled && !mfaVerified {
				if r.URL.Path != "/auth/mfa" && r.URL.Path != "/auth/mfa/verify" {
					http.Redirect(w, r, "/auth/mfa", http.StatusFound)
					return
				}
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			ctx = context.WithValue(ctx, UserRoleKey, user.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin rejects non-admin users with 403.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(UserRoleKey).(models.UserRole)
		if role != models.RoleAdmin {
			if isAPIPath(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":"forbidden"}`)
			} else {
				renderErrorPage(w, r, http.StatusForbidden,
					"Access Denied",
					"You don't have permission to access this page. Admin privileges are required.")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if isAPIPath(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func clearSessionCookie(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name: "gomail_session", Value: "", MaxAge: -1, Path: "/",
		Secure: cfg.SecureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func isAPIPath(r *http.Request) bool {
	return len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api"
}

func GetUserID(r *http.Request) int64 {
	id, _ := r.Context().Value(UserIDKey).(int64)
	return id
}

func GetUserRole(r *http.Request) models.UserRole {
	role, _ := r.Context().Value(UserRoleKey).(models.UserRole)
	return role
}

func ClientIP(r *http.Request) string {
	// Use X-Forwarded-For as-is for logging — proxy trust is enforced at config level
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// BruteForceProtect wraps the login POST handler with rate-limiting and geo-blocking.
// It must be called with the raw handler so it can intercept BEFORE auth.
func BruteForceProtect(database *db.DB, cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := cfg.RealIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))

		// Whitelist check runs FIRST — whitelisted IPs bypass all blocking entirely.
		if cfg.IsIPWhitelisted(ip) {
			next.ServeHTTP(w, r)
			return
		}

		// Resolve country for geo-block and attempt recording.
		// Only do a live lookup for non-GET to save API quota; GET uses cache only.
		geoResult := geo.Lookup(ip)

		// --- Geo block (apply to all requests) ---
		if geoResult.CountryCode != "" {
			if !cfg.IsCountryAllowed(geoResult.CountryCode) {
				log.Printf("geo-block: %s (%s %s)", ip, geoResult.CountryCode, geoResult.Country)
				renderErrorPage(w, r, http.StatusForbidden,
					"Access Denied",
					"Access from your country is not permitted.")
				return
			}
		}

		if !cfg.BruteEnabled || r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		// Check if already blocked
		if database.IsIPBlocked(ip) {
			renderErrorPage(w, r, http.StatusForbidden,
				"IP Address Blocked",
				"Your IP address has been temporarily blocked due to too many failed login attempts. Please contact the administrator.")
			return
		}

		// Wrap the response writer to detect a failed login (redirect to error vs success)
		rw := &loginResponseCapture{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)

		// Determine success: a redirect away from login = success
		success := rw.statusCode == http.StatusFound && !strings.Contains(rw.location, "error=")
		username := r.FormValue("username")
		database.RecordLoginAttempt(ip, username, geoResult.Country, geoResult.CountryCode, success)

		if !success && !rw.skipBrute {
			failures := database.CountRecentFailures(ip, cfg.BruteWindowMins)
			if failures >= cfg.BruteMaxAttempts {
				reason := "Too many failed logins"
				database.BlockIP(ip, reason, geoResult.Country, geoResult.CountryCode, failures, cfg.BruteBanHours)
				log.Printf("brute-force block: %s (%d failures in %d min, ban %d hrs)",
					ip, failures, cfg.BruteWindowMins, cfg.BruteBanHours)

				// Send security notification to the targeted user (non-blocking goroutine)
				go func(targetUsername string) {
					user, _ := database.GetUserByUsername(targetUsername)
					if user == nil {
						user, _ = database.GetUserByEmail(targetUsername)
					}
					if user != nil && user.Email != "" {
						notify.SendBruteForceAlert(cfg, notify.BruteForceAlert{
							Username:    user.Username,
							ToEmail:     user.Email,
							AttackerIP:  ip,
							Country:     geoResult.Country,
							CountryCode: geoResult.CountryCode,
							Attempts:    failures,
							BlockedAt:   time.Now().UTC(),
							BanHours:    cfg.BruteBanHours,
							Hostname:    cfg.Hostname,
						})
					}
				}(username)
			}
		}
	})
}

// loginResponseCapture captures the redirect location and skip-brute signal from the login handler.
type loginResponseCapture struct {
	http.ResponseWriter
	statusCode int
	location   string
	skipBrute  bool
}

func (lrc *loginResponseCapture) WriteHeader(code int) {
	lrc.statusCode = code
	lrc.location = lrc.ResponseWriter.Header().Get("Location")
	if lrc.Header().Get("X-Skip-Brute") == "1" {
		lrc.skipBrute = true
		lrc.Header().Del("X-Skip-Brute") // strip before sending to client
	}
	lrc.ResponseWriter.WriteHeader(code)
}

// ServeErrorPage is the public wrapper used by main.go for 404/405 handlers.
func ServeErrorPage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	renderErrorPage(w, r, status, title, message)
}

// renderErrorPage writes a themed HTML error page for browser requests,
// or a JSON error for API paths.
func renderErrorPage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	if isAPIPath(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q}`, message)
		return
	}
	// Decide back-button destination: if the user has a session cookie they're
	// likely logged in, so send them home. Otherwise send to login.
	backHref := "/auth/login"
	backLabel := "← Back to Login"
	if _, err := r.Cookie("gomail_session"); err == nil {
		backHref = "/"
		backLabel = "← Go to Home"
	}

	data := struct {
		Status   int
		Title    string
		Message  string
		BackHref string
		BackLabel string
	}{status, title, message, backHref, backLabel}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := errorPageTmpl.Execute(w, data); err != nil {
		// Last-resort plain text fallback
		fmt.Fprintf(w, "%d %s: %s", status, title, message)
	}
}

var errorPageTmpl = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Status}} – {{.Title}}</title>
<link href="https://fonts.googleapis.com/css2?family=DM+Sans:wght@300;400;500&display=swap" rel="stylesheet">
<link rel="stylesheet" href="/static/css/gowebmail.css">
<style>
  html, body { height: 100%; margin: 0; }
  .error-page {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg, #18191b);
    font-family: 'DM Sans', sans-serif;
  }
  .error-card {
    background: var(--surface, #232428);
    border: 1px solid var(--border, #2e2f34);
    border-radius: 16px;
    padding: 48px 56px;
    text-align: center;
    max-width: 480px;
    width: 90%;
    box-shadow: 0 8px 32px rgba(0,0,0,.4);
  }
  .error-code {
    font-size: 64px;
    font-weight: 700;
    color: var(--accent, #6b8afd);
    line-height: 1;
    margin: 0 0 8px;
    letter-spacing: -2px;
  }
  .error-title {
    font-size: 20px;
    font-weight: 600;
    color: var(--text, #e8e9ed);
    margin: 0 0 12px;
  }
  .error-message {
    font-size: 14px;
    color: var(--muted, #8b8d97);
    line-height: 1.6;
    margin: 0 0 32px;
  }
  .error-back {
    display: inline-block;
    padding: 10px 24px;
    background: var(--accent, #6b8afd);
    color: #fff;
    border-radius: 8px;
    text-decoration: none;
    font-size: 14px;
    font-weight: 500;
    transition: opacity .15s;
  }
  .error-back:hover { opacity: .85; }
</style>
</head>
<body>
<div class="error-page">
  <div class="error-card">
    <div class="error-code">{{.Status}}</div>
    <h1 class="error-title">{{.Title}}</h1>
    <p class="error-message">{{.Message}}</p>
    <a href="{{.BackHref}}" class="error-back">{{.BackLabel}}</a>
  </div>
</div>
</body>
</html>`))
