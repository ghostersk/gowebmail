package models

import "time"

// ---- Users ----

// UserRole controls access level within GoMail.
type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

// User represents a GoMail application user.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	IsActive     bool      `json:"is_active"`
	// MFA
	MFAEnabled  bool   `json:"mfa_enabled"`
	MFASecret   string `json:"-"` // TOTP secret, stored encrypted
	// Pending MFA setup (secret generated but not yet verified)
	MFAPending  string `json:"-"`
	// Preferences
	SyncInterval  int  `json:"sync_interval"`
	ComposePopup  bool `json:"compose_popup"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// ---- Audit Log ----

// AuditEventType categorises log events.
type AuditEventType string

const (
	AuditLogin        AuditEventType = "login"
	AuditLoginFail    AuditEventType = "login_fail"
	AuditLogout       AuditEventType = "logout"
	AuditMFASuccess   AuditEventType = "mfa_success"
	AuditMFAFail      AuditEventType = "mfa_fail"
	AuditMFAEnable    AuditEventType = "mfa_enable"
	AuditMFADisable   AuditEventType = "mfa_disable"
	AuditUserCreate   AuditEventType = "user_create"
	AuditUserDelete   AuditEventType = "user_delete"
	AuditUserUpdate   AuditEventType = "user_update"
	AuditAccountAdd   AuditEventType = "account_add"
	AuditAccountDel   AuditEventType = "account_delete"
	AuditSyncRun      AuditEventType = "sync_run"
	AuditConfigChange AuditEventType = "config_change"
	AuditAppError     AuditEventType = "app_error"
)

// AuditLog is a single audit event.
type AuditLog struct {
	ID        int64          `json:"id"`
	UserID    *int64         `json:"user_id,omitempty"`
	UserEmail string         `json:"user_email,omitempty"`
	Event     AuditEventType `json:"event"`
	Detail    string         `json:"detail,omitempty"`
	IPAddress string         `json:"ip_address,omitempty"`
	UserAgent string         `json:"user_agent,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// AuditPage is a paginated audit log result.
type AuditPage struct {
	Logs     []AuditLog `json:"logs"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	HasMore  bool       `json:"has_more"`
}

// ---- Email Accounts ----

// AccountProvider indicates the email provider type.
type AccountProvider string

const (
	ProviderGmail   AccountProvider = "gmail"
	ProviderOutlook AccountProvider = "outlook"
	ProviderIMAPSMTP AccountProvider = "imap_smtp"
)

// EmailAccount represents a connected email account (Gmail, Outlook, IMAP).
type EmailAccount struct {
	ID           int64           `json:"id"`
	UserID       int64           `json:"user_id"`
	Provider     AccountProvider `json:"provider"`
	EmailAddress string          `json:"email_address"`
	DisplayName  string          `json:"display_name"`
	// OAuth tokens (stored encrypted in DB)
	AccessToken  string    `json:"-"`
	RefreshToken string    `json:"-"`
	TokenExpiry  time.Time `json:"-"`
	// IMAP/SMTP settings (optional, stored encrypted)
	IMAPHost string `json:"imap_host,omitempty"`
	IMAPPort int    `json:"imap_port,omitempty"`
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"`
	// Sync settings
	SyncDays  int    `json:"sync_days"`  // how many days back to fetch (0 = all)
	SyncMode  string `json:"sync_mode"`  // "days" or "all"
	// SyncInterval is populated from the owning user's setting during background sync
	SyncInterval int    `json:"-"`
	LastError    string `json:"last_error,omitempty"`
	// Display
	Color     string    `json:"color"`
	IsActive  bool      `json:"is_active"`
	LastSync  time.Time `json:"last_sync"`
	CreatedAt time.Time `json:"created_at"`
}
// Folder represents a mailbox folder or Gmail label.
type Folder struct {
	ID        int64  `json:"id"`
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`      // Display name
	FullPath  string `json:"full_path"` // e.g. "INBOX", "[Gmail]/Sent Mail"
	FolderType string `json:"folder_type"` // inbox, sent, drafts, trash, spam, custom
	UnreadCount int  `json:"unread_count"`
	TotalCount  int  `json:"total_count"`
	IsHidden    bool `json:"is_hidden"`
	SyncEnabled bool `json:"sync_enabled"`
}

// ---- Messages ----

// MessageFlag represents IMAP message flags.
type MessageFlag string

const (
	FlagSeen     MessageFlag = "\\Seen"
	FlagAnswered MessageFlag = "\\Answered"
	FlagFlagged  MessageFlag = "\\Flagged"
	FlagDeleted  MessageFlag = "\\Deleted"
	FlagDraft    MessageFlag = "\\Draft"
)

// Attachment holds metadata for email attachments.
type Attachment struct {
	ID          int64  `json:"id"`
	MessageID   int64  `json:"message_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ContentID   string `json:"content_id,omitempty"` // for inline attachments
	Data        []byte `json:"-"`                    // actual bytes, loaded on demand
}

// Message represents a cached email message.
type Message struct {
	ID          int64     `json:"id"`
	AccountID   int64     `json:"account_id"`
	FolderID    int64     `json:"folder_id"`
	RemoteUID   string    `json:"remote_uid"` // UID from provider (IMAP UID or Gmail message ID)
	ThreadID    string    `json:"thread_id,omitempty"`
	MessageID   string    `json:"message_id"` // RFC 2822 Message-ID header
	// Encrypted fields (stored encrypted, decrypted on read)
	Subject     string    `json:"subject"`
	FromName    string    `json:"from_name"`
	FromEmail   string    `json:"from_email"`
	ToList      string    `json:"to"`   // comma-separated
	CCList      string    `json:"cc"`
	BCCList     string    `json:"bcc"`
	ReplyTo     string    `json:"reply_to"`
	BodyText    string    `json:"body_text,omitempty"`
	BodyHTML    string    `json:"body_html,omitempty"`
	// Metadata (not encrypted)
	Date        time.Time `json:"date"`
	IsRead      bool      `json:"is_read"`
	IsStarred   bool      `json:"is_starred"`
	IsDraft     bool      `json:"is_draft"`
	HasAttachment bool    `json:"has_attachment"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MessageSummary is a lightweight version for list views.
type MessageSummary struct {
	ID            int64     `json:"id"`
	AccountID     int64     `json:"account_id"`
	AccountEmail  string    `json:"account_email"`
	AccountColor  string    `json:"account_color"`
	FolderID      int64     `json:"folder_id"`
	FolderName    string    `json:"folder_name"`
	Subject       string    `json:"subject"`
	FromName      string    `json:"from_name"`
	FromEmail     string    `json:"from_email"`
	Preview       string    `json:"preview"` // first ~100 chars of body
	Date          time.Time `json:"date"`
	IsRead        bool      `json:"is_read"`
	IsStarred     bool      `json:"is_starred"`
	HasAttachment bool      `json:"has_attachment"`
}

// ---- Compose ----

// ComposeRequest is the payload for sending/replying/forwarding.
type ComposeRequest struct {
	AccountID   int64    `json:"account_id"`
	To          []string `json:"to"`
	CC          []string `json:"cc"`
	BCC         []string `json:"bcc"`
	Subject     string   `json:"subject"`
	BodyHTML    string   `json:"body_html"`
	BodyText    string   `json:"body_text"`
	// For reply/forward
	InReplyToID   int64  `json:"in_reply_to_id,omitempty"`
	ForwardFromID int64  `json:"forward_from_id,omitempty"`
}

// ---- Search ----

// SearchQuery parameters.
type SearchQuery struct {
	Query     string `json:"query"`
	AccountID int64  `json:"account_id"` // 0 = all accounts
	FolderID  int64  `json:"folder_id"`  // 0 = all folders
	From      string `json:"from"`
	To        string `json:"to"`
	HasAttachment bool `json:"has_attachment"`
	IsUnread  bool   `json:"is_unread"`
	IsStarred bool   `json:"is_starred"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
}

// PagedMessages is a paginated message result.
type PagedMessages struct {
	Messages []MessageSummary `json:"messages"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	HasMore  bool             `json:"has_more"`
}
