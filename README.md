# GoMail

A self-hosted, encrypted web email client written entirely in Go. Supports Gmail and Outlook via OAuth2, plus any standard IMAP/SMTP provider.

# Notes:
- work still in progress ( gmail and hotmail email not tested yet, just prepared the app for it)
- AI is involved in making this work, as I do not have the skill and time to do it on my own
- looking for any advice and suggestions to improve it!

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
<img width="1213" height="848" alt="image" src="https://github.com/user-attachments/assets/955eda04-e358-4779-80e7-0a9b299ac110" />
<img width="1261" height="921" alt="image" src="https://github.com/user-attachments/assets/40ee58e8-6c4b-45c3-974d-98cc8ccc45a5" />
<img width="1153" height="907" alt="image" src="https://github.com/user-attachments/assets/ebc92335-f6b7-46ed-b9a2-84512f70e1b2" />
<img width="551" height="669" alt="image" src="https://github.com/user-attachments/assets/412585c0-434a-4177-ab04-7db69da9d08a" />

## Quick Start

### Option 1: Build executable

```bash
# 1. Clone / copy the project
git clone https://github.com/ghostersk/gowebmail && cd gowebmail
go build -o gowebmail ./cmd/server
./gowebmail
```

Visit http://localhost:8080, default login admin/admin, register an account, then connect your email.

### Option 2: Run directly

```bash
git clone https://github.com/ghostersk/gowebmail && cd gowebmail
go run ./cmd/server/main.go
# check ./data/gomail.conf what gets generated on first run if not exists, update as needed.
# then restart the app
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
This project is licensed under the [GPL-3.0 license](LICENSE).