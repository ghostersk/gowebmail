package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ghostersk/gowebmail/internal/models"
)

// newTestDB creates a fresh, migrated DB backed by a temp file (WAL mode needs a real file,
// not :memory:) and returns it along with the bootstrap admin user's ID (always 1 — Migrate
// creates it when no users exist).
func newTestDB(t *testing.T) (*DB, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	d, err := New(path, key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return d, 1 // bootstrap admin
}

// seedAccountAndFolder creates a minimal IMAP account + INBOX folder for userID, returning
// their IDs.
func seedAccountAndFolder(t *testing.T, d *DB, userID int64) (accountID, folderID int64) {
	t.Helper()
	acc := &models.EmailAccount{
		UserID: userID, Provider: models.ProviderIMAPSMTP,
		EmailAddress: "user@example.com", DisplayName: "Test User",
		IMAPHost: "imap.example.com", IMAPPort: 993,
		SMTPHost: "smtp.example.com", SMTPPort: 587,
		Color: "#4A90D9",
	}
	if err := d.CreateAccount(acc); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := d.UpsertFolder(&models.Folder{AccountID: acc.ID, Name: "INBOX", FullPath: "INBOX", FolderType: "inbox"}); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	f, err := d.GetFolderByPath(acc.ID, "INBOX")
	if err != nil || f == nil {
		t.Fatalf("GetFolderByPath: %v", err)
	}
	return acc.ID, f.ID
}

func seedMessage(t *testing.T, d *DB, accountID, folderID int64, remoteUID, subject string) int64 {
	t.Helper()
	m := &models.Message{
		AccountID: accountID, FolderID: folderID, RemoteUID: remoteUID,
		Subject: subject, FromName: "Sender Name", FromEmail: "sender@example.com",
		ToList: "user@example.com", BodyText: "hello world", Date: time.Now(),
	}
	if err := d.UpsertMessage(m); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}
	if m.ID == 0 {
		t.Fatalf("UpsertMessage did not populate ID")
	}
	return m.ID
}

// ---- Encryption round-trip ----

func TestMessageEncryptionRoundTrip(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	const subject = `Subject with "quotes", unicode ✉️ and a semicolon; and a % sign`
	msgID := seedMessage(t, d, accountID, folderID, "100", subject)

	got, err := d.GetMessage(msgID, userID)
	if err != nil || got == nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Subject != subject {
		t.Errorf("Subject = %q, want %q", got.Subject, subject)
	}
	if got.FromEmail != "sender@example.com" {
		t.Errorf("FromEmail = %q", got.FromEmail)
	}
}

func TestGetMessage_WrongUserScoped(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "100", "secret")

	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := d.GetMessage(msgID, other.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for another user's message, got %+v", got)
	}
}

// ---- ListMessages / snooze filtering ----

func TestListMessages_ExcludesFutureSnoozed(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	visibleID := seedMessage(t, d, accountID, folderID, "1", "visible")
	snoozedID := seedMessage(t, d, accountID, folderID, "2", "snoozed")

	if err := d.SnoozeMessage(snoozedID, userID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}

	page, err := d.ListMessages(userID, []int64{folderID}, 0, 1, 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d, want 1 (snoozed message should be excluded)", page.Total)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != visibleID {
		t.Fatalf("Messages = %+v, want only %d", page.Messages, visibleID)
	}
}

func TestListMessages_IncludesPastSnoozed(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "was snoozed")

	if err := d.SnoozeMessage(msgID, userID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}
	// Simulate the snooze having already expired (SnoozeMessage validates nothing server-side
	// about "future", so write an already-past timestamp directly).
	if _, err := d.sql.Exec(`UPDATE messages SET snoozed_until=? WHERE id=?`,
		time.Now().Add(-time.Hour).UTC().Format("2006-01-02 15:04:05"), msgID); err != nil {
		t.Fatalf("backdate snooze: %v", err)
	}

	page, err := d.ListMessages(userID, []int64{folderID}, 0, 1, 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d, want 1 (past-snooze message should be visible again)", page.Total)
	}
}

// ---- Snooze / unsnooze / wake ----

func TestSnoozeUnsnoozeRoundTrip(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "snooze me")

	until := time.Now().Add(2 * time.Hour)
	if err := d.SnoozeMessage(msgID, userID, until); err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}
	snoozed, err := d.ListSnoozedMessages(userID, 1, 50)
	if err != nil {
		t.Fatalf("ListSnoozedMessages: %v", err)
	}
	if snoozed.Total != 1 || snoozed.Messages[0].ID != msgID {
		t.Fatalf("ListSnoozedMessages = %+v, want [%d]", snoozed.Messages, msgID)
	}
	if snoozed.Messages[0].SnoozedUntil == nil {
		t.Fatalf("SnoozedUntil not populated")
	}

	if err := d.UnsnoozeMessage(msgID, userID); err != nil {
		t.Fatalf("UnsnoozeMessage: %v", err)
	}
	snoozed, err = d.ListSnoozedMessages(userID, 1, 50)
	if err != nil {
		t.Fatalf("ListSnoozedMessages after unsnooze: %v", err)
	}
	if snoozed.Total != 0 {
		t.Fatalf("Total = %d after unsnooze, want 0", snoozed.Total)
	}
}

func TestSnoozeMessage_WrongUserScoped(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "not yours")

	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Attempting to snooze someone else's message must be a silent no-op (0 rows affected),
	// not an error and not a mutation.
	if err := d.SnoozeMessage(msgID, other.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SnoozeMessage (other user): %v", err)
	}
	msg, err := d.GetMessage(msgID, userID)
	if err != nil || msg == nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.SnoozedUntil != nil {
		t.Errorf("message got snoozed by a non-owning user: %+v", msg.SnoozedUntil)
	}
}

func TestWakeExpiredSnoozes(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	expiredID := seedMessage(t, d, accountID, folderID, "1", "expired")
	futureID := seedMessage(t, d, accountID, folderID, "2", "future")

	if err := d.SnoozeMessage(expiredID, userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}
	if _, err := d.sql.Exec(`UPDATE messages SET snoozed_until=? WHERE id=?`,
		time.Now().Add(-time.Hour).UTC().Format("2006-01-02 15:04:05"), expiredID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := d.SnoozeMessage(futureID, userID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}
	// Mark both read=0 initially is already the UpsertMessage default; flip expired one to
	// read=1 so we can prove WakeExpiredSnoozes resets it to unread.
	if _, err := d.sql.Exec(`UPDATE messages SET is_read=1 WHERE id=?`, expiredID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	folderIDs, err := d.WakeExpiredSnoozes()
	if err != nil {
		t.Fatalf("WakeExpiredSnoozes: %v", err)
	}
	if len(folderIDs) != 1 || folderIDs[0] != folderID {
		t.Fatalf("folderIDs = %v, want [%d]", folderIDs, folderID)
	}

	expired, err := d.GetMessage(expiredID, userID)
	if err != nil || expired == nil {
		t.Fatalf("GetMessage(expired): %v", err)
	}
	if expired.SnoozedUntil != nil {
		t.Errorf("expired message still snoozed: %+v", expired.SnoozedUntil)
	}
	if expired.IsRead {
		t.Errorf("expired message should be marked unread on wake")
	}

	// GetMessage doesn't project snoozed_until (only the Snoozed-view listing does), so check
	// the future message is still excluded from the normal folder listing instead.
	page, err := d.ListMessages(userID, []int64{folderID}, 0, 1, 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, m := range page.Messages {
		if m.ID == futureID {
			t.Errorf("future-snoozed message reappeared in folder listing after wake sweep")
		}
	}
}

// ---- Scheduled sends ----

func TestScheduledSendRoundTrip(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, _ := seedAccountAndFolder(t, d, userID)

	s := &models.ScheduledSend{
		UserID: userID, AccountID: accountID,
		To: []string{"a@example.com", "b@example.com"},
		CC: []string{"c@example.com"},
		Subject: `Meeting notes — "Q3 review"`, BodyHTML: "<p>hi</p>", BodyText: "hi",
		ForwardFromIDs: []int64{42},
		SendAt:         time.Now().Add(time.Hour),
	}
	id, err := d.CreateScheduledSend(s)
	if err != nil {
		t.Fatalf("CreateScheduledSend: %v", err)
	}
	if id == 0 {
		t.Fatalf("CreateScheduledSend returned id=0")
	}

	list, err := d.ListScheduledSends(userID)
	if err != nil {
		t.Fatalf("ListScheduledSends: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListScheduledSends returned %d items, want 1", len(list))
	}
	got := list[0]
	if got.Subject != s.Subject {
		t.Errorf("Subject = %q, want %q", got.Subject, s.Subject)
	}
	if len(got.To) != 2 || got.To[0] != "a@example.com" || got.To[1] != "b@example.com" {
		t.Errorf("To = %v", got.To)
	}
	if len(got.CC) != 1 || got.CC[0] != "c@example.com" {
		t.Errorf("CC = %v", got.CC)
	}
	if len(got.ForwardFromIDs) != 1 || got.ForwardFromIDs[0] != 42 {
		t.Errorf("ForwardFromIDs = %v", got.ForwardFromIDs)
	}

	// Not due yet (send_at is an hour out).
	due, err := d.ListDueScheduledSends()
	if err != nil {
		t.Fatalf("ListDueScheduledSends: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("ListDueScheduledSends = %d items, want 0 (not due yet)", len(due))
	}

	if err := d.DeleteScheduledSend(id, userID); err != nil {
		t.Fatalf("DeleteScheduledSend: %v", err)
	}
	list, err = d.ListScheduledSends(userID)
	if err != nil {
		t.Fatalf("ListScheduledSends after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListScheduledSends after delete = %d, want 0", len(list))
	}
}

func TestListDueScheduledSends(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, _ := seedAccountAndFolder(t, d, userID)

	dueID, err := d.CreateScheduledSend(&models.ScheduledSend{
		UserID: userID, AccountID: accountID, To: []string{"a@example.com"},
		Subject: "due", SendAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateScheduledSend: %v", err)
	}
	// Backdate it into the past so it's due.
	if _, err := d.sql.Exec(`UPDATE scheduled_sends SET send_at=? WHERE id=?`,
		time.Now().Add(-time.Minute).UTC().Format("2006-01-02 15:04:05"), dueID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := d.CreateScheduledSend(&models.ScheduledSend{
		UserID: userID, AccountID: accountID, To: []string{"a@example.com"},
		Subject: "not due", SendAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateScheduledSend: %v", err)
	}

	due, err := d.ListDueScheduledSends()
	if err != nil {
		t.Fatalf("ListDueScheduledSends: %v", err)
	}
	if len(due) != 1 || due[0].ID != dueID {
		t.Fatalf("ListDueScheduledSends = %+v, want only id=%d", due, dueID)
	}
}

func TestDeleteScheduledSend_WrongUserScoped(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, _ := seedAccountAndFolder(t, d, userID)
	id, err := d.CreateScheduledSend(&models.ScheduledSend{
		UserID: userID, AccountID: accountID, To: []string{"a@example.com"},
		Subject: "mine", SendAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateScheduledSend: %v", err)
	}
	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := d.DeleteScheduledSend(id, other.ID); err != nil {
		t.Fatalf("DeleteScheduledSend: %v", err)
	}
	list, err := d.ListScheduledSends(userID)
	if err != nil {
		t.Fatalf("ListScheduledSends: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("scheduled send was deleted by a non-owning user; list = %+v", list)
	}
}

// ---- Labels ----

func TestLabelCRUDAndAssignment(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "label me")

	// userID (the bootstrap admin) already has the 4 seeded default labels — use a name that
	// doesn't collide with those ("Important", "Personal", "Work", "ToDo").
	baseline, err := d.ListLabels(userID)
	if err != nil {
		t.Fatalf("ListLabels (baseline): %v", err)
	}
	label, err := d.CreateLabel(userID, "Project Zeta", "#e74c3c")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if label.ID == 0 {
		t.Fatalf("CreateLabel returned id=0")
	}

	if _, err := d.CreateLabel(userID, "Project Zeta", "#000000"); err == nil {
		t.Errorf("expected duplicate label name to fail")
	}

	if err := d.AssignLabel(msgID, label.ID, userID); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	msg, err := d.GetMessage(msgID, userID)
	if err != nil || msg == nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if len(msg.Labels) != 1 || msg.Labels[0].ID != label.ID {
		t.Fatalf("Labels = %+v, want [%d]", msg.Labels, label.ID)
	}

	if err := d.UpdateLabel(label.ID, userID, "Project Zeta Renamed", "#ff0000"); err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}
	labels, err := d.ListLabels(userID)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != len(baseline)+1 {
		t.Fatalf("ListLabels = %+v, want %d entries", labels, len(baseline)+1)
	}
	found := false
	for _, l := range labels {
		if l.ID == label.ID {
			found = true
			if l.Name != "Project Zeta Renamed" {
				t.Errorf("renamed label Name = %q", l.Name)
			}
		}
	}
	if !found {
		t.Fatalf("renamed label not found in ListLabels: %+v", labels)
	}

	if err := d.UnassignLabel(msgID, label.ID, userID); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	msg, err = d.GetMessage(msgID, userID)
	if err != nil || msg == nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if len(msg.Labels) != 0 {
		t.Fatalf("Labels after unassign = %+v, want none", msg.Labels)
	}

	if err := d.DeleteLabel(label.ID, userID); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}
	labels, err = d.ListLabels(userID)
	if err != nil {
		t.Fatalf("ListLabels after delete: %v", err)
	}
	if len(labels) != len(baseline) {
		t.Fatalf("ListLabels after delete = %+v, want back to baseline %+v", labels, baseline)
	}
}

func TestAssignLabel_CannotCrossUserBoundary(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "protected")

	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	label, err := d.CreateLabel(other.ID, "Bob's label", "#123456")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	// Bob tries to label userID's message with his own label — must be a no-op.
	if err := d.AssignLabel(msgID, label.ID, other.ID); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	msg, err := d.GetMessage(msgID, userID)
	if err != nil || msg == nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if len(msg.Labels) != 0 {
		t.Errorf("cross-user label assignment succeeded: %+v", msg.Labels)
	}
}

// ---- Folder export support ----

func TestListMessageIDsByFolder(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	id1 := seedMessage(t, d, accountID, folderID, "1", "one")
	id2 := seedMessage(t, d, accountID, folderID, "2", "two")

	ids, err := d.ListMessageIDsByFolder(folderID, userID)
	if err != nil {
		t.Fatalf("ListMessageIDsByFolder: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2 entries", ids)
	}
	got := map[int64]bool{ids[0]: true, ids[1]: true}
	if !got[id1] || !got[id2] {
		t.Errorf("ids = %v, want %d and %d", ids, id1, id2)
	}
}

func TestListMessageIDsByFolder_WrongUserScoped(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	seedMessage(t, d, accountID, folderID, "1", "not yours")

	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ids, err := d.ListMessageIDsByFolder(folderID, other.ID)
	if err != nil {
		t.Fatalf("ListMessageIDsByFolder: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("non-owning user got message IDs from another user's folder: %v", ids)
	}
}

// ---- Delete / star (existing behavior, previously untested) ----

func TestDeleteMessage(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "delete me")

	if err := d.DeleteMessage(msgID, userID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	msg, err := d.GetMessage(msgID, userID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg != nil {
		t.Errorf("message still present after delete: %+v", msg)
	}
}

func TestToggleMessageStar(t *testing.T) {
	d, userID := newTestDB(t)
	accountID, folderID := seedAccountAndFolder(t, d, userID)
	msgID := seedMessage(t, d, accountID, folderID, "1", "star me")

	starred, err := d.ToggleMessageStar(msgID, userID)
	if err != nil {
		t.Fatalf("ToggleMessageStar: %v", err)
	}
	if !starred {
		t.Errorf("expected starred=true after first toggle")
	}
	starred, err = d.ToggleMessageStar(msgID, userID)
	if err != nil {
		t.Fatalf("ToggleMessageStar: %v", err)
	}
	if starred {
		t.Errorf("expected starred=false after second toggle")
	}
}

func TestCalendarsAndEventFilter(t *testing.T) {
	d, uid := newTestDB(t)
	accID, _ := seedAccountAndFolder(t, d, uid)

	if err := d.SeedCalendars(uid); err != nil {
		t.Fatalf("SeedCalendars: %v", err)
	}
	cals, err := d.ListCalendars(uid)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("expected 2 seeded calendars, got %d", len(cals))
	}
	var generalID, accCalID int64
	for _, c := range cals {
		if c.AccountID == nil {
			if c.Name != "General" {
				t.Fatalf("account-less calendar should be General, got %q", c.Name)
			}
			generalID = c.ID
		} else if *c.AccountID == accID {
			accCalID = c.ID
		}
	}
	if generalID == 0 || accCalID == 0 {
		t.Fatalf("missing seeded calendars: general=%d acc=%d", generalID, accCalID)
	}

	// Seed is idempotent
	if err := d.SeedCalendars(uid); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if cals, _ = d.ListCalendars(uid); len(cals) != 2 {
		t.Fatalf("seed not idempotent: %d calendars", len(cals))
	}

	// Legacy event (no calendar_id) gets repointed to its account calendar
	legacy := &models.CalendarEvent{
		UserID: uid, AccountID: &accID, Title: "legacy",
		StartTime: "2026-11-05 09:00", EndTime: "2026-11-05 10:00", Status: "confirmed",
	}
	if err := d.UpsertCalendarEvent(legacy); err != nil {
		t.Fatalf("UpsertCalendarEvent: %v", err)
	}
	if err := d.SeedCalendars(uid); err != nil {
		t.Fatalf("re-seed after event: %v", err)
	}
	got, err := d.GetCalendarEvent(legacy.ID, uid)
	if err != nil || got == nil {
		t.Fatalf("GetCalendarEvent: %v", err)
	}
	if got.CalendarID != accCalID {
		t.Fatalf("legacy event not repointed: cal=%d want %d", got.CalendarID, accCalID)
	}

	one := &models.CalendarEvent{
		UserID: uid, CalendarID: generalID, Title: "on general",
		StartTime: "2026-11-10 09:00", EndTime: "2026-11-10 10:00", Status: "confirmed",
	}
	two := &models.CalendarEvent{
		UserID: uid, AccountID: &accID, CalendarID: accCalID, Title: "on account",
		StartTime: "2026-11-11 09:00", EndTime: "2026-11-11 10:00", Status: "confirmed",
	}
	if err := d.UpsertCalendarEvent(one); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := d.UpsertCalendarEvent(two); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	from, to := "2026-11-01", "2026-11-30"
	all, err := d.ListCalendarEvents(uid, from, to, nil)
	if err != nil {
		t.Fatalf("ListCalendarEvents: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	filtered, err := d.ListCalendarEvents(uid, from, to, []int64{generalID})
	if err != nil {
		t.Fatalf("ListCalendarEvents filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].CalendarID != generalID {
		t.Fatalf("filter returned wrong events: %+v", filtered)
	}
}

func TestCalendarCRUD(t *testing.T) {
	d, uid := newTestDB(t)
	accID, _ := seedAccountAndFolder(t, d, uid)
	if err := d.SeedCalendars(uid); err != nil {
		t.Fatalf("SeedCalendars: %v", err)
	}

	c := &models.Calendar{UserID: uid, Name: "Work", Color: "#EA4335", AccountID: &accID}
	if err := d.CreateCalendar(c); err != nil {
		t.Fatalf("CreateCalendar: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("CreateCalendar did not set ID")
	}
	got, err := d.GetCalendar(c.ID, uid)
	if err != nil || got == nil || got.Name != "Work" {
		t.Fatalf("GetCalendar: %+v %v", got, err)
	}

	got.Name = "Personal"
	if err := d.UpdateCalendar(got); err != nil {
		t.Fatalf("UpdateCalendar: %v", err)
	}
	got2, _ := d.GetCalendar(c.ID, uid)
	if got2.Name != "Personal" {
		t.Fatalf("update not persisted: %q", got2.Name)
	}

	// Event on the calendar must be repointed to General on delete
	ev := &models.CalendarEvent{UserID: uid, AccountID: &accID, CalendarID: c.ID, Title: "work ev",
		StartTime: "2026-11-15 09:00", EndTime: "2026-11-15 10:00", Status: "confirmed"}
	if err := d.UpsertCalendarEvent(ev); err != nil {
		t.Fatalf("UpsertCalendarEvent: %v", err)
	}
	if err := d.DeleteCalendar(c.ID, uid); err != nil {
		t.Fatalf("DeleteCalendar: %v", err)
	}
	if _, err := d.GetCalendar(c.ID, uid); err == nil {
		t.Fatal("calendar still exists after delete")
	}
	evs, _ := d.ListCalendarEvents(uid, "2026-11-01", "2026-11-30", nil)
	if len(evs) != 1 || evs[0].CalendarID == c.ID {
		t.Fatalf("event not repointed to General: %+v", evs)
	}
}
