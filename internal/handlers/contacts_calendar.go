package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	events, err := h.db.ListCalendarEvents(userID, from, to)
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
	events, err := h.db.ListCalendarEvents(userID, from, to)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gowebmail.ics"`)

	fmt.Fprintf(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//GoWebMail//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\nX-WR-CALNAME:GoWebMail\r\n")

	for _, ev := range events {
		fmt.Fprintf(w, "BEGIN:VEVENT\r\n")
		fmt.Fprintf(w, "UID:%s\r\n", escICAL(ev.UID))
		fmt.Fprintf(w, "SUMMARY:%s\r\n", escICAL(ev.Title))
		if ev.Description != "" {
			fmt.Fprintf(w, "DESCRIPTION:%s\r\n", escICAL(ev.Description))
		}
		if ev.Location != "" {
			fmt.Fprintf(w, "LOCATION:%s\r\n", escICAL(ev.Location))
		}
		if ev.AllDay {
			// All-day events use DATE format
			start := strings.ReplaceAll(strings.Split(ev.StartTime, "T")[0], "-", "")
			end := strings.ReplaceAll(strings.Split(ev.EndTime, "T")[0], "-", "")
			fmt.Fprintf(w, "DTSTART;VALUE=DATE:%s\r\n", start)
			fmt.Fprintf(w, "DTEND;VALUE=DATE:%s\r\n", end)
		} else {
			fmt.Fprintf(w, "DTSTART:%s\r\n", toICALDate(ev.StartTime))
			fmt.Fprintf(w, "DTEND:%s\r\n", toICALDate(ev.EndTime))
		}
		if ev.OrganizerEmail != "" {
			fmt.Fprintf(w, "ORGANIZER:mailto:%s\r\n", ev.OrganizerEmail)
		}
		if ev.Status != "" {
			fmt.Fprintf(w, "STATUS:%s\r\n", strings.ToUpper(ev.Status))
		}
		if ev.RecurrenceRule != "" {
			fmt.Fprintf(w, "RRULE:%s\r\n", ev.RecurrenceRule)
		}
		fmt.Fprintf(w, "END:VEVENT\r\n")
	}

	fmt.Fprintf(w, "END:VCALENDAR\r\n")
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
