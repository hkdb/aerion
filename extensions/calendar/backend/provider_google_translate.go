package backend

// ICS ↔ Google Calendar JSON translation.
//
// The provider stores everything as ICS blobs (events.ics_blob /
// event_recurrence_overrides.ics_blob) so rrule_expand.go + alarm.go +
// the views work uniformly across providers. Translation lives at the
// transport boundary: SyncCalendar converts incoming Google JSON →
// per-event ICS blob; PushEvent converts the master ICS blob from
// event_crud.go's serializeVEVENT → Google POST/PATCH JSON.
//
// Field mapping is documented inline. RRULE strings round-trip 1-for-1
// (Google uses ICS RFC 5545 syntax). Recurring-event overrides are
// modeled in Google's API as separate event resources with
// recurringEventId pointing at the master + originalStartTime declaring
// which occurrence they replace — we surface those as ICS VEVENTs
// carrying RECURRENCE-ID, so rrule_expand.go finds them via the
// existing master-plus-overrides path.

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// googleEvent is the JSON shape for one Calendar API event resource. Only
// the fields Aerion reads/writes are modeled; the rest are ignored. JSON
// tags use omitempty so PATCH payloads only carry the fields we actually
// touched.
type googleEvent struct {
	ID                string           `json:"id,omitempty"`
	ICalUID           string           `json:"iCalUID,omitempty"`
	ETag              string           `json:"etag,omitempty"`
	Status            string           `json:"status,omitempty"` // "confirmed" | "tentative" | "cancelled"
	Summary           string           `json:"summary,omitempty"`
	Description       string           `json:"description,omitempty"`
	Location          string           `json:"location,omitempty"`
	Start             *googleTimePoint `json:"start,omitempty"`
	End               *googleTimePoint `json:"end,omitempty"`
	Recurrence        []string         `json:"recurrence,omitempty"`
	RecurringEventID  string           `json:"recurringEventId,omitempty"`
	OriginalStartTime *googleTimePoint `json:"originalStartTime,omitempty"`
	Reminders         *googleReminders `json:"reminders,omitempty"`
	Attendees         []googleAttendee `json:"attendees,omitempty"`
	Organizer         *googleAttendee  `json:"organizer,omitempty"`
}

// googleAttendee is the shape Google Calendar API v3 returns/accepts.
// `organizer`/`self` are read-only metadata Google adds on responses; we
// emit them empty on writes. `resource` is set when CUTYPE=RESOURCE.
// `additionalGuests` is unused on our side; we round-trip-preserve it
// from server responses by leaving it on the struct.
type googleAttendee struct {
	Email            string `json:"email"`
	DisplayName      string `json:"displayName,omitempty"`
	Optional         bool   `json:"optional,omitempty"`
	ResponseStatus   string `json:"responseStatus,omitempty"` // needsAction|declined|tentative|accepted
	Organizer        bool   `json:"organizer,omitempty"`      // read-only on responses
	Self             bool   `json:"self,omitempty"`           // read-only on responses
	Resource         bool   `json:"resource,omitempty"`
	Comment          string `json:"comment,omitempty"`
	AdditionalGuests int    `json:"additionalGuests,omitempty"`
}

// googleTimePoint represents start/end/originalStartTime. Mutually
// exclusive: either Date (all-day, YYYY-MM-DD) or DateTime (RFC 3339).
type googleTimePoint struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type googleReminders struct {
	UseDefault bool                     `json:"useDefault"`
	Overrides  []googleReminderOverride `json:"overrides,omitempty"`
}

type googleReminderOverride struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}

// googleCalendarListEntry is one item returned from /users/me/calendarList.
// Used by the bridge's calendar picker.
type googleCalendarListEntry struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	Primary    bool   `json:"primary,omitempty"`
	AccessRole string `json:"accessRole"` // "owner" | "writer" | "reader" | "freeBusyReader"
}

// errGoogleEventCancelled is returned by translateGoogleEventToICS for
// events with status="cancelled". The sync caller treats it as a delete.
var errGoogleEventCancelled = errors.New("google event cancelled")

// translateGoogleEventToICS converts ONE Google event JSON into a
// single-VEVENT VCALENDAR ICS blob. Master events and overrides go through
// the same function; overrides carry RECURRENCE-ID from OriginalStartTime.
//
// Cancelled events (status="cancelled") return errGoogleEventCancelled
// so the sync caller treats them as deletions, not upserts.
func translateGoogleEventToICS(ev googleEvent) (string, error) {
	if ev.Status == "cancelled" {
		return "", errGoogleEventCancelled
	}
	if ev.ICalUID == "" {
		return "", fmt.Errorf("google event missing iCalUID")
	}
	if ev.Start == nil || ev.End == nil {
		return "", fmt.Errorf("google event missing start/end")
	}

	icalEv := ical.NewEvent()
	icalEv.Props.SetText(ical.PropUID, ev.ICalUID)
	icalEv.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

	if ev.Summary != "" {
		icalEv.Props.SetText(ical.PropSummary, ev.Summary)
	}
	if ev.Description != "" {
		icalEv.Props.SetText(ical.PropDescription, ev.Description)
	}
	if ev.Location != "" {
		icalEv.Props.SetText(ical.PropLocation, ev.Location)
	}

	if err := setICSTimeFromGoogle(icalEv, ical.PropDateTimeStart, ev.Start); err != nil {
		return "", fmt.Errorf("translate start: %w", err)
	}
	if err := setICSTimeFromGoogle(icalEv, ical.PropDateTimeEnd, ev.End); err != nil {
		return "", fmt.Errorf("translate end: %w", err)
	}

	for _, line := range ev.Recurrence {
		applyRecurrenceLine(icalEv, line)
	}

	if ev.OriginalStartTime != nil {
		if err := setICSTimeFromGoogle(icalEv, ical.PropRecurrenceID, ev.OriginalStartTime); err != nil {
			return "", fmt.Errorf("translate recurrence-id: %w", err)
		}
	}

	if ev.Reminders != nil && !ev.Reminders.UseDefault {
		for _, r := range ev.Reminders.Overrides {
			if r.Method != "popup" {
				// Aerion's notifier is "popup"-shaped; email reminders
				// are out of scope for now. Round-trip preserves them
				// only when Google echoes them back; we don't author them.
				continue
			}
			alarm := &ical.Component{Name: ical.CompAlarm, Props: ical.Props{}}
			alarm.Props.SetText(ical.PropAction, "DISPLAY")
			trigger := ical.NewProp(ical.PropTrigger)
			trigger.Value = fmt.Sprintf("-PT%dM", r.Minutes)
			alarm.Props.Add(trigger)
			if ev.Summary != "" {
				alarm.Props.SetText(ical.PropDescription, ev.Summary)
			}
			icalEv.Component.Children = append(icalEv.Component.Children, alarm)
		}
	}

	// Organizer + Attendees from Google → ATTENDEE/ORGANIZER ICS lines.
	// Emitted via the shared helper so the wire form matches what Phase A's
	// parser would expect on read-back. Aerion's local DB then carries them
	// via store.go's attendees_json column.
	var orgInput *OrganizerInput
	if ev.Organizer != nil && ev.Organizer.Email != "" {
		orgInput = &OrganizerInput{
			Email:      ev.Organizer.Email,
			CommonName: ev.Organizer.DisplayName,
		}
	}
	if len(ev.Attendees) > 0 || orgInput != nil {
		atts := make([]AttendeeInput, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			ai := AttendeeInput{
				Email:      a.Email,
				CommonName: a.DisplayName,
				PartStat:   googlePartStatToICS(a.ResponseStatus),
				Role:       RoleReqParticipant,
			}
			if a.Optional {
				ai.Role = RoleOptParticipant
			}
			if a.Resource {
				ai.CUType = CUTypeResource
			}
			atts = append(atts, ai)
		}
		emitAttendeesIntoVEVENT(icalEv, orgInput, atts)
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Aerion//Calendar Extension//EN")
	cal.Children = append(cal.Children, icalEv.Component)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// applyRecurrenceLine sets one RRULE/EXDATE/RDATE property on the event
// from Google's recurrence-array line.
func applyRecurrenceLine(ev *ical.Event, line string) {
	switch {
	case strings.HasPrefix(line, "RRULE:"):
		setRRuleText(ev.Props, strings.TrimPrefix(line, "RRULE:"))
	case strings.HasPrefix(line, "EXDATE"):
		colon := strings.Index(line, ":")
		if colon < 0 {
			return
		}
		p := ical.NewProp(ical.PropExceptionDates)
		p.Value = line[colon+1:]
		ev.Props.Add(p)
	case strings.HasPrefix(line, "RDATE"):
		colon := strings.Index(line, ":")
		if colon < 0 {
			return
		}
		p := ical.NewProp(ical.PropRecurrenceDates)
		p.Value = line[colon+1:]
		ev.Props.Add(p)
	}
}

// translateICSToGoogleJSON extracts the master VEVENT from a single-VEVENT
// VCALENDAR blob (built by event_crud.go's serializeVEVENT) and produces
// the Google JSON shape suitable for POST/PATCH /events.
func translateICSToGoogleJSON(icsBlob string) (googleEvent, error) {
	dec := ical.NewDecoder(strings.NewReader(icsBlob))
	cal, err := dec.Decode()
	if err != nil {
		return googleEvent{}, fmt.Errorf("ical decode: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return googleEvent{}, fmt.Errorf("no VEVENT in ICS blob")
	}
	// Find the master (no RECURRENCE-ID) — Phase 3 + Google sync produce
	// single-VEVENT blobs, so events[0] is the master in practice.
	ev := events[0]

	out := googleEvent{
		ICalUID:     propText(&ev, ical.PropUID),
		Summary:     propText(&ev, ical.PropSummary),
		Description: propText(&ev, ical.PropDescription),
		Location:    propText(&ev, ical.PropLocation),
	}

	start, end, err := extractGoogleTimes(&ev)
	if err != nil {
		return googleEvent{}, err
	}
	out.Start = start
	out.End = end

	if rrule := propText(&ev, ical.PropRecurrenceRule); rrule != "" {
		out.Recurrence = []string{"RRULE:" + rrule}
	}

	// Attendees + Organizer. Roundtrip via the shared parser so the wire
	// shape matches whatever Phase A's parser would produce — single source
	// of truth for ATTENDEE/ORGANIZER property handling.
	if atts := parseAttendeesFromVEVENT(&ev); len(atts) > 0 {
		out.Attendees = make([]googleAttendee, 0, len(atts))
		for _, a := range atts {
			out.Attendees = append(out.Attendees, googleAttendee{
				Email:          a.Email,
				DisplayName:    a.CommonName,
				Optional:       strings.EqualFold(a.Role, RoleOptParticipant),
				ResponseStatus: icsPartStatToGoogle(a.PartStat),
				Resource:       strings.EqualFold(a.CUType, CUTypeResource) || strings.EqualFold(a.CUType, CUTypeRoom),
			})
		}
	}
	if org := parseOrganizerFromVEVENT(&ev); org != nil {
		out.Organizer = &googleAttendee{Email: org.Email, DisplayName: org.CommonName}
	}

	for _, child := range ev.Component.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		if propTextOnComp(child, ical.PropAction) != "DISPLAY" {
			continue
		}
		minutes, ok := parseTriggerMinutes(propTextOnComp(child, ical.PropTrigger))
		if !ok {
			continue
		}
		if out.Reminders == nil {
			out.Reminders = &googleReminders{UseDefault: false}
		}
		out.Reminders.Overrides = append(out.Reminders.Overrides,
			googleReminderOverride{Method: "popup", Minutes: minutes})
	}

	return out, nil
}

// setICSTimeFromGoogle stamps an ICS DATE-TIME (with TZID) or DATE property
// from a Google time point.
func setICSTimeFromGoogle(ev *ical.Event, propName string, tp *googleTimePoint) error {
	if tp == nil {
		return fmt.Errorf("time point required")
	}
	// All-day: Google's "date" → ICS DATE form (YYYYMMDD, no T).
	if tp.Date != "" {
		parsed, err := time.Parse("2006-01-02", tp.Date)
		if err != nil {
			return fmt.Errorf("parse date %q: %w", tp.Date, err)
		}
		prop := ical.NewProp(propName)
		prop.Params.Set(ical.ParamValue, string(ical.ValueDate))
		prop.Value = parsed.Format("20060102")
		ev.Props.Set(prop)
		return nil
	}
	if tp.DateTime == "" {
		return fmt.Errorf("time point has neither date nor dateTime")
	}
	parsed, err := time.Parse(time.RFC3339, tp.DateTime)
	if err != nil {
		return fmt.Errorf("parse dateTime %q: %w", tp.DateTime, err)
	}
	// Floating to UTC when timezone is missing or explicitly "UTC".
	if tp.TimeZone == "" || tp.TimeZone == "UTC" {
		ev.Props.SetDateTime(propName, parsed.UTC())
		return nil
	}
	loc, lerr := time.LoadLocation(tp.TimeZone)
	if lerr != nil {
		// Unknown tz — fall back to UTC; sync still works.
		ev.Props.SetDateTime(propName, parsed.UTC())
		return nil
	}
	local := parsed.In(loc)
	prop := ical.NewProp(propName)
	prop.Params.Set(ical.ParamTimezoneID, tp.TimeZone)
	prop.Value = local.Format("20060102T150405")
	ev.Props.Set(prop)
	return nil
}

// extractGoogleTimes builds Google start/end time points from an ICS
// VEVENT's DTSTART/DTEND properties.
func extractGoogleTimes(ev *ical.Event) (*googleTimePoint, *googleTimePoint, error) {
	startProp := ev.Props.Get(ical.PropDateTimeStart)
	endProp := ev.Props.Get(ical.PropDateTimeEnd)
	if startProp == nil || endProp == nil {
		return nil, nil, fmt.Errorf("VEVENT missing DTSTART/DTEND")
	}
	start, err := icsPropToGoogleTime(startProp)
	if err != nil {
		return nil, nil, fmt.Errorf("convert DTSTART: %w", err)
	}
	end, err := icsPropToGoogleTime(endProp)
	if err != nil {
		return nil, nil, fmt.Errorf("convert DTEND: %w", err)
	}
	return start, end, nil
}

// icsPropToGoogleTime converts a single DTSTART/DTEND/RECURRENCE-ID ICS
// property to Google's time-point shape. Handles three forms:
//   - VALUE=DATE (all-day) → Google's "date"
//   - tz-local DATE-TIME with TZID → Google's "dateTime" + "timeZone"
//   - UTC DATE-TIME (Z suffix) → Google's "dateTime" with timeZone="UTC"
func icsPropToGoogleTime(p *ical.Prop) (*googleTimePoint, error) {
	isAllDay := p.Params.Get(ical.ParamValue) == string(ical.ValueDate)
	if isAllDay {
		parsed, err := time.Parse("20060102", p.Value)
		if err != nil {
			return nil, err
		}
		return &googleTimePoint{Date: parsed.Format("2006-01-02")}, nil
	}
	tzName := p.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	if tzName != "" {
		if l, lerr := time.LoadLocation(tzName); lerr == nil {
			loc = l
		}
	}
	t, perr := parseICSDateTime(p.Value, loc)
	if perr != nil {
		return nil, perr
	}
	if tzName == "" {
		return &googleTimePoint{DateTime: t.UTC().Format(time.RFC3339), TimeZone: "UTC"}, nil
	}
	return &googleTimePoint{DateTime: t.In(loc).Format(time.RFC3339), TimeZone: tzName}, nil
}

// parseICSDateTime parses an ICS DATE-TIME value in either local form
// (YYYYMMDDTHHMMSS) interpreted in loc, or UTC form (YYYYMMDDTHHMMSSZ).
func parseICSDateTime(s string, loc *time.Location) (time.Time, error) {
	if strings.HasSuffix(s, "Z") {
		return time.Parse("20060102T150405Z", s)
	}
	return time.ParseInLocation("20060102T150405", s, loc)
}

// propText is provided by ical_convert.go; trim-whitespace-aware reader.

func propTextOnComp(c *ical.Component, name string) string {
	p := c.Props.Get(name)
	if p == nil {
		return ""
	}
	return p.Value
}

// parseTriggerMinutes parses a VALARM TRIGGER value like "-PT15M", "-PT1H",
// or "-P1D" into minutes-before-start. Returns (minutes, true) on success;
// (0, false) otherwise.
func parseTriggerMinutes(trigger string) (int, bool) {
	if strings.HasPrefix(trigger, "-P") && strings.HasSuffix(trigger, "D") && !strings.HasPrefix(trigger, "-PT") {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(trigger, "-P"), "D"))
		if err != nil {
			return 0, false
		}
		return n * 24 * 60, true
	}
	if !strings.HasPrefix(trigger, "-PT") {
		return 0, false
	}
	body := strings.TrimPrefix(trigger, "-PT")
	switch {
	case strings.HasSuffix(body, "M"):
		n, err := strconv.Atoi(strings.TrimSuffix(body, "M"))
		if err != nil {
			return 0, false
		}
		return n, true
	case strings.HasSuffix(body, "H"):
		n, err := strconv.Atoi(strings.TrimSuffix(body, "H"))
		if err != nil {
			return 0, false
		}
		return n * 60, true
	}
	return 0, false
}
