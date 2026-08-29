// Package caldav pulls calendar events and contacts from a remote
// CalDAV/CardDAV server so they can be mirrored into gowebmail's local DB.
// One-way (server -> gowebmail) read sync only.
package caldav

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	dav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"

	"github.com/ghostersk/gowebmail/internal/models"
)

const timeout = 30 * time.Second

// SyncCalendar fetches all VEVENTs from the calendar collection at url
// (HTTP basic auth) and returns them as CalendarEvent rows tagged with accountID.
func SyncCalendar(ctx context.Context, url, user, pass string, accountID int64) ([]*models.CalendarEvent, error) {
	hc := dav.HTTPClientWithBasicAuth(&http.Client{Timeout: timeout}, user, pass)
	c, err := caldav.NewClient(hc, url)
	if err != nil {
		return nil, fmt.Errorf("caldav client: %w", err)
	}
	objs, err := c.QueryCalendar(ctx, "", &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{AllProps: true, AllComps: true},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT"}}},
	})
	if err != nil {
		return nil, fmt.Errorf("caldav query: %w", err)
	}

	var out []*models.CalendarEvent
	for _, obj := range objs {
		if obj.Data == nil {
			continue
		}
		for _, ev := range obj.Data.Events() {
			ev := ev
			out = append(out, eventFromICal(&ev, accountID))
		}
	}
	return out, nil
}

func eventFromICal(ev *ical.Event, accountID int64) *models.CalendarEvent {
	uid, _ := ev.Props.Text(ical.PropUID)
	summary, _ := ev.Props.Text(ical.PropSummary)
	desc, _ := ev.Props.Text(ical.PropDescription)
	loc, _ := ev.Props.Text(ical.PropLocation)

	allDay := false
	if p := ev.Props.Get(ical.PropDateTimeStart); p != nil {
		allDay = p.ValueType() == ical.ValueDate
	}
	start, _ := ev.DateTimeStart(time.UTC)
	end, _ := ev.DateTimeEnd(time.UTC)
	if end.IsZero() {
		end = start
	}

	status := ""
	if s, err := ev.Status(); err == nil {
		status = strings.ToLower(string(s))
	}

	organizer := ""
	if p := ev.Props.Get(ical.PropOrganizer); p != nil {
		organizer = strings.TrimPrefix(p.Value, "mailto:")
	}
	var attendees []string
	for _, p := range ev.Props.Values(ical.PropAttendee) {
		attendees = append(attendees, strings.TrimPrefix(p.Value, "mailto:"))
	}

	rrule := ""
	if p := ev.Props.Get(ical.PropRecurrenceRule); p != nil {
		rrule = p.Value
	}

	return &models.CalendarEvent{
		AccountID:      &accountID,
		UID:            uid,
		Title:          summary,
		Description:    desc,
		Location:       loc,
		StartTime:      formatEventTime(start, allDay),
		EndTime:        formatEventTime(end, allDay),
		AllDay:         allDay,
		RecurrenceRule: rrule,
		Status:         status,
		OrganizerEmail: organizer,
		Attendees:      strings.Join(attendees, ", "),
	}
}

func formatEventTime(t time.Time, allDay bool) string {
	if allDay {
		return t.Format("2006-01-02")
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// SyncContacts fetches all vCards from the address book collection at url
// (HTTP basic auth) and returns them as Contact rows tagged with accountID.
func SyncContacts(ctx context.Context, url, user, pass string, accountID int64) ([]*models.Contact, error) {
	hc := dav.HTTPClientWithBasicAuth(&http.Client{Timeout: timeout}, user, pass)
	c, err := carddav.NewClient(hc, url)
	if err != nil {
		return nil, fmt.Errorf("carddav client: %w", err)
	}
	objs, err := c.QueryAddressBook(ctx, "", &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{AllProp: true},
	})
	if err != nil {
		return nil, fmt.Errorf("carddav query: %w", err)
	}

	var out []*models.Contact
	for _, obj := range objs {
		if obj.Card == nil {
			continue
		}
		out = append(out, contactFromVCard(obj.Card, obj.Path, accountID))
	}
	return out, nil
}

func contactFromVCard(card vcard.Card, path string, accountID int64) *models.Contact {
	uid := card.PreferredValue(vcard.FieldUID)
	if uid == "" {
		// vCard UID is only a SHOULD in vCard 3.0 — fall back to the stable
		// resource path so contacts without one don't collide on upsert.
		uid = path
	}
	name := card.PreferredValue(vcard.FieldFormattedName)
	org := card.PreferredValue(vcard.FieldOrganization)
	if i := strings.Index(org, ";"); i >= 0 {
		org = org[:i]
	}
	return &models.Contact{
		AccountID:   &accountID,
		UID:         uid,
		DisplayName: name,
		Email:       card.PreferredValue(vcard.FieldEmail),
		Phone:       card.PreferredValue(vcard.FieldTelephone),
		Company:     org,
		Notes:       card.PreferredValue(vcard.FieldNote),
	}
}
