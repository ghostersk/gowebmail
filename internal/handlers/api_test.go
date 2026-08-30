package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
)

// newTestHandler builds an APIHandler backed by a fresh, migrated temp-file DB, with no
// syncer/cfg — sufficient for the local-only handlers under test here (Labels, Snooze,
// Send-later, Folder export), none of which touch IMAP/Graph/JMAP or config.
func newTestHandler(t *testing.T) (*APIHandler, *db.DB, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	d, err := db.New(path, key)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &APIHandler{db: d}, d, 1 // bootstrap admin
}

func seedTestAccountAndFolder(t *testing.T, d *db.DB, userID int64) (accountID, folderID int64) {
	t.Helper()
	acc := &models.EmailAccount{
		UserID: userID, Provider: models.ProviderIMAPSMTP,
		EmailAddress: "user@example.com", DisplayName: "Test User", Color: "#4A90D9",
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

func seedTestMessage(t *testing.T, d *db.DB, accountID, folderID int64, remoteUID, subject string) int64 {
	t.Helper()
	m := &models.Message{
		AccountID: accountID, FolderID: folderID, RemoteUID: remoteUID,
		Subject: subject, FromName: "Sender", FromEmail: "sender@example.com",
		ToList: "user@example.com", BodyText: "hello", Date: time.Now(),
	}
	if err := d.UpsertMessage(m); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}
	return m.ID
}

// authedRequest builds a request carrying userID the way RequireAuth middleware would (via
// context), with mux path vars set directly (bypassing the router) and an optional JSON body.
func authedRequest(t *testing.T, method, target string, userID int64, vars map[string]string, body interface{}) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = httptest.NewRequest(method, target, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, userID)
	r = r.WithContext(ctx)
	if vars != nil {
		r = mux.SetURLVars(r, vars)
	}
	return r
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

// ---- Labels ----

func TestCreateAndListLabels(t *testing.T) {
	h, d, userID := newTestHandler(t)
	baseline, err := d.ListLabels(userID)
	if err != nil {
		t.Fatalf("ListLabels (baseline): %v", err)
	}

	rec := httptest.NewRecorder()
	h.CreateLabel(rec, authedRequest(t, "POST", "/api/labels", userID, nil, map[string]string{"Name": "Project Zeta", "Color": "#abcdef"}))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("CreateLabel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created models.Label
	decodeJSON(t, rec, &created)
	if created.ID == 0 || created.Name != "Project Zeta" {
		t.Fatalf("created label = %+v", created)
	}

	rec = httptest.NewRecorder()
	h.ListLabels(rec, authedRequest(t, "GET", "/api/labels", userID, nil, nil))
	var labels []models.Label
	decodeJSON(t, rec, &labels)
	if len(labels) != len(baseline)+1 {
		t.Fatalf("ListLabels = %+v, want %d entries", labels, len(baseline)+1)
	}
}

func TestCreateLabel_MissingFields(t *testing.T) {
	h, _, userID := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.CreateLabel(rec, authedRequest(t, "POST", "/api/labels", userID, nil, map[string]string{"Name": "", "Color": "#fff"}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// ---- Snooze ----

func TestSnoozeMessage_Handler(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, folderID := seedTestAccountAndFolder(t, d, userID)
	msgID := seedTestMessage(t, d, accountID, folderID, "1", "snooze via handler")

	until := time.Now().Add(time.Hour).Format(time.RFC3339)
	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(msgID)}
	h.SnoozeMessage(rec, authedRequest(t, "PUT", "/api/messages/"+itoa(msgID)+"/snooze", userID, vars, map[string]string{"until": until}))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("SnoozeMessage status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.SnoozedMessages(rec, authedRequest(t, "GET", "/api/messages/snoozed", userID, nil, nil))
	var page models.PagedMessages
	decodeJSON(t, rec, &page)
	if page.Total != 1 || len(page.Messages) != 1 || page.Messages[0].ID != msgID {
		t.Fatalf("SnoozedMessages = %+v", page)
	}

	rec = httptest.NewRecorder()
	h.UnsnoozeMessage(rec, authedRequest(t, "DELETE", "/api/messages/"+itoa(msgID)+"/snooze", userID, vars, nil))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("UnsnoozeMessage status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.SnoozedMessages(rec, authedRequest(t, "GET", "/api/messages/snoozed", userID, nil, nil))
	decodeJSON(t, rec, &page)
	if page.Total != 0 {
		t.Fatalf("SnoozedMessages after unsnooze = %+v, want empty", page)
	}
}

func TestSnoozeMessage_RejectsMissingUntil(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, folderID := seedTestAccountAndFolder(t, d, userID)
	msgID := seedTestMessage(t, d, accountID, folderID, "1", "no until")

	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(msgID)}
	h.SnoozeMessage(rec, authedRequest(t, "PUT", "/api/messages/"+itoa(msgID)+"/snooze", userID, vars, map[string]string{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- Send-later ----

func TestCreateScheduledSend_RejectsPastDate(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, _ := seedTestAccountAndFolder(t, d, userID)

	body := map[string]interface{}{
		"account_id": accountID, "to": []string{"a@example.com"},
		"subject": "hi", "send_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
	}
	rec := httptest.NewRecorder()
	h.CreateScheduledSend(rec, authedRequest(t, "POST", "/api/send-later", userID, nil, body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateScheduledSend_RejectsFileAttachments(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, _ := seedTestAccountAndFolder(t, d, userID)

	body := map[string]interface{}{
		"account_id": accountID, "to": []string{"a@example.com"},
		"subject": "hi", "send_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		"attachments": []map[string]string{{"filename": "x.pdf", "content_type": "application/pdf"}},
	}
	rec := httptest.NewRecorder()
	h.CreateScheduledSend(rec, authedRequest(t, "POST", "/api/send-later", userID, nil, body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestScheduledSend_CreateListCancel(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, _ := seedTestAccountAndFolder(t, d, userID)

	body := map[string]interface{}{
		"account_id": accountID, "to": []string{"a@example.com"},
		"subject": "Scheduled", "send_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	rec := httptest.NewRecorder()
	h.CreateScheduledSend(rec, authedRequest(t, "POST", "/api/send-later", userID, nil, body))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("CreateScheduledSend status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	decodeJSON(t, rec, &created)
	if !created.OK || created.ID == 0 {
		t.Fatalf("CreateScheduledSend result = %+v", created)
	}

	rec = httptest.NewRecorder()
	h.ListScheduledSends(rec, authedRequest(t, "GET", "/api/scheduled-sends", userID, nil, nil))
	var list []models.ScheduledSend
	decodeJSON(t, rec, &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListScheduledSends = %+v", list)
	}

	rec = httptest.NewRecorder()
	vars := map[string]string{"id": itoa(created.ID)}
	h.CancelScheduledSend(rec, authedRequest(t, "DELETE", "/api/scheduled-sends/"+itoa(created.ID), userID, vars, nil))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("CancelScheduledSend status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ListScheduledSends(rec, authedRequest(t, "GET", "/api/scheduled-sends", userID, nil, nil))
	decodeJSON(t, rec, &list)
	if len(list) != 0 {
		t.Fatalf("ListScheduledSends after cancel = %+v, want empty", list)
	}
}

// ---- Folder export ----

func TestExportFolder_Zip(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, folderID := seedTestAccountAndFolder(t, d, userID)
	seedTestMessage(t, d, accountID, folderID, "1", "one")
	seedTestMessage(t, d, accountID, folderID, "2", "two")

	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(folderID)}
	target := "/api/folders/" + itoa(folderID) + "/export?format=zip"
	h.ExportFolder(rec, authedRequest(t, "GET", target, userID, vars, nil))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("ExportFolder status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.Bytes()
	if len(body) < 2 || string(body[:2]) != "PK" {
		t.Errorf("body doesn't look like a zip (got %d bytes, prefix %q)", len(body), body[:min(4, len(body))])
	}
}

func TestExportFolder_Mbox(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, folderID := seedTestAccountAndFolder(t, d, userID)
	seedTestMessage(t, d, accountID, folderID, "1", "one")
	seedTestMessage(t, d, accountID, folderID, "2", "two")

	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(folderID)}
	target := "/api/folders/" + itoa(folderID) + "/export?format=mbox"
	h.ExportFolder(rec, authedRequest(t, "GET", target, userID, vars, nil))
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("ExportFolder status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/mbox" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	count := bytesCount(body, "From MAILER-DAEMON")
	if count != 2 {
		t.Errorf("mbox has %d envelope lines, want 2; body:\n%s", count, body)
	}
}

func TestExportFolder_EmptyFolderRejected(t *testing.T) {
	h, d, userID := newTestHandler(t)
	_, folderID := seedTestAccountAndFolder(t, d, userID)

	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(folderID)}
	h.ExportFolder(rec, authedRequest(t, "GET", "/api/folders/"+itoa(folderID)+"/export", userID, vars, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestExportFolder_WrongUserScoped(t *testing.T) {
	h, d, userID := newTestHandler(t)
	accountID, folderID := seedTestAccountAndFolder(t, d, userID)
	seedTestMessage(t, d, accountID, folderID, "1", "not yours")

	other, err := d.CreateUser("bob", "bob@example.com", "password123", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rec := httptest.NewRecorder()
	vars := map[string]string{"id": itoa(folderID)}
	h.ExportFolder(rec, authedRequest(t, "GET", "/api/folders/"+itoa(folderID)+"/export", other.ID, vars, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-owning user's export status = %d, want %d (folder empty for them); body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// ---- small local helpers ----

func itoa(id int64) string {
	if id == 0 {
		return "0"
	}
	neg := id < 0
	if neg {
		id = -id
	}
	var buf [20]byte
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = byte('0' + id%10)
		id /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func bytesCount(s, substr string) int {
	count := 0
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			count++
			i += len(substr) - 1
		}
	}
	return count
}
