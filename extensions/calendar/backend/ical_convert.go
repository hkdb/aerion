package backend

import (
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// ParsedObject is the result of parsing one CalendarObject's ICS data. It
// holds the master event (the VEVENT without RECURRENCE-ID, or the only
// VEVENT for non-recurring objects) plus any RECURRENCE-ID exceptions
// found alongside it. The sync layer fills in calendar_id / etag / href /
// uuids before persisting.
type ParsedObject struct {
	Master    Event
	Overrides []EventOverride
}

// ParseCalendarObject parses an ICS blob (one CalendarObject's full data,
// usually a VCALENDAR wrapping one or more VEVENTs that share a UID).
// Returns the master event with all fields except ID, CalendarID, ETag,
// Href populated. The master's ICSBlob is set to the input string for
// re-parse during recurrence expansion.
//
// Mapping notes:
//   - Master event: the VEVENT without RECURRENCE-ID. If multiple, picks
//     the first; CalDAV servers should not produce ambiguous objects.
//   - All-day detection: DTSTART with VALUE=DATE parameter.
//   - DTEnd fallback: if no DTEND, treat as DTSTART+0 (per RFC 5545, DTEND
//     is optional; without it, the event is "instantaneous"). DURATION
//     would be a more complete handling — deferred until users hit it.
//   - TZName: extracted from DTSTART's TZID parameter, or empty for
//     floating / UTC times.
//   - Floating local times (no TZID, no Z suffix): parsed as time.Local.
//     The Unix timestamp captures the instant for the user's current
//     locale at parse time; if the user's tz changes later, floating
//     events would display at a different absolute time. Documented as
//     a known limitation.
func ParseCalendarObject(rawICS string) (*ParsedObject, error) {
	if rawICS == "" {
		return nil, fmt.Errorf("ical: empty ICS data")
	}

	dec := ical.NewDecoder(strings.NewReader(rawICS))
	cal, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("ical decode: %w", err)
	}

	events := cal.Events()
	if len(events) == 0 {
		return nil, fmt.Errorf("ical: no VEVENT found in ICS data")
	}

	// Find the master (no RECURRENCE-ID) + collect overrides.
	var master *ical.Event
	var overrideEvents []ical.Event
	for i := range events {
		ev := events[i]
		if ev.Props.Get(ical.PropRecurrenceID) == nil {
			if master == nil {
				master = &ev
				continue
			}
			// Multiple VEVENTs without RECURRENCE-ID — pick the first,
			// drop the rest. CalDAV servers shouldn't produce these.
			continue
		}
		overrideEvents = append(overrideEvents, ev)
	}

	if master == nil {
		// All events have RECURRENCE-ID — unusual but possible (server
		// sent only the override). Treat the first as the master.
		first := events[0]
		master = &first
		overrideEvents = nil
	}

	masterEvent, err := buildEvent(master, rawICS)
	if err != nil {
		return nil, fmt.Errorf("build master event: %w", err)
	}

	overrides := make([]EventOverride, 0, len(overrideEvents))
	for i := range overrideEvents {
		ov, err := buildOverride(&overrideEvents[i])
		if err != nil {
			// Skip malformed overrides rather than fail the whole parse;
			// they're additive metadata, not the master event itself.
			continue
		}
		overrides = append(overrides, ov)
	}

	return &ParsedObject{
		Master:    masterEvent,
		Overrides: overrides,
	}, nil
}

func buildEvent(ev *ical.Event, rawICS string) (Event, error) {
	uid := propText(ev, ical.PropUID)
	if uid == "" {
		return Event{}, fmt.Errorf("VEVENT missing UID")
	}

	dtstartProp := ev.Props.Get(ical.PropDateTimeStart)
	if dtstartProp == nil {
		return Event{}, fmt.Errorf("VEVENT missing DTSTART")
	}

	isAllDay := dtstartProp.Params.Get(ical.ParamValue) == string(ical.ValueDate)
	tzName := dtstartProp.Params.Get(ical.ParamTimezoneID)

	// tz-less values (all-day VALUE=DATE, floating no-TZID) are interpreted in
	// the user's configured display tz so they bucket on the right day; an
	// explicit TZID still wins.
	loc := configuredTZ()
	if tzName != "" {
		if l, err := time.LoadLocation(tzName); err == nil {
			loc = l
		}
	}

	dtstart, err := ev.DateTimeStart(loc)
	if err != nil {
		return Event{}, fmt.Errorf("parse DTSTART: %w", err)
	}

	dtend, err := ev.DateTimeEnd(loc)
	if err != nil {
		// No DTEND / DURATION → treat as instantaneous (same as DTSTART).
		dtend = dtstart
	}

	rrule := propText(ev, ical.PropRecurrenceRule)
	// go-ical exposes the raw RRULE value (e.g., "FREQ=WEEKLY;BYDAY=MO").
	// We store the property line including the prefix so re-parse via
	// RecurrenceSet on the master Component works. The raw rrule_text
	// column is purely a denormalized hint for "is this recurring?"
	// queries (NULL = non-recurring).

	return Event{
		UID:          uid,
		Summary:      propText(ev, ical.PropSummary),
		Description:  propText(ev, ical.PropDescription),
		Location:     propText(ev, ical.PropLocation),
		DTStartUnix:  dtstart.Unix(),
		DTEndUnix:    dtend.Unix(),
		IsAllDay:     isAllDay,
		TZName:       tzName,
		RRuleText:    rrule,
		Transparency: transparencyFromICS(propText(ev, icsPropTransp)),
		Visibility:   visibilityFromICS(propText(ev, icsPropClass)),
		ICSBlob:      rawICS,
		Attendees:    parseAttendeesFromVEVENT(ev),
		Organizer:    parseOrganizerFromVEVENT(ev),
	}, nil
}

func buildOverride(ev *ical.Event) (EventOverride, error) {
	recProp := ev.Props.Get(ical.PropRecurrenceID)
	if recProp == nil {
		return EventOverride{}, fmt.Errorf("override missing RECURRENCE-ID")
	}

	tzName := recProp.Params.Get(ical.ParamTimezoneID)
	loc := configuredTZ() // tz-less RECURRENCE-ID → configured display tz
	if tzName != "" {
		if l, err := time.LoadLocation(tzName); err == nil {
			loc = l
		}
	}

	recTime, err := recProp.DateTime(loc)
	if err != nil {
		return EventOverride{}, fmt.Errorf("parse RECURRENCE-ID: %w", err)
	}

	// Re-encode just this override VEVENT into its own ICS blob so the
	// expander can read it standalone.
	wrapper := ical.NewCalendar()
	wrapper.Props.SetText(ical.PropVersion, "2.0")
	wrapper.Props.SetText(ical.PropProductID, "-//Aerion//Calendar//EN")
	wrapper.Children = append(wrapper.Children, ev.Component)

	var sb strings.Builder
	enc := ical.NewEncoder(&sb)
	if err := enc.Encode(wrapper); err != nil {
		return EventOverride{}, fmt.Errorf("encode override: %w", err)
	}

	return EventOverride{
		RecurrenceIDUnix: recTime.Unix(),
		ICSBlob:          sb.String(),
	}, nil
}

// propText returns the trimmed raw Value of a property, or "" when absent.
// Raw (not iCal-unescaped) on purpose: callers include RRULE, whose commas are
// RECUR part-separators — Prop.Text() would comma-split them. For human-facing
// TEXT bodies (DESCRIPTION, X-ALT-DESC) use propTextDecoded instead.
func propText(ev *ical.Event, name string) string {
	p := ev.Props.Get(name)
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Value)
}

// propTextDecoded returns a TEXT property iCal-unescaped (\n \, \; \\ → literal)
// via Prop.Text(). Use only for genuine TEXT properties — NOT RRULE/RECUR
// values, where Text()'s comma-splitting would corrupt the value.
func propTextDecoded(ev *ical.Event, name string) string {
	p := ev.Props.Get(name)
	if p == nil {
		return ""
	}
	if t, err := p.Text(); err == nil {
		return strings.TrimSpace(t)
	}
	return strings.TrimSpace(p.Value)
}

// unescapeICalText decodes RFC 5545 TEXT escapes in a raw property value:
// \n and \N → newline; \\ \, \; → literal backslash/comma/semicolon. Applied
// when a stored body is rendered, so DESCRIPTIONs that were persisted raw
// (any provider) display with real line breaks instead of literal "\n".
//
// Deliberately NOT go-ical's Prop.Text(): that splits on unescaped commas and
// returns only the first segment (silently truncating a body) and errors on
// any unknown escape. This preserves unknown escapes (backslash + char) and
// never truncates. Idempotent on already-decoded text (no backslash → returned
// unchanged), so it's safe to run on bodies from every sync path.
func unescapeICalText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		case '\\', ',', ';':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
