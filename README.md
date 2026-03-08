# GoWebMail

A self-hosted, multi-user, encrypted web email client written entirely in Go. Supports Gmail and Outlook via OAuth2, plus any standard IMAP/SMTP provider (Fastmail, ProtonMail Bridge, iCloud, etc.).

> **Notes:**
> - Work still in progress (Gmail and Outlook OAuth2 not yet fully tested in production)
> - AI-assisted development — suggestions and contributions very welcome!

## Features

### Email
- **Unified inbox** — view emails from all connected accounts in one stream
- **Gmail & Outlook OAuth2** — modern token-based auth (no raw passwords stored for these providers)
- **IMAP/SMTP** — connect any standard provider with username/password credentials
- **Auto-detect mail settings** — MX lookup + common port patterns to pre-fill IMAP/SMTP config
- **Send / Reply / Forward / Draft** — full compose workflow with floating draggable compose window
- **Attachments** — view inline images, download individual files or all at once
- **Forward as attachment** — attach original `.eml` as `message/rfc822`
- **Folder navigation** — per-account folder/label browsing with right-click context menu
- **Full-text search** — across all accounts and folders locally (no server-side search required)
- **Message filtering** — unread only, starred, has attachment, from/to filters
- **Bulk operations** — multi-select with Ctrl+click / Shift+range; bulk mark read/delete
- **Drag-and-drop** — move messages to folders; attach files in compose
- **Starred messages** — virtual folder across all accounts
- **EML download** — download raw message as `.eml`
- **Raw headers view** — fetches full RFC 822 headers from IMAP on demand

### Security
- **AES-256-GCM encryption** — all email content, credentials and OAuth tokens encrypted at rest in SQLite (field-level, not whole-DB encryption)
- **bcrypt password hashing** — GoWebMail account passwords hashed with cost=12
- **TOTP MFA** — custom implementation, no external library; ±60s window for clock skew tolerance
- **Brute-force IP blocking** — auto-blocks IPs after configurable failed login attempts (default: 5 attempts in 30 min → 12h ban); permanent blocks supported
- **Geo-blocking** — deny or allow-only access by country via ip-api.com (no API key needed); 24h in-memory cache
- **Per-user IP access rules** — each user configures their own IP allow-list or brute-force bypass list independently of global rules
- **Security alert emails** — notifies the targeted user when their account is brute-forced; supports STARTTLS, implicit TLS, and plain relay
- **DNS rebinding protection** — `HostCheckMiddleware` rejects requests with unexpected `Host` headers
- **Security headers** — CSP, X-Frame-Options, Referrer-Policy, Permissions-Policy, X-XSS-Protection on all responses
- **Sandboxed HTML email rendering** — emails rendered in CSP-sandboxed `<iframe>`; external links require confirmation before opening
- **Remote image blocking** — images blocked by default with per-sender whitelist
- **Styled HTTP error pages** — 403/404/405 served as themed pages matching the app (not plain browser defaults)

### Admin Panel (`/admin`)
- **User management** — create, edit (role, active status, password reset), delete users
- **Audit log** — paginated, filterable event log for all security-relevant actions
- **Security dashboard** — live blocked IPs table with attacker country, login attempt history per IP, manual block/unblock controls
- **App settings** — all runtime configuration editable from the UI; changes are written back to `gowebmail.conf`
- **MFA disable** — admin can disable MFA for any locked-out user
- **Password reset** — admin can reset any user's password from the web UI

### User Settings
- **Profile** — change username and email address (password confirmation required)
- **Password change** — change own password
- **TOTP MFA setup** — enable/disable via QR code scan
- **Sync interval** — per-user background sync frequency
- **Compose popup mode** — toggle floating window vs. browser popup window
- **Per-user IP rules** — three modes: `disabled` (global rules apply), `brute_skip` (listed IPs bypass lockout counter), `allow_only` (only listed IPs may log in to this account)

### UI
- **Dark-themed SPA** — clean, responsive vanilla-JS single-page app; no JavaScript framework
- **OS / browser notifications** — permission requested once; slide-in toast + OS push notification on new mail
- **Folder context menu** — right-click: sync, enable/disable sync, hide, empty trash/spam, move contents, delete
- **Compose window** — draggable floating window or browser popup; tag-input for To/CC/BCC; auto-saves draft every 60s

<img width="1213" height="848" alt="Inbox view" src="https://github.com/user-attachments/assets/955eda04-e358-4779-80e7-0a9b299ac110" />
<img width="1261" height="921" alt="Compose" src="https://github.com/user-attachments/assets/40ee58e8-6c4b-45c3-974d-98cc8ccc45a5" />
<img width="1153" height="907" alt="Admin Security panel" src="https://github.com/user-attachments/assets/ebc92335-f6b7-46ed-b9a2-84512f70e1b2" />
<img width="551" height="669" alt="Settings" src="https://github.com/user-attachments/assets/412585c0-434a-4177-ab04-7db69da9d08a" />

---

## Quick Start

### Option 1: Build executable

```bash
git clone https://github.com/ghostersk/gowebmail && cd gowebmail
go build -o gowebmail ./cmd/server
# Smaller binary (strip debug info):
go build -ldflags="-s -w" -o gowebmail ./cmd/server
./gowebmail
```

Visit `http://localhost:8080`. Default login: `admin` / `admin`.

### Option 2: Run directly

```bash
git clone https://github.com/ghostersk/gowebmail && cd gowebmail
go run ./cmd/server/main.go
# Check ./data/gowebmail.conf on first run — update as needed, then restart.
```

---

## Admin CLI Commands

All commands open the database directly without starting the HTTP server. They require the same environment variables or `data/gowebmail.conf` as the server.

```bash
# List all admin accounts with MFA status
./gowebmail --list-admin

# USERNAME                  EMAIL                                 MFA
# --------                  -----                                 ---
# admin                     admin@example.com                     ON

# Reset an admin's password (minimum 8 characters)
./gowebmail --pw admin "NewSecurePass123"

# Disable MFA for a locked-out admin
./gowebmail --mfa-off admin

# List all currently blocked IPs
# Shows: IP, username attempted, attempt count, blocked-at, expiry, time remaining
./gowebmail --blocklist

# IP                  USERNAME USED         TRIES  BLOCKED AT              EXPIRES                 REMAINING
# --                  -------------         -----  ----------              -------                 ---------
# 1.2.3.4             bob                   7      2026-03-08 14:22:01     2026-03-09 02:22:01     11h 34m
# 5.6.7.8             admin                 12     2026-03-07 09:10:00     permanent               ∞  (manual unblock)

# Remove a block immediately
./gowebmail --unblock 1.2.3.4
```

> **Note:** `--list-admin`, `--pw`, and `--mfa-off` only work on admin accounts. Regular user management is done through the web UI at `/admin`. `--blocklist` and `--unblock` are particularly useful if you have locked yourself out.

---

## Configuration

On first run, `data/gowebmail.conf` is auto-generated with all defaults and inline comments. All keys can also be set via environment variables. The Admin → Settings UI can edit and save most values at runtime, writing changes back to `gowebmail.conf`.

### Core

| Key | Default | Description |
|-----|---------|-------------|
| `HOSTNAME` | `localhost` | Public hostname for BaseURL construction and Host header validation |
| `LISTEN_ADDR` | `:8080` | Bind address |
| `SECURE_COOKIE` | `false` | Set `true` when running behind HTTPS |
| `TRUSTED_PROXIES` | _(blank)_ | Comma-separated IPs/CIDRs allowed to set `X-Forwarded-For` |
| `ENCRYPTION_KEY` | _(auto-generated)_ | AES-256 key — **back this up immediately**; losing it makes the DB unreadable |
| `SESSION_MAX_AGE` | `604800` | Session lifetime in seconds (default: 7 days) |

### Brute Force Protection

| Key | Default | Description |
|-----|---------|-------------|
| `BRUTE_ENABLED` | `true` | Enable automatic IP blocking on failed logins |
| `BRUTE_MAX_ATTEMPTS` | `5` | Failed login attempts before ban triggers |
| `BRUTE_WINDOW_MINUTES` | `30` | Rolling window in minutes for counting failures |
| `BRUTE_BAN_HOURS` | `12` | Ban duration in hours; `0` = permanent block (manual unblock required) |
| `BRUTE_WHITELIST_IPS` | _(blank)_ | Comma-separated IPs never blocked — **add your own IP here** |

### Geo Blocking

| Key | Default | Description |
|-----|---------|-------------|
| `GEO_BLOCK_COUNTRIES` | _(blank)_ | ISO country codes to deny outright (e.g. `CN,RU,KP`). Evaluated first — takes priority over allow list. |
| `GEO_ALLOW_COUNTRIES` | _(blank)_ | ISO country codes to allow exclusively (e.g. `SK,CZ,DE`). All other countries are denied. |

Geo lookups use [ip-api.com](http://ip-api.com) (free tier, no API key, ~45 req/min limit). Results are cached in-memory for 24 hours. Private/loopback IPs always bypass geo checks.

### Security Notification Emails

| Key | Default | Description |
|-----|---------|-------------|
| `NOTIFY_ENABLED` | `true` | Send alert email to user when a brute-force attack targets their account |
| `NOTIFY_SMTP_HOST` | _(blank)_ | SMTP hostname for sending alert emails |
| `NOTIFY_SMTP_PORT` | `587` | `465` = implicit TLS · `587` = STARTTLS · `25` = plain relay (no auth) |
| `NOTIFY_FROM` | _(blank)_ | Sender address (e.g. `security@yourdomain.com`) |
| `NOTIFY_USER` | _(blank)_ | SMTP auth username — leave blank for unauthenticated relay |
| `NOTIFY_PASS` | _(blank)_ | SMTP auth password — leave blank for unauthenticated relay |

---

## Setting up OAuth2

### Gmail

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → New project
2. Enable **Gmail API**
3. Create **OAuth 2.0 Client ID** (Web application type)
4. Add Authorized redirect URI: `<BASE_URL>/auth/gmail/callback`
5. Add scope `https://mail.google.com/` (required for full IMAP access)
6. Add test users while the app is in "Testing" mode
7. Set in config: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`

### Outlook / Microsoft 365

1. Go to [Azure portal](https://portal.azure.com/) → App registrations → New registration
2. Set redirect URI: `<BASE_URL>/auth/outlook/callback`
3. Under API permissions add:
   - `https://outlook.office.com/IMAP.AccessAsUser.All`
   - `https://outlook.office.com/SMTP.Send`
   - `offline_access`, `openid`, `profile`, `email`
4. Create a Client secret under Certificates & secrets
5. Set in config: `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_TENANT_ID`

---

## Security Notes

- **`ENCRYPTION_KEY` is critical** — back it up. Without it the encrypted SQLite database is permanently unreadable.
- Email content (subject, from, to, body), IMAP/SMTP credentials, and OAuth tokens are all encrypted at rest with AES-256-GCM at the field level.
- GoWebMail user passwords are bcrypt hashed (cost=12). Session tokens are 32-byte `crypto/rand` hex strings.
- All HTTP responses include security headers (CSP, X-Frame-Options, Referrer-Policy, etc.).
- HTML emails render in a CSP-sandboxed `<iframe>` — external links trigger a confirmation dialog before opening in a new tab.
- In production, run behind a reverse proxy with HTTPS (nginx / Caddy) and set `SECURE_COOKIE=true`.
- Add your own IP to `BRUTE_WHITELIST_IPS` to avoid ever locking yourself out. If it does happen, use `./gowebmail --unblock <ip>` — no server restart needed.

---

## Building for Production

```bash
CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o gowebmail ./cmd/server
```

CGO is required by `mattn/go-sqlite3`. Cross-compilation for other platforms requires a C cross-compiler (or use `zig cc` as a drop-in).

### Docker (example)

```dockerfile
FROM golang:1.22-alpine AS builder
RUN apk add gcc musl-dev sqlite-dev
WORKDIR /app
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o gowebmail ./cmd/server

FROM alpine:latest
RUN apk add --no-cache sqlite-libs ca-certificates
WORKDIR /app
COPY --from=builder /app/gowebmail .
EXPOSE 8080
CMD ["./gowebmail"]
```

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/emersion/go-imap v1.2.1` | IMAP client |
| `github.com/emersion/go-smtp` | SMTP client |
| `github.com/emersion/go-message` | MIME parsing |
| `github.com/gorilla/mux` | HTTP router |
| `github.com/mattn/go-sqlite3` | SQLite driver (CGO required) |
| `golang.org/x/crypto` | bcrypt |
| `golang.org/x/oauth2` | OAuth2 + Google/Microsoft endpoints |

---

## License

This project is licensed under the [GPL-3.0 license](LICENSE).