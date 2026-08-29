package syncer

import (
	"context"
	"fmt"
	"log"

	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/email"
	"github.com/ghostersk/gowebmail/internal/graph"
	"github.com/ghostersk/gowebmail/internal/models"
	"github.com/ghostersk/gowebmail/internal/rules"
)

// matchRule evaluates a message against an account's active rules (as loaded from the DB)
// and returns the matching models.Rule (with full action data), or nil if none match.
func matchRule(msg *models.Message, accountEmail string, activeRules []models.Rule) *models.Rule {
	if len(activeRules) == 0 {
		return nil
	}
	engineRules := make([]rules.Rule, 0, len(activeRules))
	for _, r := range activeRules {
		conds := make([]rules.Condition, 0, len(r.Conditions))
		for _, c := range r.Conditions {
			conds = append(conds, rules.Condition{Field: c.Field, Op: c.Op, Value: c.Value})
		}
		engineRules = append(engineRules, rules.Rule{
			ID: r.ID, Priority: r.Priority, Conditions: conds, MatchType: r.MatchType,
			Action: r.Action, ActionValue: r.ActionValue,
		})
	}
	mf := rules.MessageFields{
		From: msg.FromEmail, To: msg.ToList, Subject: msg.Subject, Body: msg.BodyText,
		HasAttachment: msg.HasAttachment, RecipientType: recipientType(msg, accountEmail),
	}
	matched := rules.Match(mf, engineRules)
	if matched == nil {
		return nil
	}
	for i := range activeRules {
		if activeRules[i].ID == matched.ID {
			return &activeRules[i]
		}
	}
	return nil
}

func recipientType(msg *models.Message, accountEmail string) string {
	if msg.CCList != "" && containsAddress(msg.CCList, accountEmail) {
		return "cc"
	}
	if msg.BCCList != "" && containsAddress(msg.BCCList, accountEmail) {
		return "bcc"
	}
	return "to"
}

func containsAddress(list, addr string) bool {
	// list is comma-separated; a substring check is enough since we only use this
	// to pick a synthetic recipient_type label, not for anything security-relevant.
	for _, part := range splitAndTrim(list) {
		if part == addr {
			return true
		}
	}
	return false
}

func splitAndTrim(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, trimLower(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, trimLower(cur))
	}
	return out
}

func trimLower(s string) string {
	// minimal trim, avoids pulling in strings just for this
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func parseUID(s string) uint32 {
	var uid uint32
	fmt.Sscanf(s, "%d", &uid)
	return uid
}

// ---- IMAP path ----

func (s *Scheduler) applyRuleIMAP(c *email.Client, account *models.EmailAccount, dbFolder *models.Folder, msg *models.Message, rule *models.Rule) {
	uid := parseUID(msg.RemoteUID)
	switch rule.Action {
	case "move_to_folder":
		dest, err := s.db.GetFolderByName(account.ID, rule.ActionValue)
		if err != nil || dest == nil {
			log.Printf("[rules] move_to_folder: folder %q not found for %s", rule.ActionValue, account.EmailAddress)
			return
		}
		s.db.EnqueueIMAPOp(&db.PendingIMAPOp{AccountID: account.ID, OpType: "move", RemoteUID: uid, FolderPath: dbFolder.FullPath, Extra: dest.FullPath})
		s.TriggerAccountSync(account.ID)
	case "mark_as_spam":
		junk, err := s.db.GetFolderByType(account.ID, "spam")
		if err != nil || junk == nil {
			log.Printf("[rules] mark_as_spam: no spam/junk folder found for %s", account.EmailAddress)
			return
		}
		s.db.EnqueueIMAPOp(&db.PendingIMAPOp{AccountID: account.ID, OpType: "move", RemoteUID: uid, FolderPath: dbFolder.FullPath, Extra: junk.FullPath})
		s.TriggerAccountSync(account.ID)
	case "delete":
		s.db.EnqueueIMAPOp(&db.PendingIMAPOp{AccountID: account.ID, OpType: "delete", RemoteUID: uid, FolderPath: dbFolder.FullPath})
		s.TriggerAccountSync(account.ID)
	case "mark_read":
		if err := c.SetFlagByUID(dbFolder.FullPath, uid, `\Seen`, true); err != nil {
			log.Printf("[rules] mark_read: %v", err)
		}
	case "forward":
		s.ruleForwardIMAP(account, msg, rule.ActionValue)
	case "auto_reply":
		s.ruleAutoReplyIMAP(account, msg, rule)
	}
}

func (s *Scheduler) ruleForwardIMAP(account *models.EmailAccount, msg *models.Message, to string) {
	req := &models.ComposeRequest{
		AccountID: account.ID,
		To:        []string{to},
		Subject:   "Fwd: " + msg.Subject,
		BodyHTML:  msg.BodyHTML,
		BodyText:  msg.BodyText,
	}
	if err := email.SendMessageFull(context.Background(), account, req, nil); err != nil {
		log.Printf("[rules] forward to %s: %v", to, err)
	}
}

func (s *Scheduler) ruleAutoReplyIMAP(account *models.EmailAccount, msg *models.Message, rule *models.Rule) {
	recipient := msg.FromEmail
	if recipient == "" {
		return // never reply to a bounce/empty sender — avoids loops
	}
	if sent, err := s.db.HasRecentAutoReply(account.ID, rule.ID, recipient); err != nil || sent {
		return
	}
	req := &models.ComposeRequest{
		AccountID: account.ID,
		To:        []string{recipient},
		Subject:   rule.ActionValue,
		BodyText:  rule.ActionOptions.Body,
		BodyHTML:  rule.ActionOptions.Body,
	}
	if err := email.SendMessageFull(context.Background(), account, req, nil); err != nil {
		log.Printf("[rules] auto_reply to %s: %v", recipient, err)
		return
	}
	s.db.LogAutoReply(account.ID, rule.ID, recipient)
}

// ---- Graph (personal Outlook.com) path ----
// msg.RemoteUID already holds the opaque Graph message ID (set at construction in graphDeltaSync).

func (s *Scheduler) applyRuleGraph(gc *graph.Client, account *models.EmailAccount, msg *models.Message, rule *models.Rule) {
	ctx := context.Background()
	switch rule.Action {
	case "move_to_folder":
		dest, err := s.db.GetFolderByName(account.ID, rule.ActionValue)
		if err != nil || dest == nil {
			log.Printf("[rules] move_to_folder: folder %q not found for %s", rule.ActionValue, account.EmailAddress)
			return
		}
		if err := gc.MoveMessage(ctx, msg.RemoteUID, dest.FullPath); err != nil {
			log.Printf("[rules] graph move: %v", err)
		}
	case "mark_as_spam":
		junk, err := s.db.GetFolderByType(account.ID, "spam")
		if err != nil || junk == nil {
			log.Printf("[rules] mark_as_spam: no spam/junk folder found for %s", account.EmailAddress)
			return
		}
		if err := gc.MoveMessage(ctx, msg.RemoteUID, junk.FullPath); err != nil {
			log.Printf("[rules] graph move: %v", err)
		}
	case "delete":
		if err := gc.DeleteMessage(ctx, msg.RemoteUID); err != nil {
			log.Printf("[rules] graph delete: %v", err)
		}
	case "mark_read":
		if err := gc.MarkRead(ctx, msg.RemoteUID, true); err != nil {
			log.Printf("[rules] graph mark_read: %v", err)
		}
	case "forward":
		s.ruleForwardGraph(gc, account, msg, rule.ActionValue)
	case "auto_reply":
		s.ruleAutoReplyGraph(gc, account, msg, rule)
	}
}

func (s *Scheduler) ruleForwardGraph(gc *graph.Client, account *models.EmailAccount, msg *models.Message, to string) {
	req := &models.ComposeRequest{
		AccountID: account.ID,
		To:        []string{to},
		Subject:   "Fwd: " + msg.Subject,
		BodyHTML:  msg.BodyHTML,
		BodyText:  msg.BodyText,
	}
	if err := gc.SendMail(context.Background(), req); err != nil {
		log.Printf("[rules] graph forward to %s: %v", to, err)
	}
}

func (s *Scheduler) ruleAutoReplyGraph(gc *graph.Client, account *models.EmailAccount, msg *models.Message, rule *models.Rule) {
	recipient := msg.FromEmail
	if recipient == "" {
		return
	}
	if sent, err := s.db.HasRecentAutoReply(account.ID, rule.ID, recipient); err != nil || sent {
		return
	}
	req := &models.ComposeRequest{
		AccountID: account.ID,
		To:        []string{recipient},
		Subject:   rule.ActionValue,
		BodyText:  rule.ActionOptions.Body,
		BodyHTML:  rule.ActionOptions.Body,
	}
	if err := gc.SendMail(context.Background(), req); err != nil {
		log.Printf("[rules] graph auto_reply to %s: %v", recipient, err)
		return
	}
	s.db.LogAutoReply(account.ID, rule.ID, recipient)
}
