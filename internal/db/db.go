// Package db provides encrypted SQLite storage for GoMail.
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yourusername/gomail/internal/crypto"
	"github.com/yourusername/gomail/internal/models"

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
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
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
		// Default: primary folder types sync by default, others don't.
		`ALTER TABLE folders ADD COLUMN is_hidden INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE folders ADD COLUMN sync_enabled INTEGER NOT NULL DEFAULT 1`,
		// Plaintext search index column — stores decrypted subject+from+preview for LIKE search.
		`ALTER TABLE messages ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		d.sql.Exec(stmt) // ignore "duplicate column" errors intentionally
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
		       COALESCE(sync_days,30), COALESCE(sync_mode,'days')
		FROM email_accounts WHERE id=?`, accountID,
	).Scan(
		&a.ID, &a.UserID, &a.Provider, &a.EmailAddress, &a.DisplayName,
		&accessEnc, &refreshEnc, &a.TokenExpiry,
		&imapHostEnc, &a.IMAPPort, &smtpHostEnc, &a.SMTPPort,
		&a.LastError, &a.Color, &a.IsActive, &lastSync, &a.CreatedAt,
		&a.SyncDays, &a.SyncMode,
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
		       last_error, color, is_active, last_sync, created_at
		FROM email_accounts WHERE user_id=? AND is_active=1 ORDER BY created_at`, userID)
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
	f.IsHidden = isHidden == 1; f.SyncEnabled = syncEnabled == 1
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
	id, _ := res.LastInsertId()
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
	if isHidden { ih = 1 }
	if syncEnabled { se = 1 }
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
	f.IsHidden = isHidden == 1; f.SyncEnabled = syncEnabled == 1
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
