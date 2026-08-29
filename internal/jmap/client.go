// Package jmap is a minimal JMAP (RFC 8620 Core + RFC 8621 Mail) client for
// ProviderJMAP accounts — an alternative to IMAP/SMTP for mail servers that
// speak JMAP instead. It follows internal/graph's shape (a thin REST/JSON
// wrapper), since both are HTTP+JSON providers unlike IMAP's binary protocol.
//
// Authenticated via HTTP Basic (mailbox email + app password), matching the
// reference server this was built against — see tests/jmap-client.md.
// One HTTP call per JMAP method call: no request batching or back-references,
// since sync here isn't latency-sensitive enough to justify that complexity.
package jmap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps JMAP API calls for a single mailbox account.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client

	accountID string // resolved lazily from /jmap/session
	apiURL    string
	uploadURL string
}

// New creates a JMAP client. baseURL is the server's base URL, e.g.
// "https://mail.example.com:8443" (no trailing slash needed).
func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// Force HTTP/1.1: the reference server (tests/jmap-client.md)
			// closes the connection with no response over HTTP/2 — verified
			// live (curl negotiates h2 by default and gets a broken pipe;
			// --http1.1 works). TLSNextProto disables Go's automatic h2 ALPN
			// upgrade for HTTPS requests.
			Transport: &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}},
		},
	}
}

func (c *Client) doReq(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	url := path
	if !strings.HasPrefix(path, "http") {
		url = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("jmap %s %s returned %d: %s", method, path, resp.StatusCode, string(b))
	}
	return resp, nil
}

// Session is the RFC 8620 §2 session resource.
type Session struct {
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
	Username        string            `json:"username"`
	APIURL          string            `json:"apiUrl"`
	UploadURL       string            `json:"uploadUrl"`
	State           string            `json:"state"`
}

// Session fetches /jmap/session and resolves the mail account id + API/upload
// URLs. Also serves as a pure connectivity/auth test (used by TestConnection).
func (c *Client) Session(ctx context.Context) (*Session, error) {
	resp, err := c.doReq(ctx, http.MethodGet, "/jmap/session", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode jmap session: %w", err)
	}
	c.accountID = s.PrimaryAccounts["urn:ietf:params:jmap:mail"]
	if c.accountID == "" {
		return nil, fmt.Errorf("jmap session: no mail account found")
	}
	c.apiURL = s.APIURL
	c.uploadURL = strings.ReplaceAll(s.UploadURL, "{accountId}", c.accountID)
	return &s, nil
}

func (c *Client) ensureSession(ctx context.Context) error {
	if c.accountID != "" {
		return nil
	}
	_, err := c.Session(ctx)
	return err
}

type apiRequest struct {
	Using       []string         `json:"using"`
	MethodCalls [][3]interface{} `json:"methodCalls"`
}

type apiResponse struct {
	MethodResponses [][]json.RawMessage `json:"methodResponses"`
}

// call makes a single JMAP method call and decodes its result args into out
// (which may be nil if the caller doesn't need the response body).
func (c *Client) call(ctx context.Context, method string, args map[string]interface{}, out interface{}) error {
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	body := apiRequest{
		Using: []string{
			"urn:ietf:params:jmap:core",
			"urn:ietf:params:jmap:mail",
			"urn:ietf:params:jmap:submission",
		},
		MethodCalls: [][3]interface{}{{method, args, "c1"}},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.doReq(ctx, http.MethodPost, c.apiURL, bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("decode jmap response: %w", err)
	}
	if len(ar.MethodResponses) == 0 || len(ar.MethodResponses[0]) < 2 {
		return fmt.Errorf("jmap %s: empty or malformed response", method)
	}
	first := ar.MethodResponses[0]
	var name string
	json.Unmarshal(first[0], &name)
	if name == "error" {
		return fmt.Errorf("jmap %s error: %s", method, string(first[1]))
	}
	if out != nil {
		return json.Unmarshal(first[1], out)
	}
	return nil
}

func (c *Client) withAccount(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		args = map[string]interface{}{}
	}
	args["accountId"] = c.accountID
	return args
}

// ---- Mailboxes ----

// Mailbox is a JMAP folder.
type Mailbox struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ParentID     string `json:"parentId"`
	Role         string `json:"role"` // "inbox","sent","drafts","trash","junk", or "" for custom folders
	TotalEmails  int    `json:"totalEmails"`
	UnreadEmails int    `json:"unreadEmails"`
}

// InferFolderType maps a JMAP Mailbox role to gowebmail's folder type.
func InferFolderType(role string) string {
	switch role {
	case "inbox":
		return "inbox"
	case "sent":
		return "sent"
	case "drafts":
		return "drafts"
	case "trash":
		return "trash"
	case "junk":
		return "spam"
	default:
		return "custom"
	}
}

// ListMailboxes returns every mailbox (folder) for the account.
func (c *Client) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	var out struct {
		List []Mailbox `json:"list"`
	}
	if err := c.call(ctx, "Mailbox/get", c.withAccount(nil), &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// FindMailboxByRole returns the id of the mailbox with the given role (e.g.
// "sent", "inbox"), or an error if none is found.
func (c *Client) FindMailboxByRole(ctx context.Context, role string) (string, error) {
	boxes, err := c.ListMailboxes(ctx)
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		if b.Role == role {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("no mailbox with role %q", role)
}

// ---- Emails ----

// EmailAddr is a JMAP EmailAddress object.
type EmailAddr struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// BodyPart is an entry in an Email's textBody/htmlBody list.
type BodyPart struct {
	PartID string `json:"partId"`
	Type   string `json:"type"`
}

// BodyValue is the decoded content for one BodyPart, keyed by partId in Email.BodyValues.
type BodyValue struct {
	Value string `json:"value"`
}

// Email is a JMAP message. Keywords/mailboxIds mirror IMAP flags/folder
// membership, except a message here lives in exactly one mailbox (see
// tests/jmap-client.md — "single-mailbox membership").
type Email struct {
	ID            string               `json:"id"`
	MailboxIDs    map[string]bool      `json:"mailboxIds"`
	Keywords      map[string]bool      `json:"keywords"`
	Size          int                  `json:"size"`
	ReceivedAt    time.Time            `json:"receivedAt"`
	Subject       string               `json:"subject"`
	From          []EmailAddr          `json:"from"`
	To            []EmailAddr          `json:"to"`
	Preview       string               `json:"preview"`
	HasAttachment bool                 `json:"hasAttachment"`
	TextBody      []BodyPart           `json:"textBody"`
	HTMLBody      []BodyPart           `json:"htmlBody"`
	BodyValues    map[string]BodyValue `json:"bodyValues"`
}

func (e *Email) FromName() string {
	if len(e.From) == 0 {
		return ""
	}
	return e.From[0].Name
}

func (e *Email) FromEmail() string {
	if len(e.From) == 0 {
		return ""
	}
	return e.From[0].Email
}

func (e *Email) ToList() string {
	parts := make([]string, 0, len(e.To))
	for _, t := range e.To {
		parts = append(parts, t.Email)
	}
	return strings.Join(parts, ", ")
}

func (e *Email) IsRead() bool    { return e.Keywords["$seen"] }
func (e *Email) IsFlagged() bool { return e.Keywords["$flagged"] }

// TextValue returns the plain-text body, if fetched via GetEmailBody.
func (e *Email) TextValue() string {
	for _, p := range e.TextBody {
		if bv, ok := e.BodyValues[p.PartID]; ok {
			return bv.Value
		}
	}
	return ""
}

// HTMLValue returns the HTML body, if fetched via GetEmailBody.
func (e *Email) HTMLValue() string {
	for _, p := range e.HTMLBody {
		if bv, ok := e.BodyValues[p.PartID]; ok {
			return bv.Value
		}
	}
	return ""
}

// ListEmails returns cheap-field emails in mailboxID. Newest-first order is
// not guaranteed (the reference server's Email/query sort support is
// undocumented — see tests/jmap-client.md — so no sort is requested; callers
// that need a specific order should sort client-side).
func (c *Client) ListEmails(ctx context.Context, mailboxID string, limit int) ([]Email, error) {
	if limit <= 0 {
		limit = 100
	}
	var qout struct {
		IDs []string `json:"ids"`
	}
	qargs := c.withAccount(map[string]interface{}{
		"filter": map[string]string{"inMailbox": mailboxID},
		"limit":  limit,
	})
	if err := c.call(ctx, "Email/query", qargs, &qout); err != nil {
		return nil, err
	}
	if len(qout.IDs) == 0 {
		return nil, nil
	}
	return c.GetEmails(ctx, qout.IDs, false)
}

// GetEmails fetches full Email objects for ids. withBody also fetches
// text/html body content (an expensive decrypt+MIME-parse server-side).
func (c *Client) GetEmails(ctx context.Context, ids []string, withBody bool) ([]Email, error) {
	var out struct {
		List []Email `json:"list"`
	}
	args := c.withAccount(map[string]interface{}{"ids": ids})
	if withBody {
		args["fetchTextBodyValues"] = true
		args["fetchHTMLBodyValues"] = true
	}
	if err := c.call(ctx, "Email/get", args, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// GetEmailBody fetches a single email with its full text/html body.
func (c *Client) GetEmailBody(ctx context.Context, id string) (*Email, error) {
	list, err := c.GetEmails(ctx, []string{id}, true)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("email %s not found", id)
	}
	return &list[0], nil
}

// SetKeyword sets or clears a single keyword (e.g. "$seen", "$flagged") on a message.
func (c *Client) SetKeyword(ctx context.Context, emailID, keyword string, on bool) error {
	args := c.withAccount(map[string]interface{}{
		"update": map[string]interface{}{
			emailID: map[string]interface{}{"keywords/" + keyword: on},
		},
	})
	var out struct {
		NotUpdated map[string]json.RawMessage `json:"notUpdated"`
	}
	if err := c.call(ctx, "Email/set", args, &out); err != nil {
		return err
	}
	if e, bad := out.NotUpdated[emailID]; bad {
		return fmt.Errorf("jmap keyword update rejected: %s", e)
	}
	return nil
}

// MoveEmail reassigns a message to a different (single) mailbox.
func (c *Client) MoveEmail(ctx context.Context, emailID, destMailboxID string) error {
	args := c.withAccount(map[string]interface{}{
		"update": map[string]interface{}{
			emailID: map[string]interface{}{"mailboxIds": map[string]bool{destMailboxID: true}},
		},
	})
	var out struct {
		NotUpdated map[string]json.RawMessage `json:"notUpdated"`
	}
	if err := c.call(ctx, "Email/set", args, &out); err != nil {
		return err
	}
	if e, bad := out.NotUpdated[emailID]; bad {
		return fmt.Errorf("jmap move rejected: %s", e)
	}
	return nil
}

// DeleteEmail hard-deletes a message. Unlike Mailbox/set destroy (soft, see
// tests/jmap-client.md), Email/set destroy is a real, unrecoverable delete.
func (c *Client) DeleteEmail(ctx context.Context, emailID string) error {
	args := c.withAccount(map[string]interface{}{"destroy": []string{emailID}})
	var out struct {
		NotDestroyed map[string]json.RawMessage `json:"notDestroyed"`
	}
	if err := c.call(ctx, "Email/set", args, &out); err != nil {
		return err
	}
	if e, bad := out.NotDestroyed[emailID]; bad {
		return fmt.Errorf("jmap delete rejected: %s", e)
	}
	return nil
}

// ---- Sending ----

// UploadBlob uploads raw message bytes and returns the blob id.
func (c *Client) UploadBlob(ctx context.Context, data []byte) (string, error) {
	if err := c.ensureSession(ctx); err != nil {
		return "", err
	}
	resp, err := c.doReq(ctx, http.MethodPost, c.uploadURL, bytes.NewReader(data), "message/rfc822")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		BlobID string `json:"blobId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode jmap upload response: %w", err)
	}
	return out.BlobID, nil
}

// ImportEmail imports an uploaded blob as a message into mailboxID, returning
// the new email id. There is no Email/set create (see tests/jmap-client.md) —
// this upload+import step is the only way to add a message.
func (c *Client) ImportEmail(ctx context.Context, blobID, mailboxID string) (string, error) {
	args := c.withAccount(map[string]interface{}{
		"emails": map[string]interface{}{
			"c1": map[string]interface{}{
				"blobId":     blobID,
				"mailboxIds": map[string]bool{mailboxID: true},
			},
		},
	})
	var out struct {
		Created map[string]struct {
			ID string `json:"id"`
		} `json:"created"`
		NotCreated map[string]json.RawMessage `json:"notCreated"`
	}
	if err := c.call(ctx, "Email/import", args, &out); err != nil {
		return "", err
	}
	if created, ok := out.Created["c1"]; ok {
		return created.ID, nil
	}
	return "", fmt.Errorf("jmap import failed: %s", out.NotCreated["c1"])
}

// Submit sends a previously-imported message via EmailSubmission/set.
func (c *Client) Submit(ctx context.Context, emailID string) error {
	args := c.withAccount(map[string]interface{}{
		"create": map[string]interface{}{
			"s1": map[string]interface{}{"emailId": emailID},
		},
	})
	var out struct {
		NotCreated map[string]json.RawMessage `json:"notCreated"`
	}
	if err := c.call(ctx, "EmailSubmission/set", args, &out); err != nil {
		return err
	}
	if e, bad := out.NotCreated["s1"]; bad {
		return fmt.Errorf("jmap submission rejected: %s", e)
	}
	return nil
}

// Send uploads rawMessage, imports it into mailboxID (typically the Sent
// mailbox — the server doesn't auto-file after submission), and submits it
// for delivery.
func (c *Client) Send(ctx context.Context, mailboxID string, rawMessage []byte) error {
	blobID, err := c.UploadBlob(ctx, rawMessage)
	if err != nil {
		return fmt.Errorf("jmap upload: %w", err)
	}
	emailID, err := c.ImportEmail(ctx, blobID, mailboxID)
	if err != nil {
		return fmt.Errorf("jmap import: %w", err)
	}
	if err := c.Submit(ctx, emailID); err != nil {
		return fmt.Errorf("jmap submit: %w", err)
	}
	return nil
}
