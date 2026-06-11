package backend

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// ExpandInRange expands a stored Event into concrete EventInstances within
// `[from, to]`. Non-recurring events return at most one instance; recurring
// events return zero or more via `Component.RecurrenceSet(loc).Between(...)`.
// RECURRENCE-ID overrides REPLACE the matching default-expanded instance
// (matched by occurrence start time = override's RECURRENCE-ID).
//
// Returns instances sorted by InstanceStartUnix ASC.
func ExpandInRange(ev Event, overrides []EventOverride, from, to time.Time) ([]EventInstance, error) {
	if from.After(to) {
		return nil, nil
	}

	// Non-recurring: include if the event overlaps the window.
	if ev.RRuleText == "" {
		evStart := time.Unix(ev.DTStartUnix, 0)
		evEnd := time.Unix(ev.DTEndUnix, 0)
		if evEnd.Before(from) || evStart.After(to) {
			return nil, nil
		}
		return []EventInstance{{
			Event:             ev,
			InstanceStartUnix: ev.DTStartUnix,
			InstanceEndUnix:   ev.DTEndUnix,
		}}, nil
	}

	// Recurring: re-parse the master ICS blob, run RecurrenceSet,
	// generate the occurrence list in window, then apply overrides.
	loc := resolveLocation(ev.TZName)
	dec := ical.NewDecoder(strings.NewReader(ev.ICSBlob))
	cal, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("rrule_expand: decode master ICS: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return nil, fmt.Errorf("rrule_expand: master ICS has no VEVENT")
	}

	// Pick the VEVENT without RECURRENCE-ID (the master).
	var masterEv *ical.Event
	for i := range events {
		if events[i].Props.Get(ical.PropRecurrenceID) == nil {
			e := events[i]
			masterEv = &e
			break
		}
	}
	if masterEv == nil {
		// Edge case — treat the first as master.
		first := events[0]
		masterEv = &first
	}

	set, err := masterEv.RecurrenceSet(loc)
	if err != nil {
		return nil, fmt.Errorf("rrule_expand: build recurrence set: %w", err)
	}

	occurrences := set.Between(from, to, true)

	// Index overrides by their RECURRENCE-ID for O(1) lookup. Multiple
	// overrides at the same instant shouldn't happen; if they do, the last
	// one wins (per RFC 5545, the most recent definition).
	overrideByInstant := make(map[int64]EventOverride, len(overrides))
	for _, ov := range overrides {
		overrideByInstant[ov.RecurrenceIDUnix] = ov
	}

	// Compute the event's duration once so override-less instances can
	// inherit it. Override instances supply their own DTSTART/DTEND.
	masterDuration := time.Duration(ev.DTEndUnix-ev.DTStartUnix) * time.Second

	out := make([]EventInstance, 0, len(occurrences))
	for _, occ := range occurrences {
		instUnix := occ.Unix()
		if ov, ok := overrideByInstant[instUnix]; ok {
			// Apply override: parse its ICS, extract DTSTART/DTEND/SUMMARY,
			// build an EventInstance using overrides where present and
			// master values where absent.
			inst, err := applyOverride(ev, ov)
			if err != nil {
				// Skip malformed override; fall back to default expansion.
				out = append(out, EventInstance{
					Event:             ev,
					InstanceStartUnix: instUnix,
					InstanceEndUnix:   instUnix + int64(masterDuration.Seconds()),
				})
				continue
			}
			out = append(out, inst)
			continue
		}
		out = append(out, EventInstance{
			Event:             ev,
			InstanceStartUnix: instUnix,
			InstanceEndUnix:   instUnix + int64(masterDuration.Seconds()),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].InstanceStartUnix < out[j].InstanceStartUnix
	})

	return out, nil
}

// resolveLocation looks up the IANA timezone name; returns time.Local on
// empty or unknown names so expansion still works (just less correct for
// DST around floating events).
func resolveLocation(tzName string) *time.Location {
	if tzName == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return time.Local
	}
	return loc
}

// applyOverride parses an override's ICS blob and merges its non-empty
// fields onto the master to produce one EventInstance. Override semantics
// per RFC 5545 §3.8.4.4: an override is a full VEVENT — the fields it
// specifies replace the corresponding master fields for that instance only.
func applyOverride(master Event, ov EventOverride) (EventInstance, error) {
	dec := ical.NewDecoder(strings.NewReader(ov.ICSBlob))
	cal, err := dec.Decode()
	if err != nil {
		return EventInstance{}, err
	}
	events := cal.Events()
	if len(events) == 0 {
		return EventInstance{}, fmt.Errorf("override ICS has no VEVENT")
	}
	ev := events[0]

	loc := resolveLocation(master.TZName)
	dtstartProp := ev.Props.Get(ical.PropDateTimeStart)
	if dtstartProp != nil {
		if tz := dtstartProp.Params.Get(ical.ParamTimezoneID); tz != "" {
			if l, lerr := time.LoadLocation(tz); lerr == nil {
				loc = l
			}
		}
	}

	dtstart, err := ev.DateTimeStart(loc)
	if err != nil {
		return EventInstance{}, fmt.Errorf("override DTSTART: %w", err)
	}
	dtend, err := ev.DateTimeEnd(loc)
	if err != nil {
		dtend = dtstart
	}

	inst := EventInstance{
		Event:                master,
		InstanceStartUnix:    dtstart.Unix(),
		InstanceEndUnix:      dtend.Unix(),
		IsRecurrenceOverride: true,
	}
	if v := propText(&ev, ical.PropSummary); v != "" {
		inst.Summary = v
	}
	if v := propText(&ev, ical.PropDescription); v != "" {
		inst.Description = v
	}
	if v := propText(&ev, ical.PropLocation); v != "" {
		inst.Location = v
	}
	return inst, nil
}
