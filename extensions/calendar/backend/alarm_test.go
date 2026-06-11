package backend

// Table-driven VALARM tests. Cover the encodings most CalDAV servers
// emit: RELATIVE -PT15M (default RELATED=START), RELATED=END, absolute
// DATE-TIME, and recurrence override with its own VALARM.

import (
	"strings"
	"testing"
	"time"
)

func TestExtractAlarms_RelativeBeforeStart(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e1
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Standup
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
DESCRIPTION:Standup soon
END:VALARM
END:VEVENT`)
	ev := Event{
		ID:           "ev1",
		Summary:      "Standup",
		DTStartUnix:  time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix(),
		DTEndUnix:    time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix(),
		ICSBlob:      ics,
	}
	insts := []EventInstance{{Event: ev, InstanceStartUnix: ev.DTStartUnix, InstanceEndUnix: ev.DTEndUnix}}

	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(alarms))
	}
	want := ev.DTStartUnix - 15*60
	if alarms[0].TriggerUnix != want {
		t.Errorf("trigger unix: got %d want %d", alarms[0].TriggerUnix, want)
	}
	if alarms[0].Action != "display" {
		t.Errorf("action: got %q want display", alarms[0].Action)
	}
	if alarms[0].Description != "Standup soon" {
		t.Errorf("description: got %q", alarms[0].Description)
	}
}

func TestExtractAlarms_RelativeBeforeEnd(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e2
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Meeting
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;RELATED=END:-PT5M
DESCRIPTION:Wrap up
END:VALARM
END:VEVENT`)
	ev := Event{
		ID:          "ev2",
		Summary:     "Meeting",
		DTStartUnix: time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix(),
		DTEndUnix:   time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix(),
		ICSBlob:     ics,
	}
	insts := []EventInstance{{Event: ev, InstanceStartUnix: ev.DTStartUnix, InstanceEndUnix: ev.DTEndUnix}}

	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(alarms))
	}
	// 5 min before end = 14:55
	want := ev.DTEndUnix - 5*60
	if alarms[0].TriggerUnix != want {
		t.Errorf("trigger unix: got %d want %d", alarms[0].TriggerUnix, want)
	}
}

func TestExtractAlarms_AbsoluteDateTime(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e3
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Event
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;VALUE=DATE-TIME:20260605T133000Z
END:VALARM
END:VEVENT`)
	ev := Event{
		ID:          "ev3",
		Summary:     "Event",
		DTStartUnix: time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix(),
		DTEndUnix:   time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix(),
		ICSBlob:     ics,
	}
	insts := []EventInstance{{Event: ev, InstanceStartUnix: ev.DTStartUnix, InstanceEndUnix: ev.DTEndUnix}}

	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(alarms))
	}
	want := time.Date(2026, 6, 5, 13, 30, 0, 0, time.UTC).Unix()
	if alarms[0].TriggerUnix != want {
		t.Errorf("trigger unix: got %d want %d", alarms[0].TriggerUnix, want)
	}
}

func TestExtractAlarms_RecurringOnePerInstance(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e4
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Weekly
RRULE:FREQ=WEEKLY;COUNT=4
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT10M
END:VALARM
END:VEVENT`)
	base := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC)
	ev := Event{
		ID:          "ev4",
		Summary:     "Weekly",
		DTStartUnix: base.Unix(),
		DTEndUnix:   base.Add(1 * time.Hour).Unix(),
		RRuleText:   "RRULE:FREQ=WEEKLY;COUNT=4",
		ICSBlob:     ics,
	}
	// Simulate 4 weekly occurrences.
	insts := make([]EventInstance, 4)
	for i := 0; i < 4; i++ {
		start := base.AddDate(0, 0, 7*i)
		insts[i] = EventInstance{
			Event:             ev,
			InstanceStartUnix: start.Unix(),
			InstanceEndUnix:   start.Add(time.Hour).Unix(),
		}
	}

	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 4 {
		t.Fatalf("want 4 alarms (one per instance), got %d", len(alarms))
	}
	for i, a := range alarms {
		want := insts[i].InstanceStartUnix - 10*60
		if a.TriggerUnix != want {
			t.Errorf("alarm %d trigger: got %d want %d", i, a.TriggerUnix, want)
		}
	}
}

func TestExtractAlarms_OverrideTakesPrecedence(t *testing.T) {
	masterICS := wrapICS(`BEGIN:VEVENT
UID:e5
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Master
RRULE:FREQ=DAILY;COUNT=3
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT30M
END:VALARM
END:VEVENT`)
	overrideICS := wrapICS(`BEGIN:VEVENT
UID:e5
RECURRENCE-ID:20260606T140000Z
DTSTART:20260606T140000Z
DTEND:20260606T150000Z
SUMMARY:Override
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT5M
END:VALARM
END:VEVENT`)
	base := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC)
	ev := Event{
		ID:          "ev5",
		Summary:     "Master",
		DTStartUnix: base.Unix(),
		DTEndUnix:   base.Add(time.Hour).Unix(),
		RRuleText:   "RRULE:FREQ=DAILY;COUNT=3",
		ICSBlob:     masterICS,
	}
	insts := make([]EventInstance, 3)
	for i := 0; i < 3; i++ {
		start := base.AddDate(0, 0, i)
		insts[i] = EventInstance{
			Event:             ev,
			InstanceStartUnix: start.Unix(),
			InstanceEndUnix:   start.Add(time.Hour).Unix(),
		}
	}
	overrideStart := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC).Unix()
	overrides := []EventOverride{{
		EventID:          "ev5",
		RecurrenceIDUnix: overrideStart,
		ICSBlob:          overrideICS,
	}}

	alarms, err := ExtractAlarms(ev, overrides, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 3 {
		t.Fatalf("want 3 alarms, got %d", len(alarms))
	}
	// The override instance (day 1) should fire 5 minutes before, not 30.
	for _, a := range alarms {
		if a.InstanceUnix == overrideStart {
			want := overrideStart - 5*60
			if a.TriggerUnix != want {
				t.Errorf("override alarm trigger: got %d want %d", a.TriggerUnix, want)
			}
		}
	}
}

func TestExtractAlarms_NonDisplayActionPreserved(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e6
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:Audio
BEGIN:VALARM
ACTION:AUDIO
TRIGGER:-PT5M
END:VALARM
END:VEVENT`)
	ev := Event{
		ID:          "ev6",
		Summary:     "Audio",
		DTStartUnix: time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix(),
		DTEndUnix:   time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix(),
		ICSBlob:     ics,
	}
	insts := []EventInstance{{Event: ev, InstanceStartUnix: ev.DTStartUnix, InstanceEndUnix: ev.DTEndUnix}}

	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(alarms))
	}
	if alarms[0].Action != "audio" {
		t.Errorf("action: got %q want audio", alarms[0].Action)
	}
}

func TestExtractAlarms_NoVALARM(t *testing.T) {
	ics := wrapICS(`BEGIN:VEVENT
UID:e7
DTSTART:20260605T140000Z
DTEND:20260605T150000Z
SUMMARY:No alarm
END:VEVENT`)
	ev := Event{
		ID:      "ev7",
		Summary: "No alarm",
		ICSBlob: ics,
	}
	insts := []EventInstance{{Event: ev, InstanceStartUnix: 0, InstanceEndUnix: 0}}
	alarms, err := ExtractAlarms(ev, nil, insts)
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 0 {
		t.Errorf("want 0 alarms, got %d", len(alarms))
	}
}

// wrapICS wraps a VEVENT body in a minimal VCALENDAR so go-ical's decoder
// is happy. Uses CRLF line endings per RFC 5545.
func wrapICS(body string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Aerion//Test//EN\r\n")
	for _, line := range strings.Split(body, "\n") {
		b.WriteString(strings.TrimRight(line, "\r"))
		b.WriteString("\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}
