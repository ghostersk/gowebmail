// Package middleware provides HTTP middleware for GoMail.
package middleware

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/yourusername/gomail/config"
	"github.com/yourusername/gomail/internal/db"
	"github.com/yourusername/gomail/internal/models"
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
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://api.qrserver.com;")
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
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			} else {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
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
