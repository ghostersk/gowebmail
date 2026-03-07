// Package email provides IMAP fetch/sync and SMTP send.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/smtp"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	gomailModels "github.com/yourusername/gomail/internal/models"
)

func imapHostFor(provider gomailModels.AccountProvider) (string, int) {
	switch provider {
	case gomailModels.ProviderGmail:
		return "imap.gmail.com", 993
	case gomailModels.ProviderOutlook:
		return "outlook.office365.com", 993
	default:
		return "", 993
	}
}

func smtpHostFor(provider gomailModels.AccountProvider) (string, int) {
	switch provider {
	case gomailModels.ProviderGmail:
		return "smtp.gmail.com", 587
	case gomailModels.ProviderOutlook:
		return "smtp.office365.com", 587
	default:
		return "", 587
	}
}

// ---- SASL / OAuth2 Auth ----

type xoauth2Client struct{ user, token string }

func (x *xoauth2Client) Start() (string, []byte, error) {
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", x.user, x.token)
	return "XOAUTH2", []byte(payload), nil
}
func (x *xoauth2Client) Next([]byte) ([]byte, error) { return []byte{}, nil }

type xoauth2SMTP struct{ user, token string }

func (x *xoauth2SMTP) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", x.user, x.token)
	return "XOAUTH2", []byte(payload), nil
}
func (x *xoauth2SMTP) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		if dec, err := base64.StdEncoding.DecodeString(string(fromServer)); err == nil {
			return nil, fmt.Errorf("XOAUTH2 error: %s", dec)
		}
		return nil, fmt.Errorf("XOAUTH2 error: %s", fromServer)
	}
	return nil, nil
}

// ---- IMAP Client ----

type Client struct {
	imap    *client.Client
	account *gomailModels.EmailAccount
}

func Connect(ctx context.Context, account *gomailModels.EmailAccount) (*Client, error) {
	host, port := imapHostFor(account.Provider)
	if account.IMAPHost != "" {
		host = account.IMAPHost
		port = account.IMAPPort
	}
	if host == "" {
		return nil, fmt.Errorf("IMAP host not configured for account %s", account.EmailAddress)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	var c *client.Client
	var err error

	if port == 993 {
		c, err = client.DialTLS(addr, &tls.Config{ServerName: host})
	} else {
		c, err = client.Dial(addr)
		if err == nil {
			// Attempt STARTTLS; ignore error if server doesn't support it
			_ = c.StartTLS(&tls.Config{ServerName: host})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("IMAP connect %s: %w", addr, err)
	}

	switch account.Provider {
	case gomailModels.ProviderGmail, gomailModels.ProviderOutlook:
		sasl := &xoauth2Client{user: account.EmailAddress, token: account.AccessToken}
		if err := c.Authenticate(sasl); err != nil {
			c.Logout()
			return nil, fmt.Errorf("IMAP OAuth auth failed: %w", err)
		}
	default:
		if err := c.Login(account.EmailAddress, account.AccessToken); err != nil {
			c.Logout()
			return nil, fmt.Errorf("IMAP login failed for %s: %w", account.EmailAddress, err)
		}
	}

	return &Client{imap: c, account: account}, nil
}

func TestConnection(account *gomailModels.EmailAccount) error {
	c, err := Connect(context.Background(), account)
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

func (c *Client) Close() { c.imap.Logout() }

func (c *Client) DeleteMailbox(name string) error {
	return c.imap.Delete(name)
}

// MoveByUID copies a message to destMailbox and marks it deleted in srcMailbox.
func (c *Client) MoveByUID(srcMailbox, destMailbox string, uid uint32) error {
	if _, err := c.imap.Select(srcMailbox, false); err != nil {
		return fmt.Errorf("select %s: %w", srcMailbox, err)
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	// COPY to destination
	if err := c.imap.UidCopy(seqSet, destMailbox); err != nil {
		return fmt.Errorf("uid copy: %w", err)
	}
	// Mark deleted in source
	item := imap.FormatFlagsOp(imap.SetFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := c.imap.UidStore(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("uid store deleted: %w", err)
	}
	return c.imap.Expunge(nil)
}

// DeleteByUID moves message to Trash, or hard-deletes if already in Trash.
func (c *Client) DeleteByUID(mailboxName string, uid uint32, trashName string) error {
	if _, err := c.imap.Select(mailboxName, false); err != nil {
		return fmt.Errorf("select %s: %w", mailboxName, err)
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	isTrash := strings.EqualFold(mailboxName, trashName) || trashName == ""
	if !isTrash && trashName != "" {
		// Move to trash
		if err := c.imap.UidCopy(seqSet, trashName); err == nil {
			item := imap.FormatFlagsOp(imap.SetFlags, true)
			_ = c.imap.UidStore(seqSet, item, []interface{}{imap.DeletedFlag}, nil)
			return c.imap.Expunge(nil)
		}
	}
	// Hard delete (already in trash or no trash folder)
	item := imap.FormatFlagsOp(imap.SetFlags, true)
	if err := c.imap.UidStore(seqSet, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return fmt.Errorf("uid store deleted: %w", err)
	}
	return c.imap.Expunge(nil)
}

// SetFlagByUID sets or clears an IMAP flag (e.g. \Seen, \Flagged) for a message.
func (c *Client) SetFlagByUID(mailboxName string, uid uint32, flag string, set bool) error {
	if _, err := c.imap.Select(mailboxName, false); err != nil {
		return err
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	var op imap.FlagsOp
	if set {
		op = imap.AddFlags
	} else {
		op = imap.RemoveFlags
	}
	item := imap.FormatFlagsOp(op, true)
	return c.imap.UidStore(seqSet, item, []interface{}{flag}, nil)
}

// FetchRawByUID returns the raw RFC 822 message bytes for the given UID.
func (c *Client) FetchRawByUID(mailboxName string, uid uint32) ([]byte, error) {
	if _, err := c.imap.Select(mailboxName, true); err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{section.FetchItem()}
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() { done <- c.imap.UidFetch(seqSet, items, ch) }()
	msg := <-ch
	if err := <-done; err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found")
	}
	body := msg.GetBody(section)
	if body == nil {
		return nil, fmt.Errorf("no body")
	}
	return io.ReadAll(body)
}

func (c *Client) ListMailboxes() ([]*imap.MailboxInfo, error) {
	ch := make(chan *imap.MailboxInfo, 64)
	done := make(chan error, 1)
	go func() { done <- c.imap.List("", "*", ch) }()
	var result []*imap.MailboxInfo
	for mb := range ch {
		result = append(result, mb)
	}
	return result, <-done
}

// FetchMessages fetches messages from a mailbox.
// If days <= 0, fetches ALL messages. Otherwise fetches messages since `days` days ago.
func (c *Client) FetchMessages(mailboxName string, days int) ([]*gomailModels.Message, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	if mbox.Messages == 0 {
		return nil, nil
	}

	var uids []uint32
	if days <= 0 {
		// Fetch ALL messages — empty criteria matches everything
		uids, err = c.imap.UidSearch(imap.NewSearchCriteria())
	} else {
		since := time.Now().AddDate(0, 0, -days)
		criteria := imap.NewSearchCriteria()
		criteria.Since = since
		uids, err = c.imap.UidSearch(criteria)
	}

	if err != nil || len(uids) == 0 {
		// Fallback: fetch last 500 by sequence number
		from := uint32(1)
		if mbox.Messages > 500 {
			from = mbox.Messages - 499
		}
		seqSet := new(imap.SeqSet)
		seqSet.AddRange(from, mbox.Messages)
		return c.fetchBySeqSet(seqSet)
	}

	seqSet := new(imap.SeqSet)
	for _, uid := range uids {
		seqSet.AddNum(uid)
	}
	return c.fetchByUIDSet(seqSet)
}

func (c *Client) fetchBySeqSet(seqSet *imap.SeqSet) ([]*gomailModels.Message, error) {
	// Fetch FetchRFC822 (full raw message) so we can properly parse MIME
	items := []imap.FetchItem{
		imap.FetchUid, imap.FetchEnvelope,
		imap.FetchFlags, imap.FetchBodyStructure,
		imap.FetchRFC822, // full message including headers – needed for proper MIME parsing
	}

	ch := make(chan *imap.Message, 64)
	done := make(chan error, 1)
	go func() { done <- c.imap.Fetch(seqSet, items, ch) }()

	var results []*gomailModels.Message
	for msg := range ch {
		m, err := parseIMAPMessage(msg, c.account)
		if err != nil {
			log.Printf("parse message uid=%d: %v", msg.Uid, err)
			continue
		}
		results = append(results, m)
	}
	if err := <-done; err != nil {
		return results, fmt.Errorf("fetch: %w", err)
	}
	return results, nil
}

// fetchByUIDSet fetches messages by UID set (used when UIDs are returned from UidSearch).
func (c *Client) fetchByUIDSet(seqSet *imap.SeqSet) ([]*gomailModels.Message, error) {
	items := []imap.FetchItem{
		imap.FetchUid, imap.FetchEnvelope,
		imap.FetchFlags, imap.FetchBodyStructure,
		imap.FetchRFC822,
	}

	ch := make(chan *imap.Message, 64)
	done := make(chan error, 1)
	go func() { done <- c.imap.UidFetch(seqSet, items, ch) }()

	var results []*gomailModels.Message
	for msg := range ch {
		m, err := parseIMAPMessage(msg, c.account)
		if err != nil {
			log.Printf("parse message uid=%d: %v", msg.Uid, err)
			continue
		}
		results = append(results, m)
	}
	if err := <-done; err != nil {
		return results, fmt.Errorf("uid fetch: %w", err)
	}
	return results, nil
}

func parseIMAPMessage(msg *imap.Message, account *gomailModels.EmailAccount) (*gomailModels.Message, error) {
	m := &gomailModels.Message{
		AccountID: account.ID,
		RemoteUID: fmt.Sprintf("%d", msg.Uid),
	}

	if env := msg.Envelope; env != nil {
		m.Subject = env.Subject
		m.Date = env.Date
		m.MessageID = env.MessageId
		if len(env.From) > 0 {
			m.FromEmail = env.From[0].Address()
			m.FromName = env.From[0].PersonalName
		}
		m.ToList = formatAddressList(env.To)
		m.CCList = formatAddressList(env.Cc)
		m.BCCList = formatAddressList(env.Bcc)
		if len(env.ReplyTo) > 0 {
			m.ReplyTo = env.ReplyTo[0].Address()
		}
	}

	for _, flag := range msg.Flags {
		switch flag {
		case imap.SeenFlag:
			m.IsRead = true
		case imap.FlaggedFlag:
			m.IsStarred = true
		case imap.DraftFlag:
			m.IsDraft = true
		}
	}

	// Parse MIME body from the full raw RFC822 message
	for _, literal := range msg.Body {
		raw, err := io.ReadAll(literal)
		if err != nil {
			continue
		}
		text, html, attachments := parseMIME(raw)
		if m.BodyText == "" {
			m.BodyText = text
		}
		if m.BodyHTML == "" {
			m.BodyHTML = html
		}
		if len(attachments) > 0 {
			m.Attachments = append(m.Attachments, attachments...)
			m.HasAttachment = true
		}
		break // only need first body literal
	}

	if msg.BodyStructure != nil && !m.HasAttachment {
		m.HasAttachment = hasAttachment(msg.BodyStructure)
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	return m, nil
}

// parseMIME takes a full RFC822 raw message (with headers) and extracts
// text/plain, text/html and attachment metadata.
func parseMIME(raw []byte) (text, html string, attachments []gomailModels.Attachment) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		// Last-resort fallback: treat whole thing as plain text, strip obvious headers
		return stripMIMEHeaders(string(raw)), "", nil
	}
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	body, _ := io.ReadAll(msg.Body)
	text, html, attachments = parsePart(ct, msg.Header.Get("Content-Transfer-Encoding"), body)
	return
}

// parsePart recursively handles a MIME part.
func parsePart(contentType, transferEncoding string, body []byte) (text, html string, attachments []gomailModels.Attachment) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return string(body), "", nil
	}
	mediaType = strings.ToLower(mediaType)

	decoded := decodeTransfer(transferEncoding, body)

	switch {
	case mediaType == "text/plain":
		text = decodeCharset(params["charset"], decoded)
	case mediaType == "text/html":
		html = decodeCharset(params["charset"], decoded)
	case strings.HasPrefix(mediaType, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return string(decoded), "", nil
		}
		mr := multipart.NewReader(bytes.NewReader(decoded), boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			partBody, _ := io.ReadAll(part)
			partCT := part.Header.Get("Content-Type")
			if partCT == "" {
				partCT = "text/plain"
			}
			partTE := part.Header.Get("Content-Transfer-Encoding")
			disposition := part.Header.Get("Content-Disposition")
			dispType, dispParams, _ := mime.ParseMediaType(disposition)

			if strings.EqualFold(dispType, "attachment") {
				filename := dispParams["filename"]
				if filename == "" {
					filename = part.FileName()
				}
				if filename == "" {
					filename = "attachment"
				}
				partMedia, _, _ := mime.ParseMediaType(partCT)
				attachments = append(attachments, gomailModels.Attachment{
					Filename:    filename,
					ContentType: partMedia,
					Size:        int64(len(partBody)),
				})
				continue
			}

			t, h, atts := parsePart(partCT, partTE, partBody)
			if text == "" && t != "" {
				text = t
			}
			if html == "" && h != "" {
				html = h
			}
			attachments = append(attachments, atts...)
		}
	default:
		// Any other type – treat as attachment if it has a filename
		mt, _, _ := mime.ParseMediaType(contentType)
		_ = mt
	}
	return
}

func decodeTransfer(encoding string, data []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		// Strip whitespace before decoding
		cleaned := bytes.ReplaceAll(data, []byte("\r\n"), []byte(""))
		cleaned = bytes.ReplaceAll(cleaned, []byte("\n"), []byte(""))
		dst := make([]byte, base64.StdEncoding.DecodedLen(len(cleaned)))
		n, err := base64.StdEncoding.Decode(dst, cleaned)
		if err != nil {
			return data
		}
		return dst[:n]
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err != nil {
			return data
		}
		return decoded
	default:
		return data
	}
}

func decodeCharset(charset string, data []byte) string {
	// We only handle UTF-8 and ASCII natively; for others return as-is
	// (a proper charset library would be needed for full support)
	return string(data)
}

// stripMIMEHeaders removes MIME boundary/header lines from a raw body string
// when proper parsing fails completely.
func stripMIMEHeaders(raw string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	inHeader := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") && len(trimmed) > 2 {
			inHeader = true
			continue
		}
		if inHeader {
			if trimmed == "" {
				inHeader = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func formatAddressList(addrs []*imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.PersonalName != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.PersonalName, a.Address()))
		} else {
			parts = append(parts, a.Address())
		}
	}
	return strings.Join(parts, ", ")
}

func hasAttachment(bs *imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	if strings.EqualFold(bs.Disposition, "attachment") {
		return true
	}
	for _, part := range bs.Parts {
		if hasAttachment(part) {
			return true
		}
	}
	return false
}

// InferFolderType returns a canonical folder type from mailbox name/attributes.
func InferFolderType(name string, attributes []string) string {
	for _, attr := range attributes {
		switch strings.ToLower(attr) {
		case `\inbox`:
			return "inbox"
		case `\sent`:
			return "sent"
		case `\drafts`:
			return "drafts"
		case `\trash`, `\deleted`:
			return "trash"
		case `\junk`, `\spam`:
			return "spam"
		case `\archive`:
			return "archive"
		}
	}
	lower := strings.ToLower(name)
	switch {
	case lower == "inbox":
		return "inbox"
	case strings.Contains(lower, "sent"):
		return "sent"
	case strings.Contains(lower, "draft"):
		return "drafts"
	case strings.Contains(lower, "trash") || strings.Contains(lower, "deleted"):
		return "trash"
	case strings.Contains(lower, "spam") || strings.Contains(lower, "junk"):
		return "spam"
	case strings.Contains(lower, "archive"):
		return "archive"
	default:
		return "custom"
	}
}

// ---- SMTP Send ----

// plainAuthNoTLSCheck implements smtp.Auth for AUTH PLAIN without
// Go stdlib's built-in TLS-required restriction.
type plainAuthNoTLSCheck struct{ username, password string }

func (a *plainAuthNoTLSCheck) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	// AUTH PLAIN payload: \0username\0password
	b := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", b, nil
}
func (a *plainAuthNoTLSCheck) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge")
	}
	return nil, nil
}

// loginAuth implements AUTH LOGIN (older servers that don't support PLAIN).
type loginAuth struct{ username, password string }

func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.Contains(prompt, "username") || strings.Contains(prompt, "user"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password") || strings.Contains(prompt, "pass"):
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected login prompt: %s", fromServer)
}

func authSMTP(c *smtp.Client, account *gomailModels.EmailAccount, host string) error {
	switch account.Provider {
	case gomailModels.ProviderGmail, gomailModels.ProviderOutlook:
		return c.Auth(&xoauth2SMTP{user: account.EmailAddress, token: account.AccessToken})
	default:
		ok, authAdvert := c.Extension("AUTH")
		if !ok {
			// No AUTH advertised — some servers (e.g. local relays) don't require it
			return nil
		}
		authLine := strings.ToUpper(authAdvert)
		if strings.Contains(authLine, "PLAIN") {
			return c.Auth(&plainAuthNoTLSCheck{username: account.EmailAddress, password: account.AccessToken})
		}
		if strings.Contains(authLine, "LOGIN") {
			return c.Auth(&loginAuth{username: account.EmailAddress, password: account.AccessToken})
		}
		// Fall back to PLAIN anyway (most servers accept it even if not advertised post-TLS)
		return c.Auth(&plainAuthNoTLSCheck{username: account.EmailAddress, password: account.AccessToken})
	}
}

// SendMessageFull sends an email via SMTP using the account's configured server.
// It also appends the sent message to the IMAP Sent folder.
func SendMessageFull(ctx context.Context, account *gomailModels.EmailAccount, req *gomailModels.ComposeRequest) error {
	host, port := smtpHostFor(account.Provider)
	if account.SMTPHost != "" {
		host = account.SMTPHost
		port = account.SMTPPort
	}
	if host == "" {
		return fmt.Errorf("SMTP host not configured")
	}

	var buf bytes.Buffer
	buildMIMEMessage(&buf, account, req)
	rawMsg := buf.Bytes()

	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("[SMTP] dialing %s for account %s", addr, account.EmailAddress)

	var c *smtp.Client
	var err error

	if port == 465 {
		// Implicit TLS (SMTPS)
		conn, err2 := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
		if err2 != nil {
			return fmt.Errorf("SMTPS dial %s: %w", addr, err2)
		}
		c, err = smtp.NewClient(conn, host)
	} else {
		// Plain SMTP then upgrade with STARTTLS (port 587 / 25)
		c, err = smtp.Dial(addr)
		if err == nil {
			// EHLO with sender's domain (not "localhost") to avoid rejection by strict MTAs
			senderDomain := "localhost"
			if parts := strings.Split(account.EmailAddress, "@"); len(parts) == 2 {
				senderDomain = parts[1]
			}
			if err2 := c.Hello(senderDomain); err2 != nil {
				c.Close()
				return fmt.Errorf("SMTP EHLO: %w", err2)
			}
			if ok, _ := c.Extension("STARTTLS"); ok {
				if err2 := c.StartTLS(&tls.Config{ServerName: host}); err2 != nil {
					c.Close()
					return fmt.Errorf("STARTTLS %s: %w", host, err2)
				}
			}
		}
	}
	if err != nil {
		return fmt.Errorf("SMTP dial %s: %w", addr, err)
	}
	defer c.Close()

	if err := authSMTP(c, account, host); err != nil {
		return fmt.Errorf("SMTP auth failed for %s: %w", account.EmailAddress, err)
	}
	log.Printf("[SMTP] auth OK")

	if err := c.Mail(account.EmailAddress); err != nil {
		return fmt.Errorf("SMTP MAIL FROM <%s>: %w", account.EmailAddress, err)
	}

	allRcpt := append(append([]string{}, req.To...), req.CC...)
	allRcpt = append(allRcpt, req.BCC...)
	for _, rcpt := range allRcpt {
		rcpt = strings.TrimSpace(rcpt)
		if rcpt == "" {
			continue
		}
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("SMTP RCPT TO <%s>: %w", rcpt, err)
		}
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := wc.Write(rawMsg); err != nil {
		wc.Close()
		return fmt.Errorf("SMTP write: %w", err)
	}
	if err := wc.Close(); err != nil {
		// DATA close is where the server accepts or rejects the message
		return fmt.Errorf("SMTP server rejected message: %w", err)
	}
	log.Printf("[SMTP] message accepted by server")
	_ = c.Quit()

	// Append to Sent folder via IMAP (best-effort, don't fail the send)
	go func() {
		imapClient, err := Connect(context.Background(), account)
		if err != nil {
			log.Printf("AppendToSent: IMAP connect: %v", err)
			return
		}
		defer imapClient.Close()
		if err := imapClient.AppendToSent(rawMsg); err != nil {
			log.Printf("AppendToSent: %v", err)
		}
	}()

	return nil
}

func buildMIMEMessage(buf *bytes.Buffer, account *gomailModels.EmailAccount, req *gomailModels.ComposeRequest) string {
	from := netmail.Address{Name: account.DisplayName, Address: account.EmailAddress}
	boundary := fmt.Sprintf("gomail_%x", time.Now().UnixNano())
	// Use the sender's actual domain for Message-ID so it passes spam filters
	domain := account.EmailAddress
	if at := strings.Index(domain, "@"); at >= 0 {
		domain = domain[at+1:]
	}
	msgID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), strings.ReplaceAll(account.EmailAddress, "@", "."), domain)

	buf.WriteString("Message-ID: " + msgID + "\r\n")
	buf.WriteString("From: " + from.String() + "\r\n")
	buf.WriteString("To: " + strings.Join(req.To, ", ") + "\r\n")
	if len(req.CC) > 0 {
		buf.WriteString("Cc: " + strings.Join(req.CC, ", ") + "\r\n")
	}
	// Never write BCC to headers — only used for RCPT TO commands
	buf.WriteString("Subject: " + encodeMIMEHeader(req.Subject) + "\r\n")
	buf.WriteString("Date: " + time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	buf.WriteString("\r\n")

	// Plain text part
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qpw := quotedprintable.NewWriter(buf)
	plainText := req.BodyText
	if plainText == "" && req.BodyHTML != "" {
		plainText = htmlToPlainText(req.BodyHTML)
	}
	qpw.Write([]byte(plainText))
	qpw.Close()
	buf.WriteString("\r\n")

	// HTML part
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qpw2 := quotedprintable.NewWriter(buf)
	if req.BodyHTML != "" {
		qpw2.Write([]byte(req.BodyHTML))
	} else {
		qpw2.Write([]byte("<pre>" + htmlEscape(plainText) + "</pre>"))
	}
	qpw2.Close()
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "--\r\n")
	return msgID
}

// encodeMIMEHeader encodes a header value with UTF-8 if it contains non-ASCII chars.
func encodeMIMEHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// htmlToPlainText does a very basic HTML→plain-text strip for the text/plain fallback.
func htmlToPlainText(html string) string {
	// Strip tags
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			out.WriteRune(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	// Collapse excessive whitespace
	lines := strings.Split(out.String(), "\n")
	var result []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			result = append(result, l)
		}
	}
	return strings.Join(result, "\n")
}

// AppendToSent saves the sent message to the IMAP Sent folder via APPEND command.
func (c *Client) AppendToSent(rawMsg []byte) error {
	mailboxes, err := c.ListMailboxes()
	if err != nil {
		return err
	}
	// Find the Sent folder
	var sentName string
	for _, mb := range mailboxes {
		ft := InferFolderType(mb.Name, mb.Attributes)
		if ft == "sent" {
			sentName = mb.Name
			break
		}
	}
	if sentName == "" {
		return nil // no Sent folder found, skip silently
	}
	flags := []string{imap.SeenFlag}
	now := time.Now()
	return c.imap.Append(sentName, flags, now, bytes.NewReader(rawMsg))
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// FetchAttachmentData fetches the raw bytes for a specific attachment part from IMAP.
// partPath is a dot-separated MIME section path e.g. "2" or "1.2"
func (c *Client) FetchAttachmentData(mailboxName, uid, partPath string) ([]byte, string, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil || mbox == nil {
		return nil, "", fmt.Errorf("select %s: %w", mailboxName, err)
	}

	seqSet := new(imap.SeqSet)
	// Use UID fetch
	bodySection := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{
			Specifier: imap.MIMESpecifier,
			Path:      partPathToInts(partPath),
		},
	}
	items := []imap.FetchItem{bodySection.FetchItem()}

	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	uidNum := uint32(0)
	fmt.Sscanf(uid, "%d", &uidNum)
	seqSet.AddNum(uidNum)
	go func() { done <- c.imap.Fetch(seqSet, items, ch) }()

	var data []byte
	for msg := range ch {
		for _, literal := range msg.Body {
			data, _ = io.ReadAll(literal)
			break
		}
	}
	if err := <-done; err != nil {
		return nil, "", err
	}
	ct := "application/octet-stream"
	ext := filepath.Ext(partPath)
	if ext != "" {
		ct = mime.TypeByExtension(ext)
	}
	return data, ct, nil
}

func partPathToInts(path string) []int {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	result := make([]int, 0, len(parts))
	for _, p := range parts {
		var n int
		fmt.Sscanf(p, "%d", &n)
		result = append(result, n)
	}
	return result
}

// ---- Delta sync helpers ----

// FolderStatus returns the current UIDVALIDITY, UIDNEXT, and message count
// for a mailbox without fetching any messages.
type FolderStatus struct {
	UIDValidity uint32
	UIDNext     uint32
	Messages    uint32
}

func (c *Client) GetFolderStatus(mailboxName string) (*FolderStatus, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	return &FolderStatus{
		UIDValidity: mbox.UidValidity,
		UIDNext:     mbox.UidNext,
		Messages:    mbox.Messages,
	}, nil
}

// ListAllUIDs returns all UIDs currently in the mailbox. Used for purge detection.
func (c *Client) ListAllUIDs(mailboxName string) ([]uint32, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	if mbox.Messages == 0 {
		return nil, nil
	}
	uids, err := c.imap.UidSearch(imap.NewSearchCriteria())
	if err != nil {
		return nil, fmt.Errorf("uid search all: %w", err)
	}
	return uids, nil
}

// FetchNewMessages fetches only messages with UID > afterUID (incremental).
func (c *Client) FetchNewMessages(mailboxName string, afterUID uint32) ([]*gomailModels.Message, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	if mbox.Messages == 0 {
		return nil, nil
	}

	// SEARCH UID afterUID+1:*
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(afterUID+1, ^uint32(0)) // afterUID+1 to * (max)

	items := []imap.FetchItem{
		imap.FetchUid, imap.FetchEnvelope,
		imap.FetchFlags, imap.FetchBodyStructure,
		imap.FetchRFC822,
	}

	ch := make(chan *imap.Message, 64)
	done := make(chan error, 1)
	go func() { done <- c.imap.UidFetch(seqSet, items, ch) }()

	var results []*gomailModels.Message
	for msg := range ch {
		if msg.Uid <= afterUID {
			continue // skip if server returns older (shouldn't happen)
		}
		m, err := parseIMAPMessage(msg, c.account)
		if err != nil {
			log.Printf("parse message uid=%d: %v", msg.Uid, err)
			continue
		}
		results = append(results, m)
	}
	if err := <-done; err != nil {
		// UID range with no results gives an error on some servers — treat as empty
		if strings.Contains(err.Error(), "No matching messages") ||
			strings.Contains(err.Error(), "BADUID") ||
			strings.Contains(err.Error(), "UID range") {
			return nil, nil
		}
		return results, fmt.Errorf("uid fetch new: %w", err)
	}
	return results, nil
}

// SyncFlags fetches FLAGS for all messages in a mailbox efficiently.
// Returns map[uid]->flags for reconciliation with local state.
func (c *Client) SyncFlags(mailboxName string) (map[uint32][]string, error) {
	mbox, err := c.imap.Select(mailboxName, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	if mbox.Messages == 0 {
		return map[uint32][]string{}, nil
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddRange(1, mbox.Messages)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchFlags}

	ch := make(chan *imap.Message, 256)
	done := make(chan error, 1)
	go func() { done <- c.imap.Fetch(seqSet, items, ch) }()

	result := make(map[uint32][]string, mbox.Messages)
	for msg := range ch {
		result[msg.Uid] = msg.Flags
	}
	if err := <-done; err != nil {
		return result, fmt.Errorf("fetch flags: %w", err)
	}
	return result, nil
}

// SelectMailbox selects a mailbox and returns its status info.
func (c *Client) SelectMailbox(name string) (*imap.MailboxStatus, error) {
	return c.imap.Select(name, true)
}
