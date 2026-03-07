# GoMail

A self-hosted, encrypted web email client written entirely in Go. Supports Gmail and Outlook via OAuth2, plus any standard IMAP/SMTP provider.

## Features

- **Unified inbox** — view emails from all connected accounts in one stream
- **Gmail & Outlook OAuth2** — modern, token-based auth (no storing raw passwords for these providers)
- **IMAP/SMTP** — connect any provider (ProtonMail Bridge, Fastmail, iCloud, etc.)
- **AES-256-GCM encryption** — all email content encrypted at rest in SQLite
- **bcrypt password hashing** — GoMail account passwords hashed with cost=12
- **Send / Reply / Forward** — full compose workflow
- **Folder navigation** — per-account folder/label browsing
- **Full-text search** — across all accounts locally
- **Dark-themed web UI** — clean, keyboard-shortcut-friendly interface

## Architecture

```
cmd/server/main.go          Entry point, HTTP server setup
config/config.go            Environment-based config
internal/
  auth/oauth.go             OAuth2 flows (Google + Microsoft)
  crypto/crypto.go          AES-256-GCM encryption + bcrypt
  db/db.go                  SQLite database with field-level encryption
  email/imap.go             IMAP fetch + SMTP send via XOAUTH2
  handlers/                 HTTP handlers (auth, app, api)
  middleware/middleware.go   Logger, auth guard, security headers
  models/models.go          Data models
web/static/
  login.html                Sign-in page
  register.html             Registration page
  app.html                  Single-page app (email client UI)
```

## Quick Start

### Option 1: Docker Compose (recommended)

```bash
# 1. Clone / copy the project
git clone https://github.com/yourname/gomail && cd gomail

# 2. Generate secrets
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export SESSION_SECRET=$(openssl rand -hex 32)
echo "ENCRYPTION_KEY=$ENCRYPTION_KEY"   # SAVE THIS — losing it means losing your email cache

# 3. Add your OAuth2 credentials to docker-compose.yml (see below)
# 4. Run
ENCRYPTION_KEY=$ENCRYPTION_KEY SESSION_SECRET=$SESSION_SECRET docker compose up
```

Visit http://localhost:8080, register an account, then connect your email.

### Option 2: Run directly

```bash
go build -o gomail ./cmd/server
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export SESSION_SECRET=$(openssl rand -hex 32)
./gomail
```

## Setting up OAuth2

### Gmail

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → New project
2. Enable **Gmail API**
3. Create **OAuth 2.0 Client ID** (Web application)
4. Add Authorized redirect URI: `http://localhost:8080/auth/gmail/callback`
5. Set env vars: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`

> **Important:** In the Google Cloud Console, add the scope `https://mail.google.com/` to allow IMAP access. You'll also need to add test users while in "Testing" mode.

### Outlook / Microsoft 365

1. Go to [Azure portal](https://portal.azure.com/) → App registrations → New registration
2. Set redirect URI: `http://localhost:8080/auth/outlook/callback`
3. Under API permissions, add:
   - `https://outlook.office.com/IMAP.AccessAsUser.All`
   - `https://outlook.office.com/SMTP.Send`
   - `offline_access`, `openid`, `profile`, `email`
4. Create a Client secret
5. Set env vars: `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_TENANT_ID`

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `ENCRYPTION_KEY` | **Yes** | 64-char hex string (32 bytes). Auto-generated on first run but must be persisted. |
| `SESSION_SECRET` | **Yes** | Random string for session signing. |
| `LISTEN_ADDR` | No | Default `:8080` |
| `DB_PATH` | No | Default `./data/gomail.db` |
| `BASE_URL` | No | Default `http://localhost:8080` |
| `GOOGLE_CLIENT_ID` | For Gmail | Google OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | For Gmail | Google OAuth2 client secret |
| `GOOGLE_REDIRECT_URL` | No | Default `{BASE_URL}/auth/gmail/callback` |
| `MICROSOFT_CLIENT_ID` | For Outlook | Azure AD app client ID |
| `MICROSOFT_CLIENT_SECRET` | For Outlook | Azure AD app client secret |
| `MICROSOFT_TENANT_ID` | No | Default `common` (multi-tenant) |
| `SECURE_COOKIE` | No | Set `true` in production (HTTPS only) |

## Security Notes

- **ENCRYPTION_KEY** is critical — back it up. Without it, the encrypted SQLite database is unreadable.
- Email content (subject, from, to, body) is encrypted at rest using AES-256-GCM.
- OAuth2 tokens are stored encrypted in the database.
- Passwords for GoMail accounts are bcrypt hashed (cost=12).
- All HTTP responses include security headers (CSP, X-Frame-Options, etc.).
- In production, run behind HTTPS (nginx/Caddy) and set `SECURE_COOKIE=true`.

## Keyboard Shortcuts

| Shortcut | Action |
|---|---|
| `Ctrl/Cmd + N` | Compose new message |
| `Ctrl/Cmd + K` | Focus search |
| `Escape` | Close compose / modal |

## Dependencies

```
github.com/emersion/go-imap      IMAP client
github.com/emersion/go-smtp      SMTP client  
github.com/emersion/go-message   MIME parsing
github.com/gorilla/mux           HTTP routing
github.com/mattn/go-sqlite3      SQLite driver (CGO)
golang.org/x/crypto              bcrypt
golang.org/x/oauth2              OAuth2 + Google/Microsoft endpoints
```

## Building for Production

```bash
CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o gomail ./cmd/server
```

CGO is required by `go-sqlite3`. Cross-compilation requires a C cross-compiler.

## License

MIT
