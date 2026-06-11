package backend

import (
	"testing"
	"time"
)

const weeklyMWFICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:weekly-mwf@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251103T090000Z
DTEND:20251103T093000Z
SUMMARY:Standup
RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR;COUNT=3
END:VEVENT
END:VCALENDAR
`

const monthlyFirstMonICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:monthly-firstmon@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251103T140000Z
DTEND:20251103T150000Z
SUMMARY:All-hands
RRULE:FREQ=MONTHLY;BYDAY=1MO;COUNT=3
END:VEVENT
END:VCALENDAR
`

const dailyUntilICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:daily-until@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251110T080000Z
DTEND:20251110T083000Z
SUMMARY:Coffee
RRULE:FREQ=DAILY;UNTIL=20251114T080000Z
END:VEVENT
END:VCALENDAR
`

func parsedToEvent(t *testing.T, ics string) Event {
	t.Helper()
	parsed, err := ParseCalendarObject(ics)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ev := parsed.Master
	ev.ID = "test-event"
	ev.CalendarID = "test-cal"
	return ev
}

func TestExpand_WeeklyMWF_Count(t *testing.T) {
	ev := parsedToEvent(t, weeklyMWFICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(inst) != 3 {
		t.Errorf("got %d instances, want 3 (COUNT=3)", len(inst))
	}
	// Verify they're sorted ascending.
	for i := 1; i < len(inst); i++ {
		if inst[i].InstanceStartUnix < inst[i-1].InstanceStartUnix {
			t.Errorf("instances not sorted at %d", i)
		}
	}
}

func TestExpand_MonthlyFirstMon_Count(t *testing.T) {
	ev := parsedToEvent(t, monthlyFirstMonICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(inst) != 3 {
		t.Errorf("got %d instances, want 3 (COUNT=3)", len(inst))
	}
}

func TestExpand_DailyUntil(t *testing.T) {
	ev := parsedToEvent(t, dailyUntilICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// DTSTART = Nov 10 08:00, UNTIL = Nov 14 08:00 → 5 occurrences (10, 11, 12, 13, 14).
	if len(inst) != 5 {
		t.Errorf("got %d instances, want 5 (Nov 10–14 inclusive)", len(inst))
	}
}

func TestExpand_NonRecurring_InWindow(t *testing.T) {
	ev := parsedToEvent(t, sampleNonRecurringICS)
	// The sample event is Nov 15 14:00–15:00 UTC.
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 11, 30, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(inst) != 1 {
		t.Fatalf("got %d instances, want 1", len(inst))
	}
	if inst[0].Summary != "Quarterly review" {
		t.Errorf("Summary = %q", inst[0].Summary)
	}
}

func TestExpand_NonRecurring_OutOfWindow(t *testing.T) {
	ev := parsedToEvent(t, sampleNonRecurringICS)
	// Way before the sample event.
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(inst) != 0 {
		t.Errorf("got %d instances, want 0", len(inst))
	}
}
