package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
)

// ======== Contacts ========

func (h *APIHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var contacts interface{}
	var err error
	if q != "" {
		contacts, err = h.db.SearchContacts(userID, q)
	} else {
		contacts, err = h.db.ListContacts(userID)
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list contacts")
		return
	}
	if contacts == nil {
		contacts = []*models.Contact{}
	}
	h.writeJSON(w, contacts)
}

func (h *APIHandler) GetContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	c, err := h.db.GetContact(id, userID)
	if err != nil || c == nil {
		h.writeError(w, http.StatusNotFound, "contact not found")
		return
	}
	h.writeJSON(w, c)
}

func (h *APIHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req models.Contact
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.UserID = userID
	if req.AvatarColor == "" {
		colors := []string{"#6b7280", "#0078D4", "#EA4335", "#34A853", "#FBBC04", "#9C27B0", "#FF6D00"}
		req.AvatarColor = colors[int(userID)%len(colors)]
	}
	if err := h.db.CreateContact(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create contact")
		return
	}
	h.writeJSON(w, req)
}

func (h *APIHandler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	var req models.Contact
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.ID = id
	if err := h.db.UpdateContact(&req, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update contact")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeleteContact(id, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ======== Calendar Events ========

func (h *APIHandler) ListCalendarEvents(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" {
		from = time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().AddDate(0, 3, 0).Format("2006-01-02")
	}
	if err := h.db.SeedCalendars(userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to seed calendars")
		return
	}
	var calendarIDs []int64
	if s := r.URL.Query().Get("calendar_ids"); s != "" {
		for _, p := range strings.Split(s, ",") {
			if id, err := strconv.ParseInt(p, 10, 64); err == nil {
				calendarIDs = append(calendarIDs, id)
			}
		}
	}
	events, err := h.db.ListCalendarEvents(userID, from, to, calendarIDs)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	if events == nil {
		events = []*models.CalendarEvent{}
	}
	h.writeJSON(w, events)
}

func (h *APIHandler) GetCalendarEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	ev, err := h.db.GetCalendarEvent(id, userID)
	if err != nil || ev == nil {
		h.writeError(w, http.StatusNotFound, "event not found")
		return
	}
	h.writeJSON(w, ev)
}

func (h *APIHandler) CreateCalendarEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req models.CalendarEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.UserID = userID
	if err := h.db.SeedCalendars(userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to seed calendars")
		return
	}
	if err := h.db.UpsertCalendarEvent(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create event")
		return
	}
	h.writeJSON(w, req)
}

func (h *APIHandler) UpdateCalendarEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	existing, err := h.db.GetCalendarEvent(id, userID)
	if err != nil || existing == nil {
		h.writeError(w, http.StatusNotFound, "event not found")
		return
	}
	var req models.CalendarEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.ID = id
	req.UserID = userID
	req.UID = existing.UID // preserve original UID
	if err := h.db.SeedCalendars(userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to seed calendars")
		return
	}
	if err := h.db.UpsertCalendarEvent(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update event")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) DeleteCalendarEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeleteCalendarEvent(id, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete event")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ======== Calendars ========

func (h *APIHandler) ListCalendars(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := h.db.SeedCalendars(userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to seed calendars")
		return
	}
	cals, err := h.db.ListCalendars(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list calendars")
		return
	}
	if cals == nil {
		cals = []*models.Calendar{}
	}
	h.writeJSON(w, cals)
}

func (h *APIHandler) CreateCalendar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req models.Calendar
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name required")
		return
	}
	req.UserID = userID
	if err := h.db.CreateCalendar(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create calendar")
		return
	}
	h.writeJSON(w, req)
}

func (h *APIHandler) UpdateCalendar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	existing, err := h.db.GetCalendar(id, userID)
	if err != nil || existing == nil {
		h.writeError(w, http.StatusNotFound, "calendar not found")
		return
	}
	var req models.Calendar
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.ID = id
	req.UserID = userID
	if req.Name == "" {
		req.Name = existing.Name
	}
	if err := h.db.UpdateCalendar(&req); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update calendar")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

func (h *APIHandler) DeleteCalendar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	existing, err := h.db.GetCalendar(id, userID)
	if err != nil || existing == nil {
		h.writeError(w, http.StatusNotFound, "calendar not found")
		return
	}
	// Only the seeded General calendar is protected; users may create
	// their own account-less custom calendars (form offers "None").
	if existing.AccountID == nil && existing.Name == "General" {
		h.writeError(w, http.StatusBadRequest, "cannot delete General")
		return
	}
	if err := h.db.DeleteCalendar(id, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete calendar")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// InviteCalendarEvent emails all of the event's attendees a single message with a
// METHOD:REQUEST .ics attachment, sent from the event's own account.
func (h *APIHandler) InviteCalendarEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	ev, err := h.db.GetCalendarEvent(id, userID)
	if err != nil || ev == nil {
		h.writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if ev.AccountID == nil {
		h.writeError(w, http.StatusBadRequest, "event has no account — assign it to an account's calendar")
		return
	}
	if ev.Attendees == "" {
		h.writeError(w, http.StatusBadRequest, "no attendees")
		return
	}
	account, err := h.db.GetAccount(*ev.AccountID)
	if err != nil || account == nil {
		h.writeError(w, http.StatusBadRequest, "account not found")
		return
	}
	ev.OrganizerEmail = account.EmailAddress
	ics := append([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//GoWebMail//EN\r\nMETHOD:REQUEST\r\n"), buildICSEvents([]*models.CalendarEvent{ev})...)
	ics = append(ics, []byte("END:VCALENDAR\r\n")...)
	req := &models.ComposeRequest{
		AccountID:   account.ID,
		To:          strings.Split(ev.Attendees, ","),
		Subject:     "Invitation: " + ev.Title,
		BodyText:    fmt.Sprintf("You're invited to %s\n\nWhen: %s – %s\nWhere: %s\n\nSent from GoWebMail", ev.Title, ev.StartTime, ev.EndTime, ev.Location),
		Attachments: []models.Attachment{{Filename: "invite.ics", ContentType: "text/calendar", Data: ics}},
	}
	if err := h.sendComposedMessage(userID, account, req, nil); err != nil {
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ======== CalDAV Tokens ========

func (h *APIHandler) ListCalDAVTokens(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	tokens, err := h.db.ListCalDAVTokens(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	if tokens == nil {
		tokens = []*models.CalDAVToken{}
	}
	h.writeJSON(w, tokens)
}

func (h *APIHandler) CreateCalDAVToken(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Label string `json:"label"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Label == "" {
		req.Label = "CalDAV token"
	}
	t, err := h.db.CreateCalDAVToken(userID, req.Label)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	h.writeJSON(w, t)
}

func (h *APIHandler) DeleteCalDAVToken(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeleteCalDAVToken(id, userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete token")
		return
	}
	h.writeJSON(w, map[string]bool{"ok": true})
}

// ======== CalDAV Server ========
// Serves a read-only iCalendar feed at /caldav/{token}/calendar.ics
// Compatible with any CalDAV client that supports basic calendar subscription.

func (h *APIHandler) ServeCalDAV(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	userID, err := h.db.GetUserByCalDAVToken(token)
	if err != nil || userID == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Fetch events for next 12 months + past 3 months
	from := time.Now().AddDate(0, -3, 0).Format("2006-01-02")
	to := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	events, err := h.db.ListCalendarEvents(userID, from, to, nil)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gowebmail.ics"`)

	fmt.Fprintf(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//GoWebMail//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\nX-WR-CALNAME:GoWebMail\r\n")
	w.Write(buildICSEvents(events))
	fmt.Fprintf(w, "END:VCALENDAR\r\n")
}

// buildICSEvents serializes events as VEVENT blocks only (no VCALENDAR
// header/footer) so callers can reuse them with any METHOD.
func buildICSEvents(events []*models.CalendarEvent) []byte {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString("UID:" + escICAL(ev.UID) + "\r\n")
		b.WriteString("SUMMARY:" + escICAL(ev.Title) + "\r\n")
		if ev.Description != "" {
			b.WriteString("DESCRIPTION:" + escICAL(ev.Description) + "\r\n")
		}
		if ev.Location != "" {
			b.WriteString("LOCATION:" + escICAL(ev.Location) + "\r\n")
		}
		if ev.AllDay {
			// All-day events use DATE format
			start := strings.ReplaceAll(strings.Split(ev.StartTime, "T")[0], "-", "")
			end := strings.ReplaceAll(strings.Split(ev.EndTime, "T")[0], "-", "")
			b.WriteString("DTSTART;VALUE=DATE:" + start + "\r\n")
			b.WriteString("DTEND;VALUE=DATE:" + end + "\r\n")
		} else {
			b.WriteString("DTSTART:" + toICALDate(ev.StartTime) + "\r\n")
			b.WriteString("DTEND:" + toICALDate(ev.EndTime) + "\r\n")
		}
		if ev.OrganizerEmail != "" {
			b.WriteString("ORGANIZER:mailto:" + ev.OrganizerEmail + "\r\n")
		}
		if ev.Status != "" {
			b.WriteString("STATUS:" + strings.ToUpper(ev.Status) + "\r\n")
		}
		if ev.RecurrenceRule != "" {
			b.WriteString("RRULE:" + ev.RecurrenceRule + "\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	return []byte(b.String())
}

// ExportCalendarICS downloads the user's events as a .ics file, optionally
// filtered by calendar_ids (empty = all).
func (h *APIHandler) ExportCalendarICS(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" {
		from = time.Now().AddDate(0, -3, 0).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	}
	if err := h.db.SeedCalendars(userID); err != nil {
		http.Error(w, "failed to seed calendars", http.StatusInternalServerError)
		return
	}
	var calendarIDs []int64
	if s := r.URL.Query().Get("calendar_ids"); s != "" {
		for _, p := range strings.Split(s, ",") {
			if id, err := strconv.ParseInt(p, 10, 64); err == nil {
				calendarIDs = append(calendarIDs, id)
			}
		}
	}
	events, err := h.db.ListCalendarEvents(userID, from, to, calendarIDs)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gowebmail.ics"`)
	fmt.Fprintf(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//GoWebMail//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\nX-WR-CALNAME:GoWebMail\r\n")
	w.Write(buildICSEvents(events))
	fmt.Fprintf(w, "END:VCALENDAR\r\n")
}

// ImportCalendar parses a .ics body and upserts its events by UID.
func (h *APIHandler) ImportCalendar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var req struct {
		Text        string `json:"text"`
		CalendarID  int64  `json:"calendar_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		h.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := h.db.SeedCalendars(userID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to seed calendars")
		return
	}
	// Default to the user's General calendar when none given.
	if req.CalendarID == 0 {
		if cals, err := h.db.ListCalendars(userID); err == nil {
			for _, c := range cals {
				if c.AccountID == nil {
					req.CalendarID = c.ID
					break
				}
			}
		}
	}
	n := 0
	for _, ev := range parseICSEvents(req.Text) {
		ev.UserID = userID
		ev.CalendarID = req.CalendarID
		if ev.Status == "" {
			ev.Status = "confirmed"
		}
		if err := h.db.UpsertCalendarEvent(ev); err != nil {
			h.writeError(w, http.StatusInternalServerError, "failed to import event: "+err.Error())
			return
		}
		n++
	}
	h.writeJSON(w, map[string]int{"imported": n})
}

// parseICSEvents parses VEVENT blocks from iCal text.
func parseICSEvents(text string) []*models.CalendarEvent {
	// Unfold continuation lines (RFC 5545 §3.1: lines starting with space/tab).
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var unfolded []string
	for _, ln := range lines {
		if ln != "" && (ln[0] == ' ' || ln[0] == '\t') {
			if len(unfolded) > 0 {
				unfolded[len(unfolded)-1] += ln[1:]
				continue
			}
		}
		unfolded = append(unfolded, ln)
	}

	var out []*models.CalendarEvent
	var cur *models.CalendarEvent
	var attendees []string
	for _, ln := range unfolded {
		if ln == "BEGIN:VEVENT" {
			cur = &models.CalendarEvent{}
			attendees = nil
			continue
		}
		if ln == "END:VEVENT" {
			if cur != nil && cur.UID != "" {
				cur.Attendees = strings.Join(attendees, ",")
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		if cur == nil || !strings.Contains(ln, ":") {
			continue
		}
		key, val := splitICALLine(ln)
		k := strings.ToUpper(key)
		val = unescICAL(val)
		switch k {
		case "UID":
			cur.UID = val
		case "SUMMARY":
			cur.Title = val
		case "DESCRIPTION":
			cur.Description = val
		case "LOCATION":
			cur.Location = val
		case "DTSTART", "DTEND":
			if strings.Contains(ln, "VALUE=DATE") {
				cur.AllDay = true
			}
			s := val
			if len(s) == 8 {
				s = icalDatePart(s) + " 00:00:00"
			} else if idx := strings.Index(s, "T"); idx > 0 {
				s = icalDatePart(s[:idx]) + " " + icalHM(s[idx+1:])
			}
			if k == "DTSTART" {
				cur.StartTime = s
			} else {
				cur.EndTime = s
			}
		case "RRULE":
			cur.RecurrenceRule = val
		case "STATUS":
			cur.Status = strings.ToLower(val)
		case "ORGANIZER":
			cur.OrganizerEmail = strings.TrimPrefix(val, "mailto:")
		case "ATTENDEE":
			a := strings.TrimPrefix(val, "mailto:")
			if a != "" {
				attendees = append(attendees, a)
			}
		}
	}
	return out
}

// splitICALLine splits "KEY;PARAM=val:value" into key (upper) and value.
func splitICALLine(ln string) (string, string) {
	i := strings.Index(ln, ":")
	head, val := ln[:i], ln[i+1:]
	if j := strings.Index(head, ";"); j > 0 {
		return head[:j], val
	}
	return head, val
}

// icalDatePart converts "20261105" to "2026-11-05".
func icalDatePart(s string) string {
	if len(s) == 8 {
		return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
	}
	return s
}

// icalHM converts "150405" / "1504" / "150405Z" to "15:04:05".
func icalHM(s string) string {
	s = strings.TrimRight(s, "Z")
	if len(s) == 6 {
		return s[0:2] + ":" + s[2:4] + ":" + s[4:6]
	}
	if len(s) == 4 {
		return s[0:2] + ":" + s[2:4] + ":00"
	}
	return s
}

func unescICAL(s string) string {
	return strings.NewReplacer(
		`\\`, `\`,
		`\n`, "\n",
		`\;`, ";",
		`\,`+",", ",",
		`\t`, "\t",
	).Replace(s)
}

func escICAL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	// Fold long lines at 75 chars
	if len(s) > 70 {
		var out strings.Builder
		for i, ch := range s {
			if i > 0 && i%70 == 0 {
				out.WriteString("\r\n ")
			}
			out.WriteRune(ch)
		}
		return out.String()
	}
	return s
}

func toICALDate(s string) string {
	// Convert "2006-01-02T15:04:05Z" or "2006-01-02 15:04:05" to "20060102T150405Z"
	t, err := time.Parse("2006-01-02T15:04:05Z07:00", s)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", s)
	}
	if err != nil {
		return strings.NewReplacer("-", "", ":", "", " ", "T", "Z", "").Replace(s) + "Z"
	}
	return t.UTC().Format("20060102T150405Z")
}
