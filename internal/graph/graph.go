// Package graph provides Microsoft Graph API mail access for personal
// outlook.com accounts. Personal accounts cannot use IMAP OAuth with
// custom Azure app registrations (Microsoft only issues opaque v1 tokens),
// so we use the Graph REST API instead with the JWT access token.
package graph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ghostersk/gowebmail/internal/models"
)

const baseURL = "https://graph.microsoft.com/v1.0/me"

// Client wraps Graph API calls for a single account.
type Client struct {
	token   string
	account *models.EmailAccount
	http    *http.Client
}

// New creates a Graph client for the given account.
func New(account *models.EmailAccount) *Client {
	return &Client{
		token:   account.AccessToken,
		account: account,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	fullURL := path
	if !strings.HasPrefix(path, "https://") {
		fullURL = baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph API %s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) patch(ctx context.Context, path string, body map[string]interface{}) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL+path,
		strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("graph PATCH %s returned %d", path, resp.StatusCode)
	}
	return nil
}

func (c *Client) deleteReq(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("graph DELETE %s returned %d", path, resp.StatusCode)
	}
	return nil
}

// ---- Folders ----

// GraphFolder represents a mail folder from Graph API.
type GraphFolder struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	TotalCount  int    `json:"totalItemCount"`
	UnreadCount int    `json:"unreadItemCount"`
	WellKnown   string `json:"wellKnownName"`
}

type foldersResp struct {
	Value    []GraphFolder `json:"value"`
	NextLink string        `json:"@odata.nextLink"`
}

// ListFolders returns all mail folders for the account.
func (c *Client) ListFolders(ctx context.Context) ([]GraphFolder, error) {
	var all []GraphFolder
	path := "/mailFolders?$top=100&$select=id,displayName,totalItemCount,unreadItemCount"
	for path != "" {
		var resp foldersResp
		if err := c.get(ctx, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Value...)
		if resp.NextLink != "" {
			path = resp.NextLink
		} else {
			path = ""
		}
	}
	return all, nil
}

// ---- Messages ----

// EmailAddress wraps a Graph email address object.
type EmailAddress struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

// GraphMessage represents a mail message from Graph API.
type GraphMessage struct {
	ID               string         `json:"id"`
	Subject          string         `json:"subject"`
	IsRead           bool           `json:"isRead"`
	Flag             struct{ Status string `json:"flagStatus"` } `json:"flag"`
	ReceivedDateTime time.Time      `json:"receivedDateTime"`
	HasAttachments   bool           `json:"hasAttachments"`
	From             *EmailAddress  `json:"from"`
	ToRecipients     []EmailAddress `json:"toRecipients"`
	CcRecipients     []EmailAddress `json:"ccRecipients"`
	Body             struct {
		Content     string `json:"content"`
		ContentType string `json:"contentType"`
	} `json:"body"`
	InternetMessageID string `json:"internetMessageId"`
}

// IsFlagged returns true if the message is flagged.
func (m *GraphMessage) IsFlagged() bool {
	return m.Flag.Status == "flagged"
}

// FromName returns the sender display name.
func (m *GraphMessage) FromName() string {
	if m.From == nil {
		return ""
	}
	return m.From.EmailAddress.Name
}

// FromEmail returns the sender email address.
func (m *GraphMessage) FromEmail() string {
	if m.From == nil {
		return ""
	}
	return m.From.EmailAddress.Address
}

// ToList returns a comma-separated list of recipients.
func (m *GraphMessage) ToList() string {
	var parts []string
	for _, r := range m.ToRecipients {
		parts = append(parts, r.EmailAddress.Address)
	}
	return strings.Join(parts, ", ")
}

type messagesResp struct {
	Value    []GraphMessage `json:"value"`
	NextLink string         `json:"@odata.nextLink"`
}

// ListMessages returns messages in a folder, optionally filtered by received date.
func (c *Client) ListMessages(ctx context.Context, folderID string, since time.Time, maxResults int) ([]GraphMessage, error) {
	filter := ""
	if !since.IsZero() {
		// OData filter: receivedDateTime gt 2006-01-02T15:04:05Z
		// Use strings.ReplaceAll to keep colons unencoded — Graph accepts this form
		dateStr := since.UTC().Format("2006-01-02T15:04:05Z")
		filter = "&$filter=receivedDateTime gt " + url.PathEscape(dateStr)
	}
	top := 50
	if maxResults > 0 && maxResults < top {
		top = maxResults
	}
	path := fmt.Sprintf("/mailFolders/%s/messages?$top=%d&$select=id,subject,isRead,flag,receivedDateTime,hasAttachments,from,toRecipients,internetMessageId%s&$orderby=receivedDateTime desc",
		folderID, top, filter)

	var all []GraphMessage
	for path != "" {
		var resp messagesResp
		if err := c.get(ctx, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Value...)
		if resp.NextLink != "" && (maxResults <= 0 || len(all) < maxResults) {
			path = resp.NextLink
		} else {
			path = ""
		}
	}
	return all, nil
}

// GetMessage returns a single message with full body.
func (c *Client) GetMessage(ctx context.Context, msgID string) (*GraphMessage, error) {
	var msg GraphMessage
	err := c.get(ctx, "/messages/"+msgID+
		"?$select=id,subject,isRead,flag,receivedDateTime,hasAttachments,from,toRecipients,ccRecipients,body,internetMessageId",
		&msg)
	return &msg, err
}

// GetMessageRaw returns the raw RFC 822 message bytes.
func (c *Client) GetMessageRaw(ctx context.Context, msgID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/messages/"+msgID+"/$value", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph raw message returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// MarkRead sets the isRead flag on a message.
func (c *Client) MarkRead(ctx context.Context, msgID string, read bool) error {
	return c.patch(ctx, "/messages/"+msgID, map[string]interface{}{"isRead": read})
}

// MarkFlagged sets or clears the flag on a message.
func (c *Client) MarkFlagged(ctx context.Context, msgID string, flagged bool) error {
	status := "notFlagged"
	if flagged {
		status = "flagged"
	}
	return c.patch(ctx, "/messages/"+msgID, map[string]interface{}{
		"flag": map[string]string{"flagStatus": status},
	})
}

// DeleteMessage moves a message to Deleted Items (soft delete).
func (c *Client) DeleteMessage(ctx context.Context, msgID string) error {
	return c.deleteReq(ctx, "/messages/"+msgID)
}

// MoveMessage moves a message to a different folder.
func (c *Client) MoveMessage(ctx context.Context, msgID, destFolderID string) error {
	b, _ := json.Marshal(map[string]string{"destinationId": destFolderID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/messages/"+msgID+"/move", strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("graph move returned %d", resp.StatusCode)
	}
	return nil
}

// InferFolderType maps Graph folder names/display names to GoWebMail folder types.
// WellKnown field is not selectable via $select — we infer from displayName instead.
func InferFolderType(displayName string) string {
	switch strings.ToLower(displayName) {
	case "inbox":
		return "inbox"
	case "sent items", "sent":
		return "sent"
	case "drafts":
		return "drafts"
	case "deleted items", "trash", "bin":
		return "trash"
	case "junk email", "spam", "junk":
		return "spam"
	case "archive":
		return "archive"
	default:
		return "custom"
	}
}

// WellKnownToFolderType kept for compatibility.
func WellKnownToFolderType(wk string) string {
	return InferFolderType(wk)
}

// ---- Send mail ----

// stripHTML does a minimal HTML→plain-text conversion for the text/plain fallback.
// Spam filters score HTML-only email negatively; sending both parts improves deliverability.
func stripHTML(s string) string {
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</tr>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(s)
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// SendMail sends an email via Graph API POST /me/sendMail.
// Sets both HTML and plain-text body to improve deliverability (spam filters
// penalise HTML-only messages with no text/plain alternative).
func (c *Client) SendMail(ctx context.Context, req *models.ComposeRequest) error {
	// Build body: always provide both HTML and plain text for better deliverability
	body := map[string]string{
		"contentType": "HTML",
		"content":     req.BodyHTML,
	}
	if req.BodyHTML == "" {
		body["contentType"] = "Text"
		body["content"] = req.BodyText
	}

	// Set explicit from with display name
	var fromField interface{}
	if c.account.DisplayName != "" {
		fromField = map[string]interface{}{
			"emailAddress": map[string]string{
				"address": c.account.EmailAddress,
				"name":    c.account.DisplayName,
			},
		}
	}

	msg := map[string]interface{}{
		"subject":       req.Subject,
		"body":          body,
		"toRecipients":  graphRecipients(req.To),
		"ccRecipients":  graphRecipients(req.CC),
		"bccRecipients": graphRecipients(req.BCC),
	}
	if fromField != nil {
		msg["from"] = fromField
	}

	if len(req.Attachments) > 0 {
		var atts []map[string]interface{}
		for _, a := range req.Attachments {
			atts = append(atts, map[string]interface{}{
				"@odata.type":  "#microsoft.graph.fileAttachment",
				"name":         a.Filename,
				"contentType":  a.ContentType,
				"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
			})
		}
		msg["attachments"] = atts
	}

	payload, err := json.Marshal(map[string]interface{}{
		"message":         msg,
		"saveToSentItems": true,
	})
	if err != nil {
		return fmt.Errorf("marshal sendMail: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/sendMail", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("build sendMail request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sendMail request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendMail returned %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

func graphRecipients(addrs []string) []map[string]interface{} {
	result := []map[string]interface{}{}
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a != "" {
			result = append(result, map[string]interface{}{
				"emailAddress": map[string]string{"address": a},
			})
		}
	}
	return result
}
