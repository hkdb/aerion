package backend

// VALARM parser + per-instance trigger computation. Phase 1G.
//
// ExtractAlarms walks the VALARM components nested inside an Event's
// stored ICSBlob and projects them onto the event's expanded instances,
// producing one Alarm per (occurrence × VALARM). RECURRENCE-ID overrides
// contribute their own VALARMs when present (override > master).
//
// TRIGGER encoding per RFC 5545 §3.8.6.3:
//   - Duration form (default): "-PT15M" → 15 minutes before related point.
//   - RELATED=START (default) → relative to instance DTSTART.
//   - RELATED=END → relative to instance DTEND.
//   - VALUE=DATE-TIME form: absolute UTC instant (rare; uses VEVENT-tz
//     conversion if TZID present).
//
// ACTION: only "DISPLAY" is dispatched today. Others are stored on the
// Alarm so the scheduler can filter; future Phase 2+ can route AUDIO /
// EMAIL / PROCEDURE through different mechanisms.

import (
	"fmt"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"
)

// ExtractAlarms computes Alarm rows for one Event + its expanded
// instances. Caller passes the master event (with ICSBlob) and the list of
// EventOverrides whose ics_blob may include their own VALARM blocks.
//
// The returned alarms have ID auto-generated, Status="pending", and
// CreatedAt=0 (filled in by Store.UpsertAlarmTx).
func ExtractAlarms(ev Event, overrides []EventOverride, instances []EventInstance) ([]Alarm, error) {
	if len(instances) == 0 {
		return nil, nil
	}

	masterAlarms, err := parseAlarmTemplates(ev.ICSBlob)
	if err != nil {
		return nil, fmt.Errorf("parse master alarms: %w", err)
	}

	// Override → per-instance templates (keyed by RecurrenceIDUnix).
	overrideTemplates := make(map[int64][]alarmTemplate, len(overrides))
	for _, ov := range overrides {
		tmpls, err := parseAlarmTemplates(ov.ICSBlob)
		if err != nil {
			// One bad override shouldn't kill the whole pipeline.
			continue
		}
		overrideTemplates[ov.RecurrenceIDUnix] = tmpls
	}

	out := make([]Alarm, 0, len(instances))
	for _, inst := range instances {
		templates := masterAlarms
		if ov, ok := overrideTemplates[inst.InstanceStartUnix]; ok {
			// Override takes precedence; if the override has zero alarms,
			// it means the user intentionally cleared reminders for that
			// instance — honor that.
			templates = ov
		}
		for _, t := range templates {
			triggerUnix := computeTriggerUnix(t, inst)
			out = append(out, Alarm{
				ID:           uuid.NewString(),
				EventID:      ev.ID,
				InstanceUnix: inst.InstanceStartUnix,
				TriggerUnix:  triggerUnix,
				Action:       strings.ToLower(t.action),
				Description:  t.description,
			})
		}
	}
	return out, nil
}

// alarmTemplate is one VALARM parsed from an ICSBlob — not yet projected
// onto a concrete instance. Holds the raw TRIGGER + ACTION + DESCRIPTION
// so the per-instance projection step is a simple arithmetic application.
type alarmTemplate struct {
	action      string // DISPLAY / AUDIO / EMAIL / PROCEDURE (raw casing)
	description string

	// One of the two trigger encodings is populated:
	relativeSeconds int64 // offset in seconds; negative = before reference
	relatedToEnd    bool  // RELATED=END instead of START
	absoluteUnix    int64 // if non-zero, this is an absolute trigger
}

// parseAlarmTemplates decodes one ICSBlob (a VCALENDAR wrapping VEVENTs)
// and returns the VALARM templates of the first VEVENT that doesn't
// have a RECURRENCE-ID (the master); overrides should call this on their
// own ICSBlob which holds exactly one VEVENT.
func parseAlarmTemplates(icsBlob string) ([]alarmTemplate, error) {
	if icsBlob == "" {
		return nil, nil
	}
	dec := ical.NewDecoder(strings.NewReader(icsBlob))
	cal, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("decode ics: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return nil, nil
	}

	// Master = first VEVENT without RECURRENCE-ID, else first.
	var ev *ical.Event
	for i := range events {
		if events[i].Props.Get(ical.PropRecurrenceID) == nil {
			e := events[i]
			ev = &e
			break
		}
	}
	if ev == nil {
		first := events[0]
		ev = &first
	}

	out := make([]alarmTemplate, 0, len(ev.Component.Children))
	for _, child := range ev.Component.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		t, ok := buildAlarmTemplate(child)
		if !ok {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func buildAlarmTemplate(comp *ical.Component) (alarmTemplate, bool) {
	t := alarmTemplate{action: "DISPLAY"}

	if actionProp := comp.Props.Get(ical.PropAction); actionProp != nil {
		t.action = strings.ToUpper(strings.TrimSpace(actionProp.Value))
	}
	if descProp := comp.Props.Get(ical.PropDescription); descProp != nil {
		t.description = descProp.Value
	}

	triggerProp := comp.Props.Get(ical.PropTrigger)
	if triggerProp == nil {
		return t, false
	}

	// Detect absolute vs relative trigger. RFC 5545: absolute requires
	// VALUE=DATE-TIME; otherwise default is DURATION.
	valueType := strings.ToUpper(strings.TrimSpace(triggerProp.Params.Get(ical.ParamValue)))
	if valueType == "DATE-TIME" {
		dt, err := triggerProp.DateTime(nil)
		if err != nil {
			return t, false
		}
		t.absoluteUnix = dt.Unix()
		return t, true
	}

	// Relative: parse as ical Duration.
	dur, err := triggerProp.Duration()
	if err != nil {
		return t, false
	}
	t.relativeSeconds = int64(dur.Seconds())
	related := strings.ToUpper(strings.TrimSpace(triggerProp.Params.Get(ical.ParamRelated)))
	t.relatedToEnd = related == "END"
	return t, true
}

func computeTriggerUnix(t alarmTemplate, inst EventInstance) int64 {
	if t.absoluteUnix != 0 {
		return t.absoluteUnix
	}
	if t.relatedToEnd {
		return inst.InstanceEndUnix + t.relativeSeconds
	}
	return inst.InstanceStartUnix + t.relativeSeconds
}
