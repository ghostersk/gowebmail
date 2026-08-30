package models

import "time"

// ---- Users ----

// UserRole controls access level within GoWebMail.
type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

// User represents a GoWebMail application user.
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
	ProviderGmail          AccountProvider = "gmail"
	ProviderOutlook        AccountProvider = "outlook"
	ProviderOutlookPersonal AccountProvider = "outlook_personal" // personal outlook.com via Graph API
	ProviderIMAPSMTP       AccountProvider = "imap_smtp"
	ProviderJMAP           AccountProvider = "jmap" // generic JMAP (RFC 8620/8621) server
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
	// IMAP/SMTP settings (optional, stored encrypted).
	// For ProviderJMAP accounts, IMAPHost holds the JMAP server base URL
	// (e.g. "https://mail.example.com:8443") and AccessToken holds the app
	// password — IMAPPort/SMTPHost/SMTPPort are unused for that provider.
	IMAPHost string `json:"imap_host,omitempty"`
	IMAPPort int    `json:"imap_port,omitempty"`
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"`
	// CalDAV/CardDAV sync — optional, works alongside any provider above.
	// Blank = disabled. Uses EmailAddress + AccessToken for HTTP basic auth.
	CalDAVURL  string `json:"caldav_url,omitempty"`
	CardDAVURL string `json:"carddav_url,omitempty"`
	// Sync settings
	SyncDays  int    `json:"sync_days"`  // how many days back to fetch (0 = all)
	SyncMode  string `json:"sync_mode"`  // "days" or "all"
	// SyncInterval is populated from the owning user's setting during background sync
	SyncInterval int    `json:"-"`
	LastError    string `json:"last_error,omitempty"`
	// Display
	Color     string    `json:"color"`
	IsActive  bool      `json:"is_active"`
	SortOrder int       `json:"sort_order"`
	LastSync  time.Time `json:"last_sync"`
	CreatedAt time.Time `json:"created_at"`
}
// Label is a user-defined organizational tag, local to gowebmail (not synced to the mail
// provider — labels don't have a reliable cross-provider equivalent: Gmail's are IMAP-
// extension-specific, Outlook's Categories need the Graph API, plain IMAP has none).
type Label struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Color  string `json:"color"` // hex, e.g. "#5b8def"
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
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Labels      []Label   `json:"labels,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MessageSummary is a lightweight version for list views.
type MessageSummary struct {
	ID            int64      `json:"id"`
	AccountID     int64      `json:"account_id"`
	AccountEmail  string     `json:"account_email"`
	AccountName   string     `json:"account_name"` // account's own display_name (may be blank)
	AccountColor  string     `json:"account_color"`
	FolderID      int64      `json:"folder_id"`
	FolderName    string     `json:"folder_name"`
	Subject       string     `json:"subject"`
	FromName      string     `json:"from_name"`
	FromEmail     string     `json:"from_email"`
	ToList        string     `json:"to_list"` // comma-separated; only shown in the Sent folder view
	Preview       string     `json:"preview"` // first ~100 chars of body
	Date          time.Time  `json:"date"`
	IsRead        bool       `json:"is_read"`
	IsStarred     bool       `json:"is_starred"`
	HasAttachment bool       `json:"has_attachment"`
	SnoozedUntil  *time.Time `json:"snoozed_until,omitempty"`
	Size          int64      `json:"size,omitempty"` // approximate; only populated by search results
	Labels        []Label    `json:"labels,omitempty"`
}

// ScheduledSend is a fully-composed message held until SendAt, delivered by the background
// sweep via the same send path as an immediate send. No raw file attachments in v1 — only
// forwarded-message .eml attachments (ForwardFromIDs).
type ScheduledSend struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	AccountID      int64     `json:"account_id"`
	To             []string  `json:"to"`
	CC             []string  `json:"cc,omitempty"`
	BCC            []string  `json:"bcc,omitempty"`
	Subject        string    `json:"subject"`
	BodyHTML       string    `json:"body_html"`
	BodyText       string    `json:"body_text"`
	ForwardFromIDs []int64   `json:"forward_from_ids,omitempty"`
	SendAt         time.Time `json:"send_at"`
	CreatedAt      time.Time `json:"created_at"`
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
	InReplyToID int64 `json:"in_reply_to_id,omitempty"`
	// ForwardFromIDs: each message here is fetched as a raw .eml and attached to the outgoing
	// message — independent of mode (new/reply/forward), so a user can attach one or more
	// original emails to any compose session, not just a dedicated "forward as attachment" one.
	ForwardFromIDs []int64 `json:"forward_from_ids,omitempty"`
	// Attachments: populated from multipart/form-data or inline base64
	Attachments []Attachment `json:"attachments,omitempty"`
	// DraftID identifies this compose session's previously-autosaved draft ("" if never
	// saved) — an IMAP UID, Graph message id, or JMAP email id depending on the account's
	// provider, opaque to the caller. A resave replaces that copy in place (delete-then-
	// recreate for IMAP/JMAP, PATCH for Graph) instead of piling up duplicates.
	DraftID string `json:"draft_id,omitempty"`
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

// ---- Contacts ----

type Contact struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"user_id"`
	AccountID   *int64 `json:"account_id,omitempty"` // set when synced from an account's CardDAV server
	UID         string `json:"uid,omitempty"`        // CardDAV UID, or "gwm-..." for locally-created contacts
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Company     string `json:"company"`
	Notes       string `json:"notes"`
	AvatarColor string `json:"avatar_color"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ---- Calendar ----

type CalendarEvent struct {
	ID             int64   `json:"id"`
	UserID         int64   `json:"user_id"`
	AccountID      *int64  `json:"account_id,omitempty"`
	UID            string  `json:"uid"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	Location       string  `json:"location"`
	StartTime      string  `json:"start_time"`
	EndTime        string  `json:"end_time"`
	AllDay         bool    `json:"all_day"`
	RecurrenceRule string  `json:"recurrence_rule"`
	Color          string  `json:"color"`
	Status         string  `json:"status"`
	OrganizerEmail string  `json:"organizer_email"`
	Attendees      string  `json:"attendees"`
	AccountColor   string  `json:"account_color,omitempty"`
	AccountEmail   string  `json:"account_email,omitempty"`
}

type CalDAVToken struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Token     string `json:"token"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used,omitempty"`
}

// ---- Rules (filters) ----

// RuleCondition is one field/op/value test within a Rule.
type RuleCondition struct {
	Field string `json:"field"` // from|to|subject|body|has_attachment|recipient_type
	Op    string `json:"op"`    // contains|equals|starts_with
	Value string `json:"value"`
}

// RuleActionOptions holds action-specific extra settings, stored as JSON.
type RuleActionOptions struct {
	KeepCopy bool   `json:"keep_copy,omitempty"` // forward action
	Body     string `json:"body,omitempty"`      // auto_reply action
}

// Rule is a mail filter evaluated against newly-synced messages for one account.
type Rule struct {
	ID            int64           `json:"id"`
	AccountID     int64           `json:"account_id"`
	Name          string          `json:"name"`
	Priority      int             `json:"priority"`
	Conditions    []RuleCondition `json:"conditions"`
	MatchType     string          `json:"match_type"` // all|any
	Action        string          `json:"action"`      // move_to_folder|delete|mark_read|mark_as_spam|forward|auto_reply
	ActionValue   string          `json:"action_value"`
	ActionOptions RuleActionOptions `json:"action_options"`
	IsActive      bool            `json:"is_active"`
	CreatedAt     string          `json:"created_at,omitempty"`
}

// ---- Signatures ----

type Signature struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"user_id"`
	Name        string `json:"name"`
	ContentHTML string `json:"content_html"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// SignatureDefaults maps an account to its default-for-new/default-for-reply signature.
type SignatureDefaults struct {
	AccountID       int64 `json:"account_id"`
	DefaultNewID    int64 `json:"default_new_id,omitempty"`
	DefaultReplyID  int64 `json:"default_reply_id,omitempty"`
}

// ---- S/MIME ----

type SMIMEIdentity struct {
	ID        int64     `json:"id"`
	AccountID int64     `json:"account_id"`
	CertPEM   string    `json:"cert_pem"`
	KeyPEM    string    `json:"-"` // never serialized to API responses
	NotAfter  time.Time `json:"not_after"`
	CreatedAt string    `json:"created_at,omitempty"`
}

type SMIMEContact struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Email     string `json:"email"`
	CertPEM   string `json:"cert_pem"`
	CreatedAt string `json:"created_at,omitempty"`
}

// ---- PGP ----

type PGPIdentity struct {
	ID               int64  `json:"id"`
	AccountID        int64  `json:"account_id"`
	Label            string `json:"label"`
	Email            string `json:"email"`
	Fingerprint      string `json:"fingerprint"`
	PublicKeyArmor   string `json:"public_key_armor"`
	PrivateKeyArmor  string `json:"-"` // never serialized to API responses
	CreatedAt        string `json:"created_at,omitempty"`
}

type PGPContact struct {
	ID             int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	Email          string `json:"email"`
	Label          string `json:"label"`
	Fingerprint    string `json:"fingerprint"`
	PublicKeyArmor string `json:"public_key_armor"`
	CreatedAt      string `json:"created_at,omitempty"`
}
