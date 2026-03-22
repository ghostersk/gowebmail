// Package db provides encrypted SQLite storage for GoWebMail.
package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/ghostersk/gowebmail/internal/crypto"
	"github.com/ghostersk/gowebmail/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps a SQLite database with field-level AES-256 encryption.
type DB struct {
	sql *sql.DB
	enc *crypto.Encryptor
}

// New opens (or creates) a SQLite database at path, using encKey for field encryption.
func New(path string, encKey []byte) (*DB, error) {
	enc, err := crypto.New(encKey)
	if err != nil {
		return nil, fmt.Errorf("encryptor init: %w", err)
	}

	// Enable WAL mode and foreign keys for performance and integrity
	// sqlite file path must start with `file:` for package mattn/go-sqlite3
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // SQLite is single-writer

	return &DB{sql: sqlDB, enc: enc}, nil
}

// Close closes the underlying database.
func (d *DB) Close() error {
	return d.sql.Close()
}

// ---- Migrations ----

// Migrate creates all required tables and bootstraps the admin account.
func (d *DB) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			email         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
			username      TEXT    NOT NULL DEFAULT '',
			password_hash TEXT    NOT NULL,
			role          TEXT    NOT NULL DEFAULT 'user',
			is_active     INTEGER NOT NULL DEFAULT 1,
			mfa_enabled   INTEGER NOT NULL DEFAULT 0,
			mfa_secret    TEXT    NOT NULL DEFAULT '',
			mfa_pending   TEXT    NOT NULL DEFAULT '',
			sync_interval INTEGER NOT NULL DEFAULT 15,
			last_login_at DATETIME,
			created_at    DATETIME DEFAULT (datetime('now')),
			updated_at    DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS email_accounts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			provider      TEXT    NOT NULL,
			email_address TEXT    NOT NULL,
			display_name  TEXT    NOT NULL DEFAULT '',
			access_token  TEXT    NOT NULL DEFAULT '',
			refresh_token TEXT    NOT NULL DEFAULT '',
			token_expiry  DATETIME,
			imap_host     TEXT    NOT NULL DEFAULT '',
			imap_port     INTEGER NOT NULL DEFAULT 0,
			smtp_host     TEXT    NOT NULL DEFAULT '',
			smtp_port     INTEGER NOT NULL DEFAULT 0,
			last_error    TEXT    NOT NULL DEFAULT '',
			color         TEXT    NOT NULL DEFAULT '#4A90D9',
			is_active     INTEGER NOT NULL DEFAULT 1,
			last_sync     DATETIME,
			created_at    DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_user ON email_accounts(user_id)`,
		`CREATE TABLE IF NOT EXISTS folders (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id   INTEGER NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
			name         TEXT    NOT NULL,
			full_path    TEXT    NOT NULL,
			folder_type  TEXT    NOT NULL DEFAULT 'custom',
			unread_count INTEGER NOT NULL DEFAULT 0,
			total_count  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_folders_account_path ON folders(account_id, full_path)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id     INTEGER NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
			folder_id      INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
			remote_uid     TEXT    NOT NULL,
			thread_id      TEXT    NOT NULL DEFAULT '',
			message_id     TEXT    NOT NULL DEFAULT '',
			subject        TEXT    NOT NULL DEFAULT '',
			from_name      TEXT    NOT NULL DEFAULT '',
			from_email     TEXT    NOT NULL DEFAULT '',
			to_list        TEXT    NOT NULL DEFAULT '',
			cc_list        TEXT    NOT NULL DEFAULT '',
			bcc_list       TEXT    NOT NULL DEFAULT '',
			reply_to       TEXT    NOT NULL DEFAULT '',
			body_text      TEXT    NOT NULL DEFAULT '',
			body_html      TEXT    NOT NULL DEFAULT '',
			date           DATETIME NOT NULL,
			is_read        INTEGER  NOT NULL DEFAULT 0,
			is_starred     INTEGER  NOT NULL DEFAULT 0,
			is_draft       INTEGER  NOT NULL DEFAULT 0,
			has_attachment INTEGER  NOT NULL DEFAULT 0,
			created_at     DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_uid ON messages(account_id, folder_id, remote_uid)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_account ON messages(account_id)`,
		`CREATE TABLE IF NOT EXISTS attachments (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			filename     TEXT    NOT NULL DEFAULT '',
			content_type TEXT    NOT NULL DEFAULT '',
			size         INTEGER NOT NULL DEFAULT 0,
			content_id   TEXT    NOT NULL DEFAULT '',
			data         BLOB
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token         TEXT    PRIMARY KEY,
			user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mfa_verified  INTEGER NOT NULL DEFAULT 0,
			expires_at    DATETIME NOT NULL,
			created_at    DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
			event      TEXT    NOT NULL,
			detail     TEXT    NOT NULL DEFAULT '',
			ip_address TEXT    NOT NULL DEFAULT '',
			user_agent TEXT    NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_event ON audit_log(event)`,
		`CREATE TABLE IF NOT EXISTS remote_content_whitelist (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			sender     TEXT    NOT NULL,
			created_at DATETIME DEFAULT (datetime('now')),
			UNIQUE(user_id, sender)
		)`,
	}

	for _, stmt := range stmts {
		if _, err := d.sql.Exec(stmt); err != nil {
			return fmt.Errorf("migration error (%s...): %w", stmt[:40], err)
		}
	}

	// Additive ALTER TABLE migrations — safe to re-run (SQLite ignores duplicate column errors)
	alterStmts := []string{
		`ALTER TABLE email_accounts ADD COLUMN sync_days INTEGER NOT NULL DEFAULT 30`,
		`ALTER TABLE email_accounts ADD COLUMN sync_mode TEXT NOT NULL DEFAULT 'days'`,
		`ALTER TABLE email_accounts ADD COLUMN sync_all_folders INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN compose_popup INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN folder_path TEXT NOT NULL DEFAULT ''`,
		// Folder visibility: is_hidden hides from sidebar; sync_enabled controls auto-sync.
		`ALTER TABLE folders ADD COLUMN is_hidden INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE folders ADD COLUMN sync_enabled INTEGER NOT NULL DEFAULT 1`,
		// Plaintext search index column — stores decrypted subject+from+preview for LIKE search.
		`ALTER TABLE messages ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`,
		// Per-folder IMAP sync state for incremental/delta sync.
		`ALTER TABLE folders ADD COLUMN uid_validity INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE folders ADD COLUMN last_seen_uid INTEGER NOT NULL DEFAULT 0`,
		// Account display order for sidebar drag-and-drop reordering.
		`ALTER TABLE email_accounts ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`,
		// UI preferences (JSON): collapsed accounts/folders, etc. Synced across devices.
		`ALTER TABLE users ADD COLUMN ui_prefs TEXT NOT NULL DEFAULT '{}'`,
	}
	for _, stmt := range alterStmts {
		d.sql.Exec(stmt) // ignore "duplicate column" errors intentionally
	}

	// Pending IMAP operations queue — survives server restarts.
	// op_type: "delete" | "move" | "flag_read" | "flag_star"
	_, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS pending_imap_ops (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id  INTEGER NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
		op_type     TEXT    NOT NULL,
		remote_uid  INTEGER NOT NULL,
		folder_path TEXT    NOT NULL DEFAULT '',
		extra       TEXT    NOT NULL DEFAULT '',
		attempts    INTEGER NOT NULL DEFAULT 0,
		created_at  DATETIME DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("create pending_imap_ops: %w", err)
	}

	// Login attempt tracking for brute-force protection.
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS login_attempts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		ip         TEXT    NOT NULL,
		username   TEXT    NOT NULL DEFAULT '',
		success    INTEGER NOT NULL DEFAULT 0,
		country    TEXT    NOT NULL DEFAULT '',
		country_code TEXT  NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create login_attempts: %w", err)
	}
	if _, err := d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_login_attempts_ip_time ON login_attempts(ip, created_at)`); err != nil {
		return fmt.Errorf("create login_attempts index: %w", err)
	}

	// IP block list — manually added or auto-created by brute force protection.
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS ip_blocks (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		ip           TEXT    NOT NULL UNIQUE,
		reason       TEXT    NOT NULL DEFAULT '',
		country      TEXT    NOT NULL DEFAULT '',
		country_code TEXT    NOT NULL DEFAULT '',
		attempts     INTEGER NOT NULL DEFAULT 0,
		blocked_at   DATETIME DEFAULT (datetime('now')),
		expires_at   DATETIME,
		is_permanent INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return fmt.Errorf("create ip_blocks: %w", err)
	}

	// Per-user IP access rules.
	// mode: "brute_skip" = skip brute force check for this user from listed IPs
	//       "allow_only" = only allow login from listed IPs (all others get 403)
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS user_ip_rules (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		mode       TEXT    NOT NULL DEFAULT 'brute_skip',
		ip_list    TEXT    NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT (datetime('now')),
		updated_at DATETIME DEFAULT (datetime('now')),
		UNIQUE(user_id)
	)`); err != nil {
		return fmt.Errorf("create user_ip_rules: %w", err)
	}

	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS contacts (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		display_name TEXT    NOT NULL DEFAULT '',
		email        TEXT    NOT NULL DEFAULT '',
		phone        TEXT    NOT NULL DEFAULT '',
		company      TEXT    NOT NULL DEFAULT '',
		notes        TEXT    NOT NULL DEFAULT '',
		avatar_color TEXT    NOT NULL DEFAULT '#6b7280',
		created_at   DATETIME DEFAULT (datetime('now')),
		updated_at   DATETIME DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create contacts: %w", err)
	}
	if _, err := d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_user ON contacts(user_id)`); err != nil {
		return fmt.Errorf("index contacts_user: %w", err)
	}

	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS calendar_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		account_id      INTEGER REFERENCES email_accounts(id) ON DELETE SET NULL,
		uid             TEXT    NOT NULL DEFAULT '',
		title           TEXT    NOT NULL DEFAULT '',
		description     TEXT    NOT NULL DEFAULT '',
		location        TEXT    NOT NULL DEFAULT '',
		start_time      DATETIME NOT NULL,
		end_time        DATETIME NOT NULL,
		all_day         INTEGER NOT NULL DEFAULT 0,
		recurrence_rule TEXT    NOT NULL DEFAULT '',
		color           TEXT    NOT NULL DEFAULT '',
		status          TEXT    NOT NULL DEFAULT 'confirmed',
		organizer_email TEXT    NOT NULL DEFAULT '',
		attendees       TEXT    NOT NULL DEFAULT '',
		ical_source     TEXT    NOT NULL DEFAULT '',
		created_at      DATETIME DEFAULT (datetime('now')),
		updated_at      DATETIME DEFAULT (datetime('now')),
		UNIQUE(user_id, uid)
	)`); err != nil {
		return fmt.Errorf("create calendar_events: %w", err)
	}
	if _, err := d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_calendar_user_time ON calendar_events(user_id, start_time)`); err != nil {
		return fmt.Errorf("index calendar_user_time: %w", err)
	}

	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS caldav_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token      TEXT    NOT NULL UNIQUE,
		label      TEXT    NOT NULL DEFAULT 'CalDAV token',
		created_at DATETIME DEFAULT (datetime('now')),
		last_used  DATETIME
	)`); err != nil {
		return fmt.Errorf("create caldav_tokens: %w", err)
	}

	// Bootstrap admin account if no users exist
	return d.bootstrapAdmin()
}

// bootstrapAdmin creates the default admin/admin account on first run.
func (d *DB) bootstrapAdmin() error {
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if count > 0 {
		return nil
	}
	hash, err := crypto.HashPassword("admin")
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(
		`INSERT INTO users (email, username, password_hash, role, is_active)
		 VALUES ('admin', 'admin', ?, 'admin', 1)`, hash,
	)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	fmt.Println("WARNING: Default admin account created: username=admin password=admin — CHANGE THIS IMMEDIATELY")
	return nil
}

// ---- Users ----

func (d *DB) CreateUser(username, email, password string, role models.UserRole) (*models.User, error) {
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, err
	}
	res, err := d.sql.Exec(
		`INSERT INTO users (email, username, password_hash, role) VALUES (?, ?, ?, ?)`,
		email, username, hash, role,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("email already registered")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetUserByID(id)
}

func (d *DB) scanUser(row *sql.Row) (*models.User, error) {
	u := &models.User{}
	var mfaSecretEnc, mfaPendingEnc string
	var lastLogin sql.NullTime
	var composePopup int
	err := row.Scan(
		&u.ID, &u.Email, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive,
		&u.MFAEnabled, &mfaSecretEnc, &mfaPendingEnc, &lastLogin,
		&u.CreatedAt, &u.UpdatedAt, &u.SyncInterval, &composePopup,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.MFASecret, _ = d.enc.Decrypt(mfaSecretEnc)
	u.MFAPending, _ = d.enc.Decrypt(mfaPendingEnc)
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	u.ComposePopup = composePopup == 1
	return u, nil
}

const userSelectCols = `SELECT id, email, username, password_hash, role, is_active,
	mfa_enabled, mfa_secret, mfa_pending, last_login_at, created_at, updated_at,
	COALESCE(sync_interval,15), COALESCE(compose_popup,0) FROM users`

func (d *DB) GetUserByEmail(email string) (*models.User, error) {
	return d.scanUser(d.sql.QueryRow(userSelectCols+` WHERE email=?`, email))
}

func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	return d.scanUser(d.sql.QueryRow(userSelectCols+` WHERE username=?`, username))
}

func (d *DB) GetUserByID(id int64) (*models.User, error) {
	return d.scanUser(d.sql.QueryRow(userSelectCols+` WHERE id=?`, id))
}

func (d *DB) ListUsers() ([]*models.User, error) {
	rows, err := d.sql.Query(userSelectCols + ` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*models.User
	for rows.Next() {
		u := &models.User{}
		var mfaSecretEnc, mfaPendingEnc string
		var lastLogin sql.NullTime
		var composePopup int
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive,
			&u.MFAEnabled, &mfaSecretEnc, &mfaPendingEnc, &lastLogin,
			&u.CreatedAt, &u.UpdatedAt, &u.SyncInterval, &composePopup,
		); err != nil {
			return nil, err
		}
		u.ComposePopup = composePopup == 1
		u.MFASecret, _ = d.enc.Decrypt(mfaSecretEnc)
		u.MFAPending, _ = d.enc.Decrypt(mfaPendingEnc)
		if lastLogin.Valid {
			u.LastLoginAt = &lastLogin.Time
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) UpdateUserPassword(userID int64, newPassword string) error {
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(
		`UPDATE users SET password_hash=?, updated_at=datetime('now') WHERE id=?`, hash, userID,
	)
	return err
}

// AdminListAdmins returns (username, email, mfa_enabled) for all admin-role users.
func (d *DB) AdminListAdmins() ([]struct {
	Username   string
	Email      string
	MFAEnabled bool
}, error) {
	rows, err := d.sql.Query(`SELECT username, email, mfa_enabled FROM users WHERE role='admin' ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Username   string
		Email      string
		MFAEnabled bool
	}
	for rows.Next() {
		var r struct {
			Username   string
			Email      string
			MFAEnabled bool
		}
		rows.Scan(&r.Username, &r.Email, &r.MFAEnabled)
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminResetPassword sets a new password for an admin user by username (admin-only check).
func (d *DB) AdminResetPassword(username, newPassword string) error {
	// Verify user exists and is admin
	var id int64
	var role string
	err := d.sql.QueryRow(`SELECT id, role FROM users WHERE username=?`, username).Scan(&id, &role)
	if err != nil || id == 0 {
		return fmt.Errorf("user %q not found", username)
	}
	if role != "admin" {
		return fmt.Errorf("user %q is not an admin (use the web UI for regular users)", username)
	}
	return d.UpdateUserPassword(id, newPassword)
}

// AdminDisableMFA disables MFA for an admin user by username (admin-only check).
func (d *DB) AdminDisableMFA(username string) error {
	var id int64
	var role string
	err := d.sql.QueryRow(`SELECT id, role FROM users WHERE username=?`, username).Scan(&id, &role)
	if err != nil || id == 0 {
		return fmt.Errorf("user %q not found", username)
	}
	if role != "admin" {
		return fmt.Errorf("user %q is not an admin (use the web UI for regular users)", username)
	}
	return d.DisableMFA(id)
}

func (d *DB) SetUserActive(userID int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := d.sql.Exec(`UPDATE users SET is_active=?, updated_at=datetime('now') WHERE id=?`, v, userID)
	return err
}

func (d *DB) DeleteUser(userID int64) error {
	_, err := d.sql.Exec(`DELETE FROM users WHERE id=? AND role != 'admin'`, userID)
	return err
}

func (d *DB) TouchLastLogin(userID int64) {
	d.sql.Exec(`UPDATE users SET last_login_at=datetime('now') WHERE id=?`, userID)
}

// ---- MFA ----

func (d *DB) SetMFAPending(userID int64, secret string) error {
	enc, err := d.enc.Encrypt(secret)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`UPDATE users SET mfa_pending=?, updated_at=datetime('now') WHERE id=?`, enc, userID)
	return err
}

func (d *DB) EnableMFA(userID int64, secret string) error {
	enc, err := d.enc.Encrypt(secret)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(
		`UPDATE users SET mfa_enabled=1, mfa_secret=?, mfa_pending='', updated_at=datetime('now') WHERE id=?`,
		enc, userID,
	)
	return err
}

func (d *DB) DisableMFA(userID int64) error {
	_, err := d.sql.Exec(
		`UPDATE users SET mfa_enabled=0, mfa_secret='', mfa_pending='', updated_at=datetime('now') WHERE id=?`,
		userID,
	)
	return err
}

// ---- Sessions ----

func (d *DB) CreateSession(userID int64, ttl time.Duration) (string, error) {
	token, err := crypto.GenerateToken(32)
	if err != nil {
		return "", err
	}
	expiry := time.Now().Add(ttl)
	_, err = d.sql.Exec(
		`INSERT INTO sessions (token, user_id, mfa_verified, expires_at) VALUES (?, ?, 0, ?)`,
		token, userID, expiry,
	)
	return token, err
}

func (d *DB) SetSessionMFAVerified(token string) error {
	_, err := d.sql.Exec(`UPDATE sessions SET mfa_verified=1 WHERE token=?`, token)
	return err
}

func (d *DB) GetSession(token string) (userID int64, mfaVerified bool, err error) {
	var expiresAt time.Time
	err = d.sql.QueryRow(
		`SELECT user_id, mfa_verified, expires_at FROM sessions WHERE token=?`, token,
	).Scan(&userID, &mfaVerified, &expiresAt)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if time.Now().After(expiresAt) {
		d.sql.Exec(`DELETE FROM sessions WHERE token=?`, token)
		return 0, false, nil
	}
	return userID, mfaVerified, nil
}

func (d *DB) DeleteSession(token string) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

// ---- Audit Log ----

func (d *DB) WriteAudit(userID *int64, event models.AuditEventType, detail, ip, ua string) {
	d.sql.Exec(
		`INSERT INTO audit_log (user_id, event, detail, ip_address, user_agent) VALUES (?,?,?,?,?)`,
		userID, string(event), detail, ip, ua,
	)
}

func (d *DB) ListAuditLogs(page, pageSize int, eventFilter string) (*models.AuditPage, error) {
	offset := (page - 1) * pageSize
	where := ""
	args := []interface{}{}
	if eventFilter != "" {
		where = " WHERE a.event=?"
		args = append(args, eventFilter)
	}

	var total int
	d.sql.QueryRow(`SELECT COUNT(*) FROM audit_log a`+where, args...).Scan(&total)

	args = append(args, pageSize, offset)
	rows, err := d.sql.Query(`
		SELECT a.id, a.user_id, COALESCE(u.email,''), a.event, a.detail, a.ip_address, a.user_agent, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id=a.user_id`+where+`
		ORDER BY a.created_at DESC LIMIT ? OFFSET ?`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.AuditLog
	for rows.Next() {
		l := models.AuditLog{}
		var uid sql.NullInt64
		if err := rows.Scan(&l.ID, &uid, &l.UserEmail, &l.Event, &l.Detail, &l.IPAddress, &l.UserAgent, &l.CreatedAt); err != nil {
			return nil, err
		}
		if uid.Valid {
			l.UserID = &uid.Int64
		}
		logs = append(logs, l)
	}
	return &models.AuditPage{
		Logs: logs, Total: total, Page: page, PageSize: pageSize,
		HasMore: offset+len(logs) < total,
	}, rows.Err()
}

// ---- Email Accounts ----

func (d *DB) CreateAccount(a *models.EmailAccount) error {
	accessEnc, _ := d.enc.Encrypt(a.AccessToken)
	refreshEnc, _ := d.enc.Encrypt(a.RefreshToken)
	imapHostEnc, _ := d.enc.Encrypt(a.IMAPHost)
	smtpHostEnc, _ := d.enc.Encrypt(a.SMTPHost)

	res, err := d.sql.Exec(`
		INSERT INTO email_accounts
			(user_id, provider, email_address, display_name, access_token, refresh_token,
			 token_expiry, imap_host, imap_port, smtp_host, smtp_port, color)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.UserID, a.Provider, a.EmailAddress, a.DisplayName,
		accessEnc, refreshEnc, a.TokenExpiry,
		imapHostEnc, a.IMAPPort, smtpHostEnc, a.SMTPPort,
		a.Color,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	a.ID = id
	return nil
}

func (d *DB) UpdateAccountTokens(accountID int64, accessToken, refreshToken string, expiry time.Time) error {
	accessEnc, _ := d.enc.Encrypt(accessToken)
	refreshEnc, _ := d.enc.Encrypt(refreshToken)
	_, err := d.sql.Exec(
		`UPDATE email_accounts SET access_token=?, refresh_token=?, token_expiry=? WHERE id=?`,
		accessEnc, refreshEnc, expiry, accountID,
	)
	return err
}

func (d *DB) UpdateAccountLastSync(accountID int64) error {
	_, err := d.sql.Exec(`UPDATE email_accounts SET last_sync=? WHERE id=?`, time.Now(), accountID)
	return err
}

func (d *DB) GetAccount(accountID int64) (*models.EmailAccount, error) {
	a := &models.EmailAccount{}
	var accessEnc, refreshEnc, imapHostEnc, smtpHostEnc string
	var lastSync sql.NullTime
	err := d.sql.QueryRow(`
		SELECT id, user_id, provider, email_address, display_name,
		       access_token, refresh_token, token_expiry,
		       imap_host, imap_port, smtp_host, smtp_port,
		       last_error, color, is_active, last_sync, created_at,
		       COALESCE(sync_days,30), COALESCE(sync_mode,'days'), COALESCE(sort_order,0)
		FROM email_accounts WHERE id=?`, accountID,
	).Scan(
		&a.ID, &a.UserID, &a.Provider, &a.EmailAddress, &a.DisplayName,
		&accessEnc, &refreshEnc, &a.TokenExpiry,
		&imapHostEnc, &a.IMAPPort, &smtpHostEnc, &a.SMTPPort,
		&a.LastError, &a.Color, &a.IsActive, &lastSync, &a.CreatedAt,
		&a.SyncDays, &a.SyncMode, &a.SortOrder,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.AccessToken, _ = d.enc.Decrypt(accessEnc)
	a.RefreshToken, _ = d.enc.Decrypt(refreshEnc)
	a.IMAPHost, _ = d.enc.Decrypt(imapHostEnc)
	a.SMTPHost, _ = d.enc.Decrypt(smtpHostEnc)
	if lastSync.Valid {
		a.LastSync = lastSync.Time
	}
	if a.SyncDays == 0 {
		a.SyncDays = 30
	}
	return a, nil
}

func (d *DB) GetUserSyncInterval(userID int64) (int, error) {
	var interval int
	err := d.sql.QueryRow(`SELECT sync_interval FROM users WHERE id=?`, userID).Scan(&interval)
	if err != nil {
		return 15, err
	}
	return interval, nil
}

func (d *DB) SetUserSyncInterval(userID int64, minutes int) error {
	_, err := d.sql.Exec(`UPDATE users SET sync_interval=? WHERE id=?`, minutes, userID)
	return err
}

func (d *DB) SetComposePopup(userID int64, popup bool) error {
	v := 0
	if popup {
		v = 1
	}
	_, err := d.sql.Exec(`UPDATE users SET compose_popup=? WHERE id=?`, v, userID)
	return err
}

func (d *DB) SetAccountSyncSettings(accountID, userID int64, syncDays int, syncMode string) error {
	if syncMode == "" {
		syncMode = "days"
	}
	_, err := d.sql.Exec(`UPDATE email_accounts SET sync_days=?, sync_mode=? WHERE id=? AND user_id=?`,
		syncDays, syncMode, accountID, userID)
	return err
}

func (d *DB) UpdateAccount(a *models.EmailAccount) error {
	accessEnc, _ := d.enc.Encrypt(a.AccessToken)
	imapHostEnc, _ := d.enc.Encrypt(a.IMAPHost)
	smtpHostEnc, _ := d.enc.Encrypt(a.SMTPHost)
	syncMode := a.SyncMode
	if syncMode == "" {
		syncMode = "days"
	}
	syncDays := a.SyncDays
	if syncDays == 0 {
		syncDays = 30
	}
	_, err := d.sql.Exec(`
		UPDATE email_accounts SET
			display_name=?, access_token=?,
			imap_host=?, imap_port=?, smtp_host=?, smtp_port=?,
			color=?, sync_days=?, sync_mode=?
		WHERE id=? AND user_id=?`,
		a.DisplayName, accessEnc,
		imapHostEnc, a.IMAPPort, smtpHostEnc, a.SMTPPort,
		a.Color, syncDays, syncMode, a.ID, a.UserID,
	)
	return err
}

func (d *DB) SetAccountError(accountID int64, errMsg string) {
	d.sql.Exec(`UPDATE email_accounts SET last_error=? WHERE id=?`, errMsg, accountID)
}

func (d *DB) ClearAccountError(accountID int64) {
	d.sql.Exec(`UPDATE email_accounts SET last_error='' WHERE id=?`, accountID)
}

// ListAllActiveAccounts returns all active accounts joined with their user's sync_interval.
func (d *DB) ListAllActiveAccounts() ([]*models.EmailAccount, error) {
	rows, err := d.sql.Query(`
		SELECT a.id, a.user_id, a.provider, a.email_address, a.display_name,
		       a.access_token, a.refresh_token, a.token_expiry,
		       a.imap_host, a.imap_port, a.smtp_host, a.smtp_port,
		       a.last_error, a.color, a.is_active, a.last_sync, a.created_at,
		       u.sync_interval
		FROM email_accounts a
		JOIN users u ON u.id = a.user_id
		WHERE a.is_active=1 AND u.is_active=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*models.EmailAccount
	for rows.Next() {
		a := &models.EmailAccount{}
		var accessEnc, refreshEnc, imapHostEnc, smtpHostEnc string
		var lastSync sql.NullTime
		if err := rows.Scan(
			&a.ID, &a.UserID, &a.Provider, &a.EmailAddress, &a.DisplayName,
			&accessEnc, &refreshEnc, &a.TokenExpiry,
			&imapHostEnc, &a.IMAPPort, &smtpHostEnc, &a.SMTPPort,
			&a.LastError, &a.Color, &a.IsActive, &lastSync, &a.CreatedAt,
			&a.SyncInterval,
		); err != nil {
			return nil, err
		}
		a.AccessToken, _ = d.enc.Decrypt(accessEnc)
		a.RefreshToken, _ = d.enc.Decrypt(refreshEnc)
		a.IMAPHost, _ = d.enc.Decrypt(imapHostEnc)
		a.SMTPHost, _ = d.enc.Decrypt(smtpHostEnc)
		if lastSync.Valid {
			a.LastSync = lastSync.Time
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (d *DB) ListAccountsByUser(userID int64) ([]*models.EmailAccount, error) {
	rows, err := d.sql.Query(`
		SELECT id, user_id, provider, email_address, display_name,
		       access_token, refresh_token, token_expiry,
		       imap_host, imap_port, smtp_host, smtp_port,
		       last_error, color, is_active, last_sync, created_at,
		       COALESCE(sort_order,0)
		FROM email_accounts WHERE user_id=? AND is_active=1
		ORDER BY COALESCE(sort_order,0), created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return d.scanAccounts(rows)
}

func (d *DB) scanAccounts(rows *sql.Rows) ([]*models.EmailAccount, error) {
	var accounts []*models.EmailAccount
	for rows.Next() {
		a := &models.EmailAccount{}
		var accessEnc, refreshEnc, imapHostEnc, smtpHostEnc string
		var lastSync sql.NullTime
		if err := rows.Scan(
			&a.ID, &a.UserID, &a.Provider, &a.EmailAddress, &a.DisplayName,
			&accessEnc, &refreshEnc, &a.TokenExpiry,
			&imapHostEnc, &a.IMAPPort, &smtpHostEnc, &a.SMTPPort,
			&a.LastError, &a.Color, &a.IsActive, &lastSync, &a.CreatedAt,
			&a.SortOrder,
		); err != nil {
			return nil, err
		}
		a.AccessToken, _ = d.enc.Decrypt(accessEnc)
		a.RefreshToken, _ = d.enc.Decrypt(refreshEnc)
		a.IMAPHost, _ = d.enc.Decrypt(imapHostEnc)
		a.SMTPHost, _ = d.enc.Decrypt(smtpHostEnc)
		if lastSync.Valid {
			a.LastSync = lastSync.Time
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (d *DB) DeleteAccount(accountID, userID int64) error {
	_, err := d.sql.Exec(
		`DELETE FROM email_accounts WHERE id=? AND user_id=?`, accountID, userID,
	)
	return err
}

// UpsertOAuthAccount inserts a new OAuth account or updates tokens/display name
// if an account with the same (user_id, provider, email_address) already exists.
// Used by OAuth callbacks so that re-connecting updates rather than duplicates.
func (d *DB) UpsertOAuthAccount(a *models.EmailAccount) (created bool, err error) {
	accessEnc, _ := d.enc.Encrypt(a.AccessToken)
	refreshEnc, _ := d.enc.Encrypt(a.RefreshToken)

	// Check for existing account with same user + provider + email
	var existingID int64
	row := d.sql.QueryRow(
		`SELECT id FROM email_accounts WHERE user_id=? AND provider=? AND email_address=?`,
		a.UserID, a.Provider, a.EmailAddress,
	)
	scanErr := row.Scan(&existingID)

	if scanErr == sql.ErrNoRows {
		// New account — insert with next sort_order
		var maxOrder int
		d.sql.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM email_accounts WHERE user_id=?`, a.UserID).Scan(&maxOrder)
		res, insertErr := d.sql.Exec(`
			INSERT INTO email_accounts
				(user_id, provider, email_address, display_name, access_token, refresh_token,
				 token_expiry, imap_host, imap_port, smtp_host, smtp_port, color, sort_order)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.UserID, a.Provider, a.EmailAddress, a.DisplayName,
			accessEnc, refreshEnc, a.TokenExpiry,
			"", a.IMAPPort, "", a.SMTPPort,
			a.Color, maxOrder+1,
		)
		if insertErr != nil {
			return false, insertErr
		}
		id, _ := res.LastInsertId()
		a.ID = id
		return true, nil
	}
	if scanErr != nil {
		return false, scanErr
	}

	// Existing account — update tokens and display name only.
	// If refresh token is empty (Microsoft omits it after first auth),
	// keep the existing one to avoid losing the ability to auto-refresh.
	if a.RefreshToken != "" {
		_, err = d.sql.Exec(`
			UPDATE email_accounts SET
				display_name=?, access_token=?, refresh_token=?, token_expiry=?, last_error=''
			WHERE id=?`,
			a.DisplayName, accessEnc, refreshEnc, a.TokenExpiry, existingID,
		)
	} else {
		_, err = d.sql.Exec(`
			UPDATE email_accounts SET
				display_name=?, access_token=?, token_expiry=?, last_error=''
			WHERE id=?`,
			a.DisplayName, accessEnc, a.TokenExpiry, existingID,
		)
	}
	a.ID = existingID
	return false, err
}

// UpdateAccountSortOrder sets sort_order for a batch of accounts for a user.
// accountIDs is ordered from first to last in the desired display order.
func (d *DB) UpdateAccountSortOrder(userID int64, accountIDs []int64) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	for i, id := range accountIDs {
		if _, err := tx.Exec(
			`UPDATE email_accounts SET sort_order=? WHERE id=? AND user_id=?`,
			i, id, userID,
		); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// GetUIPrefs returns the JSON ui_prefs string for a user.
func (d *DB) GetUIPrefs(userID int64) (string, error) {
	var prefs string
	err := d.sql.QueryRow(`SELECT COALESCE(ui_prefs,'{}') FROM users WHERE id=?`, userID).Scan(&prefs)
	if err != nil {
		return "{}", err
	}
	return prefs, nil
}

// SetUIPrefs stores the JSON ui_prefs string for a user.
func (d *DB) SetUIPrefs(userID int64, prefs string) error {
	_, err := d.sql.Exec(`UPDATE users SET ui_prefs=? WHERE id=?`, prefs, userID)
	return err
}

// UpdateFolderCountsDirect sets folder counts directly (used by Graph sync where
// the server provides accurate counts without needing a local recount).
func (d *DB) UpdateFolderCountsDirect(folderID int64, total, unread int) {
	d.sql.Exec(`UPDATE folders SET total_count=?, unread_count=? WHERE id=?`,
		total, unread, folderID)
}

// UpdateFolderCounts refreshes the unread/total counts for a folder.
func (d *DB) UpdateFolderCounts(folderID int64) {
	d.sql.Exec(`
		UPDATE folders SET
			total_count  = (SELECT COUNT(*) FROM messages WHERE folder_id=?),
			unread_count = (SELECT COUNT(*) FROM messages WHERE folder_id=? AND is_read=0)
		WHERE id=?`, folderID, folderID, folderID)
}

// ---- Folders ----

func (d *DB) UpsertFolder(f *models.Folder) error {
	// On insert: set sync_enabled based on folder type (primary types sync by default)
	defaultSync := 0
	switch f.FolderType {
	case "inbox", "sent", "drafts", "trash", "spam":
		defaultSync = 1
	}
	_, err := d.sql.Exec(`
		INSERT INTO folders (account_id, name, full_path, folder_type, unread_count, total_count, sync_enabled)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(account_id, full_path) DO UPDATE SET
			name=excluded.name,
			folder_type=excluded.folder_type,
			unread_count=excluded.unread_count,
			total_count=excluded.total_count`,
		f.AccountID, f.Name, f.FullPath, f.FolderType, f.UnreadCount, f.TotalCount, defaultSync,
	)
	return err
}

func (d *DB) GetFolderByPath(accountID int64, fullPath string) (*models.Folder, error) {
	f := &models.Folder{}
	var isHidden, syncEnabled int
	err := d.sql.QueryRow(
		`SELECT id, account_id, name, full_path, folder_type, unread_count, total_count,
		       COALESCE(is_hidden,0), COALESCE(sync_enabled,1)
		 FROM folders WHERE account_id=? AND full_path=?`, accountID, fullPath,
	).Scan(&f.ID, &f.AccountID, &f.Name, &f.FullPath, &f.FolderType, &f.UnreadCount, &f.TotalCount, &isHidden, &syncEnabled)
	f.IsHidden = isHidden == 1
	f.SyncEnabled = syncEnabled == 1
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return f, err
}

func (d *DB) ListFoldersByAccount(accountID int64) ([]*models.Folder, error) {
	rows, err := d.sql.Query(
		`SELECT id, account_id, name, full_path, folder_type, unread_count, total_count,
		       COALESCE(is_hidden,0), COALESCE(sync_enabled,1)
		 FROM folders WHERE account_id=? ORDER BY folder_type, name`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []*models.Folder
	for rows.Next() {
		f := &models.Folder{}
		var isHidden, syncEnabled int
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.FullPath, &f.FolderType, &f.UnreadCount, &f.TotalCount, &isHidden, &syncEnabled); err != nil {
			return nil, err
		}
		f.IsHidden = isHidden == 1
		f.SyncEnabled = syncEnabled == 1
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// ---- Messages ----

func (d *DB) UpsertMessage(m *models.Message) error {
	subjectEnc, _ := d.enc.Encrypt(m.Subject)
	fromNameEnc, _ := d.enc.Encrypt(m.FromName)
	fromEmailEnc, _ := d.enc.Encrypt(m.FromEmail)
	toEnc, _ := d.enc.Encrypt(m.ToList)
	ccEnc, _ := d.enc.Encrypt(m.CCList)
	bccEnc, _ := d.enc.Encrypt(m.BCCList)
	replyToEnc, _ := d.enc.Encrypt(m.ReplyTo)
	bodyTextEnc, _ := d.enc.Encrypt(m.BodyText)
	bodyHTMLEnc, _ := d.enc.Encrypt(m.BodyHTML)

	// Build plaintext search index: subject + from name + from email + first 200 chars of body
	preview := m.BodyText
	if len(preview) > 200 {
		preview = preview[:200]
	}
	searchText := strings.ToLower(m.Subject + " " + m.FromName + " " + m.FromEmail + " " + preview)

	res, err := d.sql.Exec(`
		INSERT INTO messages
			(account_id, folder_id, remote_uid, thread_id, message_id,
			 subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to,
			 body_text, body_html, date, is_read, is_starred, is_draft, has_attachment, search_text)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id, folder_id, remote_uid) DO UPDATE SET
			is_read=excluded.is_read,
			is_starred=excluded.is_starred,
			has_attachment=excluded.has_attachment,
			search_text=excluded.search_text`,
		m.AccountID, m.FolderID, m.RemoteUID, m.ThreadID, m.MessageID,
		subjectEnc, fromNameEnc, fromEmailEnc, toEnc, ccEnc, bccEnc, replyToEnc,
		bodyTextEnc, bodyHTMLEnc, m.Date,
		m.IsRead, m.IsStarred, m.IsDraft, m.HasAttachment, searchText,
	)
	if err != nil {
		return err
	}
	// LastInsertId returns 0 on conflict in SQLite — always look up the real ID.
	id, _ := res.LastInsertId()
	if id == 0 {
		d.sql.QueryRow(
			`SELECT id FROM messages WHERE account_id=? AND folder_id=? AND remote_uid=?`,
			m.AccountID, m.FolderID, m.RemoteUID,
		).Scan(&id)
	}
	if m.ID == 0 {
		m.ID = id
	}
	return nil
}

func (d *DB) GetMessage(messageID, userID int64) (*models.Message, error) {
	m := &models.Message{}
	var subjectEnc, fromNameEnc, fromEmailEnc, toEnc, ccEnc, bccEnc, replyToEnc, bodyTextEnc, bodyHTMLEnc string

	err := d.sql.QueryRow(`
		SELECT m.id, m.account_id, m.folder_id, m.remote_uid, m.thread_id, m.message_id,
		       m.subject, m.from_name, m.from_email, m.to_list, m.cc_list, m.bcc_list,
		       m.reply_to, m.body_text, m.body_html,
		       m.date, m.is_read, m.is_starred, m.is_draft, m.has_attachment, m.created_at
		FROM messages m
		JOIN email_accounts a ON a.id = m.account_id
		WHERE m.id=? AND a.user_id=?`, messageID, userID,
	).Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.RemoteUID, &m.ThreadID, &m.MessageID,
		&subjectEnc, &fromNameEnc, &fromEmailEnc, &toEnc, &ccEnc, &bccEnc,
		&replyToEnc, &bodyTextEnc, &bodyHTMLEnc,
		&m.Date, &m.IsRead, &m.IsStarred, &m.IsDraft, &m.HasAttachment, &m.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	m.Subject, _ = d.enc.Decrypt(subjectEnc)
	m.FromName, _ = d.enc.Decrypt(fromNameEnc)
	m.FromEmail, _ = d.enc.Decrypt(fromEmailEnc)
	m.ToList, _ = d.enc.Decrypt(toEnc)
	m.CCList, _ = d.enc.Decrypt(ccEnc)
	m.BCCList, _ = d.enc.Decrypt(bccEnc)
	m.ReplyTo, _ = d.enc.Decrypt(replyToEnc)
	m.BodyText, _ = d.enc.Decrypt(bodyTextEnc)
	m.BodyHTML, _ = d.enc.Decrypt(bodyHTMLEnc)

	// Load attachment metadata
	if m.HasAttachment {
		atts, _ := d.GetAttachmentsByMessage(m.ID, userID)
		m.Attachments = atts
	}

	return m, nil
}

func (d *DB) ListMessages(userID int64, folderIDs []int64, accountID int64, page, pageSize int) (*models.PagedMessages, error) {
	offset := (page - 1) * pageSize
	args := []interface{}{userID}

	where := "a.user_id=?"
	if accountID > 0 {
		where += " AND m.account_id=?"
		args = append(args, accountID)
	}
	if len(folderIDs) > 0 {
		placeholders := make([]string, len(folderIDs))
		for i, fid := range folderIDs {
			placeholders[i] = "?"
			args = append(args, fid)
		}
		where += " AND m.folder_id IN (" + strings.Join(placeholders, ",") + ")"
	}

	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)

	var total int
	d.sql.QueryRow("SELECT COUNT(*) FROM messages m JOIN email_accounts a ON a.id=m.account_id WHERE "+where, countArgs...).Scan(&total)

	args = append(args, pageSize, offset)
	rows, err := d.sql.Query(`
		SELECT m.id, m.account_id, a.email_address, a.color, m.folder_id, f.name,
		       m.subject, m.from_name, m.from_email, m.body_text,
		       m.date, m.is_read, m.is_starred, m.has_attachment
		FROM messages m
		JOIN email_accounts a ON a.id = m.account_id
		JOIN folders f ON f.id = m.folder_id
		WHERE `+where+`
		ORDER BY m.date DESC
		LIMIT ? OFFSET ?`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []models.MessageSummary
	for rows.Next() {
		s := models.MessageSummary{}
		var subjectEnc, fromNameEnc, fromEmailEnc, bodyTextEnc string
		if err := rows.Scan(
			&s.ID, &s.AccountID, &s.AccountEmail, &s.AccountColor, &s.FolderID, &s.FolderName,
			&subjectEnc, &fromNameEnc, &fromEmailEnc, &bodyTextEnc,
			&s.Date, &s.IsRead, &s.IsStarred, &s.HasAttachment,
		); err != nil {
			return nil, err
		}
		s.Subject, _ = d.enc.Decrypt(subjectEnc)
		s.FromName, _ = d.enc.Decrypt(fromNameEnc)
		s.FromEmail, _ = d.enc.Decrypt(fromEmailEnc)
		bodyText, _ := d.enc.Decrypt(bodyTextEnc)
		if len(bodyText) > 120 {
			bodyText = bodyText[:120] + "…"
		}
		s.Preview = bodyText
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &models.PagedMessages{
		Messages: summaries,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasMore:  offset+len(summaries) < total,
	}, nil
}

func (d *DB) SearchMessages(userID int64, q string, page, pageSize int) (*models.PagedMessages, error) {
	offset := (page - 1) * pageSize
	like := "%" + strings.ToLower(q) + "%"
	args := []interface{}{userID, like, pageSize, offset}

	var total int
	d.sql.QueryRow(`
		SELECT COUNT(*) FROM messages m
		JOIN email_accounts a ON a.id=m.account_id
		WHERE a.user_id=? AND m.search_text LIKE ?`,
		userID, like,
	).Scan(&total)

	rows, err := d.sql.Query(`
		SELECT m.id, m.account_id, a.email_address, a.color, m.folder_id, f.name,
		       m.subject, m.from_name, m.from_email, m.body_text,
		       m.date, m.is_read, m.is_starred, m.has_attachment
		FROM messages m
		JOIN email_accounts a ON a.id=m.account_id
		JOIN folders f ON f.id=m.folder_id
		WHERE a.user_id=? AND m.search_text LIKE ?
		ORDER BY m.date DESC LIMIT ? OFFSET ?`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []models.MessageSummary
	for rows.Next() {
		s := models.MessageSummary{}
		var subjectEnc, fromNameEnc, fromEmailEnc, bodyTextEnc string
		if err := rows.Scan(
			&s.ID, &s.AccountID, &s.AccountEmail, &s.AccountColor, &s.FolderID, &s.FolderName,
			&subjectEnc, &fromNameEnc, &fromEmailEnc, &bodyTextEnc,
			&s.Date, &s.IsRead, &s.IsStarred, &s.HasAttachment,
		); err != nil {
			return nil, err
		}
		s.Subject, _ = d.enc.Decrypt(subjectEnc)
		s.FromName, _ = d.enc.Decrypt(fromNameEnc)
		s.FromEmail, _ = d.enc.Decrypt(fromEmailEnc)
		bodyText, _ := d.enc.Decrypt(bodyTextEnc)
		if len(bodyText) > 120 {
			bodyText = bodyText[:120] + "…"
		}
		s.Preview = bodyText
		summaries = append(summaries, s)
	}

	return &models.PagedMessages{
		Messages: summaries, Total: total, Page: page, PageSize: pageSize,
		HasMore: offset+len(summaries) < total,
	}, rows.Err()
}

func (d *DB) MarkMessageRead(messageID, userID int64, read bool) error {
	val := 0
	if read {
		val = 1
	}
	_, err := d.sql.Exec(`
		UPDATE messages SET is_read=?
		WHERE id=? AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		val, messageID, userID,
	)
	return err
}

// UpdateMessageBody persists body text/html for a message (used by Graph lazy fetch).
func (d *DB) UpdateMessageBody(messageID int64, bodyText, bodyHTML string) {
	bodyTextEnc, _ := d.enc.Encrypt(bodyText)
	bodyHTMLEnc, _ := d.enc.Encrypt(bodyHTML)
	d.sql.Exec(`UPDATE messages SET body_text=?, body_html=? WHERE id=?`,
		bodyTextEnc, bodyHTMLEnc, messageID)
}

// GetNewestMessageDate returns the date of the most recent message in a folder.
// Returns zero time if the folder is empty.
func (d *DB) GetNewestMessageDate(folderID int64) time.Time {
	var t time.Time
	d.sql.QueryRow(`SELECT MAX(date) FROM messages WHERE folder_id=?`, folderID).Scan(&t)
	return t
}

func (d *DB) ToggleMessageStar(messageID, userID int64) (bool, error) {
	var current bool
	err := d.sql.QueryRow(`
		SELECT is_starred FROM messages
		WHERE id=? AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		messageID, userID,
	).Scan(&current)
	if err != nil {
		return false, err
	}
	newVal := !current
	intVal := 0
	if newVal {
		intVal = 1
	}
	_, err = d.sql.Exec(`UPDATE messages SET is_starred=? WHERE id=?`, intVal, messageID)
	return newVal, err
}

func (d *DB) MoveMessage(messageID, userID, folderID int64) error {
	_, err := d.sql.Exec(`
		UPDATE messages SET folder_id=?
		WHERE id=? AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		folderID, messageID, userID,
	)
	return err
}

func (d *DB) DeleteMessage(messageID, userID int64) error {
	_, err := d.sql.Exec(`
		DELETE FROM messages WHERE id=?
		AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		messageID, userID,
	)
	return err
}

func (d *DB) GetFoldersByUser(userID int64) ([]*models.Folder, error) {
	rows, err := d.sql.Query(`
		SELECT f.id, f.account_id, f.name, f.full_path, f.folder_type, f.unread_count, f.total_count,
		       COALESCE(f.is_hidden,0), COALESCE(f.sync_enabled,1)
		FROM folders f
		JOIN email_accounts a ON a.id=f.account_id
		WHERE a.user_id=?
		ORDER BY a.created_at, f.folder_type, f.name`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []*models.Folder
	for rows.Next() {
		f := &models.Folder{}
		var isHidden, syncEnabled int
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.FullPath, &f.FolderType, &f.UnreadCount, &f.TotalCount, &isHidden, &syncEnabled); err != nil {
			return nil, err
		}
		f.IsHidden = isHidden == 1
		f.SyncEnabled = syncEnabled == 1
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// ---- Remote Content Whitelist ----

func (d *DB) GetRemoteContentWhitelist(userID int64) ([]string, error) {
	rows, err := d.sql.Query(
		`SELECT sender FROM remote_content_whitelist WHERE user_id=? ORDER BY sender`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			list = append(list, s)
		}
	}
	return list, rows.Err()
}

func (d *DB) AddRemoteContentWhitelist(userID int64, sender string) error {
	_, err := d.sql.Exec(
		`INSERT OR IGNORE INTO remote_content_whitelist (user_id, sender) VALUES (?, ?)`,
		userID, sender,
	)
	return err
}

func (d *DB) IsRemoteContentAllowed(userID int64, sender string) (bool, error) {
	var count int
	err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM remote_content_whitelist WHERE user_id=? AND sender=?`,
		userID, sender,
	).Scan(&count)
	return count > 0, err
}

// SetFolderVisibility sets is_hidden and sync_enabled for a folder owned by the user.
func (d *DB) SetFolderVisibility(folderID, userID int64, isHidden, syncEnabled bool) error {
	ih, se := 0, 0
	if isHidden {
		ih = 1
	}
	if syncEnabled {
		se = 1
	}
	_, err := d.sql.Exec(`
		UPDATE folders SET is_hidden=?, sync_enabled=?
		WHERE id=? AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		ih, se, folderID, userID)
	return err
}

// CountFolderMessages returns how many messages are in a folder (owned by user).
func (d *DB) CountFolderMessages(folderID, userID int64) (int, error) {
	var count int
	err := d.sql.QueryRow(`
		SELECT COUNT(*) FROM messages m
		JOIN folders f ON f.id=m.folder_id
		JOIN email_accounts a ON a.id=f.account_id
		WHERE m.folder_id=? AND a.user_id=?`, folderID, userID).Scan(&count)
	return count, err
}

// DeleteFolder removes a folder and all its messages (cascade).
func (d *DB) DeleteFolder(folderID, userID int64) error {
	_, err := d.sql.Exec(`
		DELETE FROM folders WHERE id=?
		AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		folderID, userID)
	return err
}

// MoveFolderContents moves all messages from one folder to another (both must belong to user).
func (d *DB) MoveFolderContents(fromID, toID, userID int64) (int64, error) {
	res, err := d.sql.Exec(`
		UPDATE messages SET folder_id=?
		WHERE folder_id=?
		AND folder_id IN (SELECT f.id FROM folders f JOIN email_accounts a ON a.id=f.account_id WHERE a.user_id=?)`,
		toID, fromID, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (d *DB) GetFolderByID(folderID int64) (*models.Folder, error) {
	f := &models.Folder{}
	var isHidden, syncEnabled int
	err := d.sql.QueryRow(
		`SELECT id, account_id, name, full_path, folder_type, unread_count, total_count,
		       COALESCE(is_hidden,0), COALESCE(sync_enabled,1)
		 FROM folders WHERE id=?`, folderID,
	).Scan(&f.ID, &f.AccountID, &f.Name, &f.FullPath, &f.FolderType, &f.UnreadCount, &f.TotalCount, &isHidden, &syncEnabled)
	f.IsHidden = isHidden == 1
	f.SyncEnabled = syncEnabled == 1
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return f, err
}

// GetMessageIMAPInfo returns the remote_uid, folder full_path, account info needed for IMAP ops.
func (d *DB) GetMessageIMAPInfo(messageID, userID int64) (remoteUID uint32, folderPath string, account *models.EmailAccount, err error) {
	var uidStr string
	var accountID int64
	var folderID int64
	err = d.sql.QueryRow(`
		SELECT m.remote_uid, m.account_id, m.folder_id
		FROM messages m
		JOIN email_accounts a ON a.id = m.account_id
		WHERE m.id=? AND a.user_id=?`, messageID, userID,
	).Scan(&uidStr, &accountID, &folderID)
	if err != nil {
		return 0, "", nil, err
	}
	// Parse uid
	var uid uint64
	fmt.Sscanf(uidStr, "%d", &uid)
	remoteUID = uint32(uid)

	folder, err := d.GetFolderByID(folderID)
	if err != nil || folder == nil {
		return remoteUID, "", nil, fmt.Errorf("folder not found")
	}
	account, err = d.GetAccount(accountID)
	return remoteUID, folder.FullPath, account, err
}

// GetMessageGraphInfo returns the Graph message ID (remote_uid as string), folder ID string,
// and account for a Graph-backed message. Used by handlers for outlook_personal accounts.
func (d *DB) GetMessageGraphInfo(messageID, userID int64) (graphMsgID string, folderGraphID string, account *models.EmailAccount, err error) {
	var accountID int64
	var folderID int64
	err = d.sql.QueryRow(`
		SELECT m.remote_uid, m.account_id, m.folder_id
		FROM messages m
		JOIN email_accounts a ON a.id = m.account_id
		WHERE m.id=? AND a.user_id=?`, messageID, userID,
	).Scan(&graphMsgID, &accountID, &folderID)
	if err != nil {
		return "", "", nil, err
	}
	folder, err := d.GetFolderByID(folderID)
	if err != nil || folder == nil {
		return graphMsgID, "", nil, fmt.Errorf("folder not found")
	}
	account, err = d.GetAccount(accountID)
	return graphMsgID, folder.FullPath, account, err
}

// ListStarredMessages returns all starred messages for a user, newest first.
func (d *DB) ListStarredMessages(userID int64, page, pageSize int) (*models.PagedMessages, error) {
	offset := (page - 1) * pageSize
	var total int
	d.sql.QueryRow(`SELECT COUNT(*) FROM messages m JOIN email_accounts a ON a.id=m.account_id WHERE a.user_id=? AND m.is_starred=1`, userID).Scan(&total)

	rows, err := d.sql.Query(`
		SELECT m.id, m.account_id, a.email_address, a.color, m.folder_id, f.name,
		       m.subject, m.from_name, m.from_email, m.body_text,
		       m.date, m.is_read, m.is_starred, m.has_attachment
		FROM messages m
		JOIN email_accounts a ON a.id = m.account_id
		JOIN folders f ON f.id = m.folder_id
		WHERE a.user_id=? AND m.is_starred=1
		ORDER BY m.date DESC
		LIMIT ? OFFSET ?`, userID, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []models.MessageSummary
	for rows.Next() {
		s := models.MessageSummary{}
		var subjectEnc, fromNameEnc, fromEmailEnc, bodyTextEnc string
		if err := rows.Scan(
			&s.ID, &s.AccountID, &s.AccountEmail, &s.AccountColor, &s.FolderID, &s.FolderName,
			&subjectEnc, &fromNameEnc, &fromEmailEnc, &bodyTextEnc,
			&s.Date, &s.IsRead, &s.IsStarred, &s.HasAttachment,
		); err != nil {
			return nil, err
		}
		s.Subject, _ = d.enc.Decrypt(subjectEnc)
		s.FromName, _ = d.enc.Decrypt(fromNameEnc)
		s.FromEmail, _ = d.enc.Decrypt(fromEmailEnc)
		bodyText, _ := d.enc.Decrypt(bodyTextEnc)
		if len(bodyText) > 120 {
			bodyText = bodyText[:120] + "…"
		}
		s.Preview = bodyText
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &models.PagedMessages{
		Messages: summaries,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasMore:  offset+len(summaries) < total,
	}, nil
}

// ---- Pending IMAP ops queue ----

// PendingIMAPOp represents an IMAP write operation that needs to be applied to the server.
type PendingIMAPOp struct {
	ID         int64
	AccountID  int64
	OpType     string // "delete" | "move" | "flag_read" | "flag_star"
	RemoteUID  uint32
	FolderPath string
	Extra      string // for move: dest folder path; for flag_*: "1" or "0"
	Attempts   int
}

// EnqueueIMAPOp adds an operation to the pending queue atomically.
func (d *DB) EnqueueIMAPOp(op *PendingIMAPOp) error {
	_, err := d.sql.Exec(
		`INSERT INTO pending_imap_ops (account_id, op_type, remote_uid, folder_path, extra) VALUES (?,?,?,?,?)`,
		op.AccountID, op.OpType, op.RemoteUID, op.FolderPath, op.Extra,
	)
	return err
}

// DequeuePendingOps returns up to `limit` pending ops for a given account.
func (d *DB) DequeuePendingOps(accountID int64, limit int) ([]*PendingIMAPOp, error) {
	rows, err := d.sql.Query(
		`SELECT id, account_id, op_type, remote_uid, folder_path, extra, attempts
		 FROM pending_imap_ops WHERE account_id=? ORDER BY id ASC LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []*PendingIMAPOp
	for rows.Next() {
		op := &PendingIMAPOp{}
		rows.Scan(&op.ID, &op.AccountID, &op.OpType, &op.RemoteUID, &op.FolderPath, &op.Extra, &op.Attempts)
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// DeletePendingOp removes a successfully applied op.
func (d *DB) DeletePendingOp(id int64) error {
	_, err := d.sql.Exec(`DELETE FROM pending_imap_ops WHERE id=?`, id)
	return err
}

// IncrementPendingOpAttempts bumps attempt count; ops with >5 attempts are abandoned.
func (d *DB) IncrementPendingOpAttempts(id int64) {
	d.sql.Exec(`UPDATE pending_imap_ops SET attempts=attempts+1 WHERE id=?`, id)
	d.sql.Exec(`DELETE FROM pending_imap_ops WHERE id=? AND attempts>5`, id)
}

// CountPendingOps returns number of queued ops for an account (for logging).
func (d *DB) CountPendingOps(accountID int64) int {
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM pending_imap_ops WHERE account_id=?`, accountID).Scan(&n)
	return n
}

// ---- Folder delta-sync state ----

// GetFolderSyncState returns uid_validity and last_seen_uid for incremental sync.
func (d *DB) GetFolderSyncState(folderID int64) (uidValidity, lastSeenUID uint32) {
	d.sql.QueryRow(`SELECT COALESCE(uid_validity,0), COALESCE(last_seen_uid,0) FROM folders WHERE id=?`, folderID).
		Scan(&uidValidity, &lastSeenUID)
	return
}

// SetFolderSyncState persists uid_validity and last_seen_uid after a successful sync.
func (d *DB) SetFolderSyncState(folderID int64, uidValidity, lastSeenUID uint32) {
	d.sql.Exec(`UPDATE folders SET uid_validity=?, last_seen_uid=? WHERE id=?`, uidValidity, lastSeenUID, folderID)
}

// PurgeDeletedMessages removes local messages whose remote_uid is no longer
// in the server's UID list for a folder. Returns count purged.
func (d *DB) PurgeDeletedMessages(folderID int64, serverUIDs []uint32) (int, error) {
	if len(serverUIDs) == 0 {
		// Don't purge everything if server returned empty (connection issue)
		return 0, nil
	}
	// Build placeholder list
	args := make([]interface{}, len(serverUIDs)+1)
	args[0] = folderID
	placeholders := make([]string, len(serverUIDs))
	for i, uid := range serverUIDs {
		args[i+1] = fmt.Sprintf("%d", uid)
		placeholders[i] = "?"
	}
	q := fmt.Sprintf(
		`DELETE FROM messages WHERE folder_id=? AND remote_uid NOT IN (%s)`,
		strings.Join(placeholders, ","),
	)
	res, err := d.sql.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteAllFolderMessages removes all messages from a folder (used on UIDVALIDITY change).
func (d *DB) DeleteAllFolderMessages(folderID int64) {
	d.sql.Exec(`DELETE FROM messages WHERE folder_id=?`, folderID)
}

// GetFolderMessageCount returns the local message count for a folder by account and path.
func (d *DB) GetFolderMessageCount(accountID int64, folderPath string) int {
	var n int
	d.sql.QueryRow(`
		SELECT COUNT(*) FROM messages m
		JOIN folders f ON f.id=m.folder_id
		WHERE f.account_id=? AND f.full_path=?`, accountID, folderPath,
	).Scan(&n)
	return n
}

// ReconcileFlags updates is_read and is_starred from server flags, but ONLY for
// messages that do NOT have a pending local write op (to avoid overwriting in-flight changes).
func (d *DB) ReconcileFlags(folderID int64, serverFlags map[uint32][]string) {
	// Get set of UIDs with pending ops so we don't overwrite them
	rows, _ := d.sql.Query(
		`SELECT DISTINCT remote_uid FROM pending_imap_ops po
		 JOIN folders f ON f.account_id=po.account_id
		 WHERE f.id=? AND (po.op_type='flag_read' OR po.op_type='flag_star')`, folderID,
	)
	pendingUIDs := make(map[uint32]bool)
	if rows != nil {
		for rows.Next() {
			var uid uint32
			rows.Scan(&uid)
			pendingUIDs[uid] = true
		}
		rows.Close()
	}

	for uid, flags := range serverFlags {
		if pendingUIDs[uid] {
			continue // don't reconcile — we have a pending write for this message
		}
		isRead := false
		isStarred := false
		for _, f := range flags {
			switch f {
			case `\Seen`:
				isRead = true
			case `\Flagged`:
				isStarred = true
			}
		}
		d.sql.Exec(
			`UPDATE messages SET is_read=?, is_starred=?
			 WHERE folder_id=? AND remote_uid=?`,
			boolToInt(isRead), boolToInt(isStarred),
			folderID, fmt.Sprintf("%d", uid),
		)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// EmptyFolder deletes all messages in a folder (Trash/Spam).
// Returns count deleted.
func (d *DB) EmptyFolder(folderID, userID int64) (int, error) {
	res, err := d.sql.Exec(`
		DELETE FROM messages WHERE folder_id=?
		AND folder_id IN (SELECT id FROM folders WHERE account_id IN
			(SELECT id FROM email_accounts WHERE user_id=?))`,
		folderID, userID,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// EnableAllFolderSync enables sync for all currently-disabled folders belonging
// to accounts owned by userID. Returns count updated.
func (d *DB) EnableAllFolderSync(accountID, userID int64) (int, error) {
	res, err := d.sql.Exec(`
		UPDATE folders SET sync_enabled=1
		WHERE account_id=? AND sync_enabled=0
		AND account_id IN (SELECT id FROM email_accounts WHERE user_id=?)`,
		accountID, userID,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PollUnread returns inbox unread count + total unread, and whether there are
// new messages since `sinceID`. Used by the client-side poller.
func (d *DB) PollUnread(userID int64, sinceID int64) (inboxUnread int, totalUnread int, newestID int64, err error) {
	// Inbox unread count
	d.sql.QueryRow(`
		SELECT COALESCE(SUM(f.unread_count),0) FROM folders f
		JOIN email_accounts a ON a.id=f.account_id
		WHERE a.user_id=? AND f.folder_type='inbox'`, userID,
	).Scan(&inboxUnread)

	// Total unread (all folders except trash/spam)
	d.sql.QueryRow(`
		SELECT COALESCE(SUM(f.unread_count),0) FROM folders f
		JOIN email_accounts a ON a.id=f.account_id
		WHERE a.user_id=? AND f.folder_type NOT IN ('trash','spam')`, userID,
	).Scan(&totalUnread)

	// Newest message ID in inbox
	d.sql.QueryRow(`
		SELECT COALESCE(MAX(m.id),0) FROM messages m
		JOIN folders f ON f.id=m.folder_id
		JOIN email_accounts a ON a.id=f.account_id
		WHERE a.user_id=? AND f.folder_type='inbox'`, userID,
	).Scan(&newestID)

	return
}

// GetNewMessagesSince returns inbox message summaries with id > sinceID for notifications.
func (d *DB) GetNewMessagesSince(userID int64, sinceID int64) ([]map[string]interface{}, error) {
	rows, err := d.sql.Query(`
		SELECT m.id, m.subject, m.from_name, m.from_email
		FROM messages m
		JOIN folders f ON f.id=m.folder_id
		JOIN email_accounts a ON a.id=f.account_id
		WHERE a.user_id=? AND f.folder_type='inbox' AND m.id>?
		ORDER BY m.id DESC LIMIT 5`, userID, sinceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]interface{}
	for rows.Next() {
		var id int64
		var subject, fromName, fromEmail string
		rows.Scan(&id, &subject, &fromName, &fromEmail)
		// Decrypt
		subject, _ = d.enc.Decrypt(subject)
		fromName, _ = d.enc.Decrypt(fromName)
		fromEmail, _ = d.enc.Decrypt(fromEmail)
		result = append(result, map[string]interface{}{
			"id": id, "subject": subject, "from_name": fromName, "from_email": fromEmail,
		})
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	return result, rows.Err()
}

// ---- Attachment metadata ----

// SaveAttachmentMeta saves attachment metadata for a message (no binary data).
// Uses INSERT OR REPLACE so a re-sync always refreshes the part path (ContentID).
func (d *DB) SaveAttachmentMeta(messageID int64, atts []models.Attachment) error {
	// Delete stale rows first so re-syncs don't leave orphans
	d.sql.Exec(`DELETE FROM attachments WHERE message_id=?`, messageID)
	for _, a := range atts {
		_, err := d.sql.Exec(`
			INSERT INTO attachments (message_id, filename, content_type, size, content_id)
			VALUES (?,?,?,?,?)`,
			messageID, a.Filename, a.ContentType, a.Size, a.ContentID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetAttachmentsByMessage returns attachment metadata for a message.
func (d *DB) GetAttachmentsByMessage(messageID, userID int64) ([]models.Attachment, error) {
	rows, err := d.sql.Query(`
		SELECT a.id, a.message_id, a.filename, a.content_type, a.size, a.content_id
		FROM attachments a
		JOIN messages m ON m.id=a.message_id
		JOIN email_accounts ac ON ac.id=m.account_id
		WHERE a.message_id=? AND ac.user_id=?`, messageID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []models.Attachment
	for rows.Next() {
		var a models.Attachment
		rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID)
		result = append(result, a)
	}
	return result, rows.Err()
}

// GetAttachment returns a single attachment record (ownership via userID check).
func (d *DB) GetAttachment(attachmentID, userID int64) (*models.Attachment, error) {
	var a models.Attachment
	err := d.sql.QueryRow(`
		SELECT a.id, a.message_id, a.filename, a.content_type, a.size, a.content_id
		FROM attachments a
		JOIN messages m ON m.id=a.message_id
		JOIN email_accounts ac ON ac.id=m.account_id
		WHERE a.id=? AND ac.user_id=?`, attachmentID, userID,
	).Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

// ---- Mark all read ----

// MarkFolderAllRead marks every message in a folder as read and enqueues IMAP flag ops.
// Returns the list of (remoteUID, folderPath, accountID) for IMAP ops.
func (d *DB) MarkFolderAllRead(folderID, userID int64) ([]PendingIMAPOp, error) {
	// Verify folder ownership
	var accountID int64
	var fullPath string
	err := d.sql.QueryRow(`
		SELECT f.account_id, f.full_path FROM folders f
		JOIN email_accounts a ON a.id=f.account_id
		WHERE f.id=? AND a.user_id=?`, folderID, userID,
	).Scan(&accountID, &fullPath)
	if err != nil {
		return nil, fmt.Errorf("folder not found or not owned: %w", err)
	}

	// Get all unread messages in folder for IMAP ops
	rows, err := d.sql.Query(`
		SELECT remote_uid FROM messages WHERE folder_id=? AND is_read=0`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []PendingIMAPOp
	for rows.Next() {
		var uid string
		rows.Scan(&uid)
		var uidNum uint32
		fmt.Sscanf(uid, "%d", &uidNum)
		if uidNum > 0 {
			ops = append(ops, PendingIMAPOp{
				AccountID: accountID, OpType: "flag_read",
				RemoteUID: uidNum, FolderPath: fullPath, Extra: "1",
			})
		}
	}
	rows.Close()

	// Bulk mark read in DB
	_, err = d.sql.Exec(`UPDATE messages SET is_read=1 WHERE folder_id=?`, folderID)
	if err != nil {
		return nil, err
	}
	d.UpdateFolderCounts(folderID)
	return ops, nil
}

// ---- Admin MFA disable ----

// AdminDisableMFAByID disables MFA for a user by ID (admin action).
func (d *DB) AdminDisableMFAByID(targetUserID int64) error {
	_, err := d.sql.Exec(`
		UPDATE users SET mfa_enabled=0, mfa_secret='', mfa_pending=''
		WHERE id=?`, targetUserID)
	return err
}

// ---- Brute Force / IP Block ----

// IPBlock represents a blocked IP entry.
type IPBlock struct {
	ID          int64     `json:"id"`
	IP          string    `json:"ip"`
	Reason      string    `json:"reason"`
	Country     string    `json:"country"`
	CountryCode string    `json:"country_code"`
	Attempts    int       `json:"attempts"`
	BlockedAt   time.Time `json:"blocked_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	IsPermanent bool      `json:"is_permanent"`
}

// LoginAttemptStat is used for summary display.
type LoginAttemptStat struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	Total       int    `json:"total"`
	Failures    int    `json:"failures"`
	LastSeen    string `json:"last_seen"`
}

// RecordLoginAttempt saves a login attempt for an IP.
func (d *DB) RecordLoginAttempt(ip, username, country, countryCode string, success bool) {
	suc := 0
	if success {
		suc = 1
	}
	d.sql.Exec(`INSERT INTO login_attempts (ip, username, success, country, country_code) VALUES (?,?,?,?,?)`,
		ip, username, suc, country, countryCode)
}

// CountRecentFailures returns the number of failed logins from an IP in the last windowMinutes.
func (d *DB) CountRecentFailures(ip string, windowMinutes int) int {
	var count int
	d.sql.QueryRow(`
		SELECT COUNT(*) FROM login_attempts
		WHERE ip=? AND success=0 AND created_at >= datetime('now', ? || ' minutes')`,
		ip, fmt.Sprintf("-%d", windowMinutes),
	).Scan(&count)
	return count
}

// IsIPBlocked returns true if the IP is currently blocked (non-expired entry).
func (d *DB) IsIPBlocked(ip string) bool {
	var count int
	d.sql.QueryRow(`
		SELECT COUNT(*) FROM ip_blocks
		WHERE ip=? AND (is_permanent=1 OR expires_at IS NULL OR expires_at > datetime('now'))`,
		ip,
	).Scan(&count)
	return count > 0
}

// BlockIP adds or updates a block entry for an IP.
// banHours=0 means permanent block (admin must remove manually).
func (d *DB) BlockIP(ip, reason, country, countryCode string, attempts int, banHours int) {
	isPermanent := 0
	var expiresExpr string
	if banHours == 0 {
		isPermanent = 1
		expiresExpr = "NULL"
	} else {
		expiresExpr = fmt.Sprintf("datetime('now', '+%d hours')", banHours)
	}
	d.sql.Exec(fmt.Sprintf(`
		INSERT INTO ip_blocks (ip, reason, country, country_code, attempts, is_permanent, expires_at)
		VALUES (?,?,?,?,?,%d,%s)
		ON CONFLICT(ip) DO UPDATE SET
			reason=excluded.reason, attempts=excluded.attempts,
			blocked_at=datetime('now'), is_permanent=%d, expires_at=%s`,
		isPermanent, expiresExpr, isPermanent, expiresExpr,
	), ip, reason, country, countryCode, attempts)
}

// UnblockIP removes a block entry.
func (d *DB) UnblockIP(ip string) error {
	_, err := d.sql.Exec(`DELETE FROM ip_blocks WHERE ip=?`, ip)
	return err
}

// ListIPBlocks returns all current (non-expired or permanent) blocked IPs.
func (d *DB) ListIPBlocks() ([]IPBlock, error) {
	rows, err := d.sql.Query(`
		SELECT id, ip, reason, country, country_code, attempts, blocked_at, expires_at, is_permanent
		FROM ip_blocks
		WHERE is_permanent=1 OR expires_at IS NULL OR expires_at > datetime('now')
		ORDER BY blocked_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []IPBlock
	for rows.Next() {
		var b IPBlock
		var expiresAt sql.NullTime
		rows.Scan(&b.ID, &b.IP, &b.Reason, &b.Country, &b.CountryCode,
			&b.Attempts, &b.BlockedAt, &expiresAt, &b.IsPermanent)
		if expiresAt.Valid {
			t := expiresAt.Time
			b.ExpiresAt = &t
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// ListLoginAttemptStats returns per-IP attempt summaries for display.
func (d *DB) ListLoginAttemptStats(limitHours int) ([]LoginAttemptStat, error) {
	rows, err := d.sql.Query(`
		SELECT ip, country, country_code,
		       COUNT(*) as total,
		       SUM(CASE WHEN success=0 THEN 1 ELSE 0 END) as failures,
		       MAX(created_at) as last_seen
		FROM login_attempts
		WHERE created_at >= datetime('now', ? || ' hours')
		GROUP BY ip ORDER BY failures DESC LIMIT 100`,
		fmt.Sprintf("-%d", limitHours),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []LoginAttemptStat
	for rows.Next() {
		var s LoginAttemptStat
		rows.Scan(&s.IP, &s.Country, &s.CountryCode, &s.Total, &s.Failures, &s.LastSeen)
		result = append(result, s)
	}
	return result, rows.Err()
}

// PurgeExpiredBlocks removes expired (non-permanent) blocks from the table.
func (d *DB) PurgeExpiredBlocks() {
	d.sql.Exec(`DELETE FROM ip_blocks WHERE is_permanent=0 AND expires_at IS NOT NULL AND expires_at <= datetime('now')`)
}

// LookupIPCountry returns cached country info for an IP from recent login_attempts.
func (d *DB) LookupCachedCountry(ip string) (country, countryCode string) {
	d.sql.QueryRow(`
		SELECT country, country_code FROM login_attempts
		WHERE ip=? AND country != '' ORDER BY created_at DESC LIMIT 1`, ip,
	).Scan(&country, &countryCode)
	return
}

// ---- Profile Updates ----

// UpdateUserEmail changes a user's email address. Returns error if already taken.
func (d *DB) UpdateUserEmail(userID int64, newEmail string) error {
	_, err := d.sql.Exec(
		`UPDATE users SET email=?, updated_at=datetime('now') WHERE id=?`,
		newEmail, userID)
	return err
}

// UpdateUserUsername changes a user's display username. Returns error if already taken.
func (d *DB) UpdateUserUsername(userID int64, newUsername string) error {
	_, err := d.sql.Exec(
		`UPDATE users SET username=?, updated_at=datetime('now') WHERE id=?`,
		newUsername, userID)
	return err
}

// ---- Per-User IP Rules ----

// UserIPRule holds per-user IP access settings.
type UserIPRule struct {
	UserID int64  `json:"user_id"`
	Mode   string `json:"mode"`    // "brute_skip" | "allow_only" | "disabled"
	IPList string `json:"ip_list"` // comma-separated IPs
}

// GetUserIPRule returns the IP rule for a user, or nil if none set.
func (d *DB) GetUserIPRule(userID int64) (*UserIPRule, error) {
	row := d.sql.QueryRow(`SELECT user_id, mode, ip_list FROM user_ip_rules WHERE user_id=?`, userID)
	r := &UserIPRule{}
	if err := row.Scan(&r.UserID, &r.Mode, &r.IPList); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return r, nil
}

// SetUserIPRule upserts the IP rule for a user.
func (d *DB) SetUserIPRule(userID int64, mode, ipList string) error {
	_, err := d.sql.Exec(`
		INSERT INTO user_ip_rules (user_id, mode, ip_list, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(user_id) DO UPDATE SET
			mode=excluded.mode,
			ip_list=excluded.ip_list,
			updated_at=datetime('now')`,
		userID, mode, ipList)
	return err
}

// DeleteUserIPRule removes IP rules for a user (disables the feature).
func (d *DB) DeleteUserIPRule(userID int64) error {
	_, err := d.sql.Exec(`DELETE FROM user_ip_rules WHERE user_id=?`, userID)
	return err
}

// CheckUserIPAccess evaluates per-user IP rules against a connecting IP.
// Returns:
//   "allow"      — rule says allow (brute_skip match or allow_only match)
//   "deny"       — allow_only mode and IP is not in list
//   "skip_brute" — brute_skip mode and IP is in list (skip brute force check)
//   "default"    — no rule exists, fall through to global rules
func (d *DB) CheckUserIPAccess(userID int64, ip string) string {
	rule, err := d.GetUserIPRule(userID)
	if err != nil || rule == nil || rule.Mode == "disabled" || rule.IPList == "" {
		return "default"
	}
	for _, listed := range splitIPs(rule.IPList) {
		if listed == ip {
			if rule.Mode == "allow_only" {
				return "allow"
			}
			return "skip_brute"
		}
	}
	// IP not in list
	if rule.Mode == "allow_only" {
		return "deny"
	}
	return "default"
}

// SplitIPList splits a comma-separated IP string into trimmed, non-empty entries.
func SplitIPList(s string) []string {
	return splitIPs(s)
}

func splitIPs(s string) []string {
	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// IPBlockWithUsername extends IPBlock with the last username attempted from that IP.
type IPBlockWithUsername struct {
	IPBlock
	LastUsername string
}

// ListIPBlocksWithUsername returns active blocks enriched with the most recent
// username that was attempted from each IP (from login_attempts history).
func (d *DB) ListIPBlocksWithUsername() ([]IPBlockWithUsername, error) {
	rows, err := d.sql.Query(`
		SELECT
			b.id, b.ip, b.reason, b.country, b.country_code,
			b.attempts, b.blocked_at, b.expires_at, b.is_permanent,
			COALESCE(
				(SELECT username FROM login_attempts
				 WHERE ip=b.ip AND username != ''
				 ORDER BY created_at DESC LIMIT 1),
				''
			) AS last_username
		FROM ip_blocks b
		WHERE b.is_permanent=1 OR b.expires_at IS NULL OR b.expires_at > datetime('now')
		ORDER BY b.blocked_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []IPBlockWithUsername
	for rows.Next() {
		var b IPBlockWithUsername
		var expiresAt sql.NullTime
		err := rows.Scan(
			&b.ID, &b.IP, &b.Reason, &b.Country, &b.CountryCode,
			&b.Attempts, &b.BlockedAt, &expiresAt, &b.IsPermanent,
			&b.LastUsername,
		)
		if err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			b.ExpiresAt = &t
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// ======== Contacts ========

func (d *DB) ListContacts(userID int64) ([]*models.Contact, error) {
	rows, err := d.sql.Query(`
		SELECT id, user_id, display_name, email, phone, company, notes, avatar_color, created_at, updated_at
		FROM contacts WHERE user_id=? ORDER BY display_name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Contact
	for rows.Next() {
		var c models.Contact
		var dn, em, ph, co, no, av []byte
		rows.Scan(&c.ID, &c.UserID, &dn, &em, &ph, &co, &no, &av, &c.CreatedAt, &c.UpdatedAt)
		c.DisplayName, _ = d.enc.Decrypt(string(dn))
		c.Email, _ = d.enc.Decrypt(string(em))
		c.Phone, _ = d.enc.Decrypt(string(ph))
		c.Company, _ = d.enc.Decrypt(string(co))
		c.Notes, _ = d.enc.Decrypt(string(no))
		c.AvatarColor, _ = d.enc.Decrypt(string(av))
		out = append(out, &c)
	}
	return out, nil
}

func (d *DB) GetContact(id, userID int64) (*models.Contact, error) {
	var c models.Contact
	var dn, em, ph, co, no, av []byte
	err := d.sql.QueryRow(`
		SELECT id, user_id, display_name, email, phone, company, notes, avatar_color, created_at, updated_at
		FROM contacts WHERE id=? AND user_id=?`, id, userID).
		Scan(&c.ID, &c.UserID, &dn, &em, &ph, &co, &no, &av, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.DisplayName, _ = d.enc.Decrypt(string(dn))
	c.Email, _ = d.enc.Decrypt(string(em))
	c.Phone, _ = d.enc.Decrypt(string(ph))
	c.Company, _ = d.enc.Decrypt(string(co))
	c.Notes, _ = d.enc.Decrypt(string(no))
	c.AvatarColor, _ = d.enc.Decrypt(string(av))
	return &c, nil
}

func (d *DB) CreateContact(c *models.Contact) error {
	dn, _ := d.enc.Encrypt(c.DisplayName)
	em, _ := d.enc.Encrypt(c.Email)
	ph, _ := d.enc.Encrypt(c.Phone)
	co, _ := d.enc.Encrypt(c.Company)
	no, _ := d.enc.Encrypt(c.Notes)
	av, _ := d.enc.Encrypt(c.AvatarColor)
	res, err := d.sql.Exec(`
		INSERT INTO contacts (user_id, display_name, email, phone, company, notes, avatar_color)
		VALUES (?,?,?,?,?,?,?)`, c.UserID, dn, em, ph, co, no, av)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

func (d *DB) UpdateContact(c *models.Contact, userID int64) error {
	dn, _ := d.enc.Encrypt(c.DisplayName)
	em, _ := d.enc.Encrypt(c.Email)
	ph, _ := d.enc.Encrypt(c.Phone)
	co, _ := d.enc.Encrypt(c.Company)
	no, _ := d.enc.Encrypt(c.Notes)
	av, _ := d.enc.Encrypt(c.AvatarColor)
	_, err := d.sql.Exec(`
		UPDATE contacts SET display_name=?, email=?, phone=?, company=?, notes=?, avatar_color=?,
		updated_at=datetime('now') WHERE id=? AND user_id=?`,
		dn, em, ph, co, no, av, c.ID, userID)
	return err
}

func (d *DB) DeleteContact(id, userID int64) error {
	_, err := d.sql.Exec(`DELETE FROM contacts WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (d *DB) SearchContacts(userID int64, q string) ([]*models.Contact, error) {
	all, err := d.ListContacts(userID)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(q)
	var out []*models.Contact
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.DisplayName), q) ||
			strings.Contains(strings.ToLower(c.Email), q) ||
			strings.Contains(strings.ToLower(c.Company), q) {
			out = append(out, c)
		}
	}
	return out, nil
}

// ======== Calendar Events ========

func (d *DB) ListCalendarEvents(userID int64, from, to string) ([]*models.CalendarEvent, error) {
	rows, err := d.sql.Query(`
		SELECT e.id, e.user_id, e.account_id, e.uid, e.title, e.description, e.location,
		       e.start_time, e.end_time, e.all_day, e.recurrence_rule, e.color,
		       e.status, e.organizer_email, e.attendees,
		       COALESCE(a.color,''), COALESCE(a.email_address,'')
		FROM calendar_events e
		LEFT JOIN email_accounts a ON a.id = e.account_id
		WHERE e.user_id=? AND e.start_time >= ? AND e.start_time <= ?
		ORDER BY e.start_time`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCalendarEvents(d, rows)
}

func (d *DB) GetCalendarEvent(id, userID int64) (*models.CalendarEvent, error) {
	rows, err := d.sql.Query(`
		SELECT e.id, e.user_id, e.account_id, e.uid, e.title, e.description, e.location,
		       e.start_time, e.end_time, e.all_day, e.recurrence_rule, e.color,
		       e.status, e.organizer_email, e.attendees,
		       COALESCE(a.color,''), COALESCE(a.email_address,'')
		FROM calendar_events e
		LEFT JOIN email_accounts a ON a.id = e.account_id
		WHERE e.id=? AND e.user_id=?`, id, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evs, err := scanCalendarEvents(d, rows)
	if err != nil || len(evs) == 0 {
		return nil, err
	}
	return evs[0], nil
}

func scanCalendarEvents(d *DB, rows interface{ Next() bool; Scan(...interface{}) error }) ([]*models.CalendarEvent, error) {
	var out []*models.CalendarEvent
	for rows.Next() {
		var e models.CalendarEvent
		var accountID *int64
		var ti, de, lo, rc, co, st, oe, at []byte
		err := rows.Scan(
			&e.ID, &e.UserID, &accountID, &e.UID,
			&ti, &de, &lo,
			&e.StartTime, &e.EndTime, &e.AllDay, &rc, &co,
			&st, &oe, &at,
			&e.AccountColor, &e.AccountEmail,
		)
		if err != nil {
			return nil, err
		}
		e.AccountID = accountID
		e.Title, _ = d.enc.Decrypt(string(ti))
		e.Description, _ = d.enc.Decrypt(string(de))
		e.Location, _ = d.enc.Decrypt(string(lo))
		e.RecurrenceRule, _ = d.enc.Decrypt(string(rc))
		e.Color, _ = d.enc.Decrypt(string(co))
		e.Status, _ = d.enc.Decrypt(string(st))
		e.OrganizerEmail, _ = d.enc.Decrypt(string(oe))
		e.Attendees, _ = d.enc.Decrypt(string(at))
		if e.Color == "" && e.AccountColor != "" {
			e.Color = e.AccountColor
		}
		out = append(out, &e)
	}
	return out, nil
}

func (d *DB) UpsertCalendarEvent(e *models.CalendarEvent) error {
	ti, _ := d.enc.Encrypt(e.Title)
	de, _ := d.enc.Encrypt(e.Description)
	lo, _ := d.enc.Encrypt(e.Location)
	rc, _ := d.enc.Encrypt(e.RecurrenceRule)
	co, _ := d.enc.Encrypt(e.Color)
	st, _ := d.enc.Encrypt(e.Status)
	oe, _ := d.enc.Encrypt(e.OrganizerEmail)
	at, _ := d.enc.Encrypt(e.Attendees)
	allDay := 0
	if e.AllDay {
		allDay = 1
	}
	if e.UID == "" {
		e.UID = fmt.Sprintf("gwm-%d-%d", e.UserID, time.Now().UnixNano())
	}
	res, err := d.sql.Exec(`
		INSERT INTO calendar_events
			(user_id, account_id, uid, title, description, location,
			 start_time, end_time, all_day, recurrence_rule, color,
			 status, organizer_email, attendees)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id, uid) DO UPDATE SET
			title=excluded.title, description=excluded.description,
			location=excluded.location, start_time=excluded.start_time,
			end_time=excluded.end_time, all_day=excluded.all_day,
			recurrence_rule=excluded.recurrence_rule, color=excluded.color,
			status=excluded.status, organizer_email=excluded.organizer_email,
			attendees=excluded.attendees,
			updated_at=datetime('now')`,
		e.UserID, e.AccountID, e.UID, ti, de, lo,
		e.StartTime, e.EndTime, allDay, rc, co, st, oe, at)
	if err != nil {
		return err
	}
	if e.ID == 0 {
		e.ID, _ = res.LastInsertId()
	}
	return nil
}

func (d *DB) DeleteCalendarEvent(id, userID int64) error {
	_, err := d.sql.Exec(`DELETE FROM calendar_events WHERE id=? AND user_id=?`, id, userID)
	return err
}

// ======== CalDAV Tokens ========

func (d *DB) CreateCalDAVToken(userID int64, label string) (*models.CalDAVToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.URLEncoding.EncodeToString(raw)
	_, err := d.sql.Exec(`INSERT INTO caldav_tokens (user_id, token, label) VALUES (?,?,?)`,
		userID, token, label)
	if err != nil {
		return nil, err
	}
	return &models.CalDAVToken{UserID: userID, Token: token, Label: label}, nil
}

func (d *DB) ListCalDAVTokens(userID int64) ([]*models.CalDAVToken, error) {
	rows, err := d.sql.Query(`
		SELECT id, user_id, token, label, created_at, COALESCE(last_used,'')
		FROM caldav_tokens WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CalDAVToken
	for rows.Next() {
		var t models.CalDAVToken
		rows.Scan(&t.ID, &t.UserID, &t.Token, &t.Label, &t.CreatedAt, &t.LastUsed)
		out = append(out, &t)
	}
	return out, nil
}

func (d *DB) DeleteCalDAVToken(id, userID int64) error {
	_, err := d.sql.Exec(`DELETE FROM caldav_tokens WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (d *DB) GetUserByCalDAVToken(token string) (int64, error) {
	var userID int64
	err := d.sql.QueryRow(`SELECT user_id FROM caldav_tokens WHERE token=?`, token).Scan(&userID)
	if err != nil {
		return 0, err
	}
	d.sql.Exec(`UPDATE caldav_tokens SET last_used=datetime('now') WHERE token=?`, token)
	return userID, nil
}
