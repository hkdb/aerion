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

// #278 followup (BurningTheSky): a single EXDATE property carrying a
// comma-separated list (RFC 5545) used to crash go-ical's RecurrenceSet
// ("error parsing exdate: ... extra text"), dropping the whole calendar.
// splitMultiValueDateLists must let these expand with the listed instances
// actually excluded.

const dailyMultiExdateUTCICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:daily-multi-exdate@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251110T080000Z
DTEND:20251110T083000Z
SUMMARY:Standup
RRULE:FREQ=DAILY;COUNT=5
EXDATE:20251111T080000Z,20251113T080000Z
END:VEVENT
END:VCALENDAR
`

func TestExpand_MultiValueExdate_UTC(t *testing.T) {
	ev := parsedToEvent(t, dailyMultiExdateUTCICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// DAILY COUNT=5 → Nov 10,11,12,13,14; EXDATE drops 11 and 13 → 3 left.
	if len(inst) != 3 {
		t.Fatalf("got %d instances, want 3 (5 minus 2 EXDATEs)", len(inst))
	}
	excluded := map[int64]bool{
		time.Date(2025, 11, 11, 8, 0, 0, 0, time.UTC).Unix(): true,
		time.Date(2025, 11, 13, 8, 0, 0, 0, time.UTC).Unix(): true,
	}
	for _, in := range inst {
		if excluded[in.InstanceStartUnix] {
			t.Errorf("instance at %d should have been excluded by EXDATE", in.InstanceStartUnix)
		}
	}
}

const dailyMultiExdateDateOnlyICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:allday-multi-exdate@example.com
DTSTAMP:20251101T120000Z
DTSTART;VALUE=DATE:20251110
DTEND;VALUE=DATE:20251111
SUMMARY:Reminder
RRULE:FREQ=DAILY;COUNT=5
EXDATE;VALUE=DATE:20251111,20251113
END:VEVENT
END:VCALENDAR
`

func TestExpand_MultiValueExdate_DateOnly(t *testing.T) {
	ev := parsedToEvent(t, dailyMultiExdateDateOnlyICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// All-day DAILY COUNT=5 → Nov 10–14; date-only EXDATE drops 11 and 13.
	if len(inst) != 3 {
		t.Fatalf("got %d instances, want 3 (5 minus 2 date-only EXDATEs)", len(inst))
	}
}

const dailySingleExdateICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:daily-single-exdate@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251110T080000Z
DTEND:20251110T083000Z
SUMMARY:Standup
RRULE:FREQ=DAILY;COUNT=5
EXDATE:20251112T080000Z
END:VEVENT
END:VCALENDAR
`

// Control: a single-value EXDATE (no comma) must behave exactly as before —
// the split helper leaves it untouched.
func TestExpand_SingleValueExdate_Unchanged(t *testing.T) {
	ev := parsedToEvent(t, dailySingleExdateICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// DAILY COUNT=5 minus one EXDATE → 4.
	if len(inst) != 4 {
		t.Fatalf("got %d instances, want 4 (5 minus 1 EXDATE)", len(inst))
	}
}

// Confirms the recurrence library accepts the BYSETPOS form produced by the
// Microsoft relativeMonthly/relativeYearly converter — i.e. "second Thursday of
// the month" actually expands. If the lib rejected BYSETPOS, M365 recurring
// masters would silently fail to expand (the #278 regression).
const secondThursdayMonthlyICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:second-thursday@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251113T090000Z
DTEND:20251113T093000Z
SUMMARY:Monthly sync
RRULE:FREQ=MONTHLY;BYDAY=TH;BYSETPOS=2;COUNT=3
END:VEVENT
END:VCALENDAR
`

func TestExpand_RelativeMonthly_BySetPos(t *testing.T) {
	ev := parsedToEvent(t, secondThursdayMonthlyICS)
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(ev, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// 2nd Thursdays: Nov 13 2025, Dec 11 2025, Jan 8 2026.
	if len(inst) != 3 {
		t.Fatalf("got %d instances, want 3 (2nd Thursday monthly, COUNT=3)", len(inst))
	}
	want := []int64{
		time.Date(2025, 11, 13, 9, 0, 0, 0, time.UTC).Unix(),
		time.Date(2025, 12, 11, 9, 0, 0, 0, time.UTC).Unix(),
		time.Date(2026, 1, 8, 9, 0, 0, 0, time.UTC).Unix(),
	}
	for i, w := range want {
		if inst[i].InstanceStartUnix != w {
			t.Errorf("instance %d: got %d, want %d", i, inst[i].InstanceStartUnix, w)
		}
	}
}
