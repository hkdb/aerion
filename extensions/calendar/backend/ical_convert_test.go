package backend

import (
	"strings"
	"testing"
)

const sampleNonRecurringICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:non-recurring-1@example.com
DTSTAMP:20251101T120000Z
DTSTART:20251115T140000Z
DTEND:20251115T150000Z
SUMMARY:Quarterly review
LOCATION:Boardroom B
DESCRIPTION:Q4 financials walkthrough.
END:VEVENT
END:VCALENDAR
`

const sampleAllDayICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:allday-1@example.com
DTSTAMP:20251101T120000Z
DTSTART;VALUE=DATE:20251225
DTEND;VALUE=DATE:20251226
SUMMARY:Christmas Day
END:VEVENT
END:VCALENDAR
`

const sampleRecurringICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:weekly-1@example.com
DTSTAMP:20251101T120000Z
DTSTART;TZID=America/New_York:20251103T090000
DTEND;TZID=America/New_York:20251103T093000
SUMMARY:Standup
RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR
END:VEVENT
END:VCALENDAR
`

func TestParseCalendarObject_NonRecurring(t *testing.T) {
	parsed, err := ParseCalendarObject(sampleNonRecurringICS)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Master.UID != "non-recurring-1@example.com" {
		t.Errorf("UID = %q, want non-recurring-1@example.com", parsed.Master.UID)
	}
	if parsed.Master.Summary != "Quarterly review" {
		t.Errorf("Summary = %q", parsed.Master.Summary)
	}
	if parsed.Master.Location != "Boardroom B" {
		t.Errorf("Location = %q", parsed.Master.Location)
	}
	if parsed.Master.IsAllDay {
		t.Errorf("IsAllDay = true, want false")
	}
	if parsed.Master.RRuleText != "" {
		t.Errorf("RRuleText = %q, want empty (non-recurring)", parsed.Master.RRuleText)
	}
	if parsed.Master.DTEndUnix <= parsed.Master.DTStartUnix {
		t.Errorf("DTEndUnix (%d) should be > DTStartUnix (%d)", parsed.Master.DTEndUnix, parsed.Master.DTStartUnix)
	}
	if len(parsed.Overrides) != 0 {
		t.Errorf("Overrides = %d, want 0", len(parsed.Overrides))
	}
}

func TestParseCalendarObject_AllDay(t *testing.T) {
	parsed, err := ParseCalendarObject(sampleAllDayICS)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.Master.IsAllDay {
		t.Errorf("IsAllDay = false, want true")
	}
	if parsed.Master.Summary != "Christmas Day" {
		t.Errorf("Summary = %q", parsed.Master.Summary)
	}
}

func TestParseCalendarObject_Recurring(t *testing.T) {
	parsed, err := ParseCalendarObject(sampleRecurringICS)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Master.RRuleText == "" {
		t.Fatalf("RRuleText empty, want a value")
	}
	if !strings.Contains(parsed.Master.RRuleText, "FREQ=WEEKLY") {
		t.Errorf("RRuleText = %q, missing FREQ=WEEKLY", parsed.Master.RRuleText)
	}
	if parsed.Master.TZName != "America/New_York" {
		t.Errorf("TZName = %q, want America/New_York", parsed.Master.TZName)
	}
	if !strings.Contains(parsed.Master.ICSBlob, "RRULE:FREQ=WEEKLY") {
		t.Errorf("ICSBlob should preserve RRULE for re-parse")
	}
}

func TestParseCalendarObject_EmptyInput(t *testing.T) {
	_, err := ParseCalendarObject("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}
