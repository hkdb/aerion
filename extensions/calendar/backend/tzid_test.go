package backend

import (
	"testing"
	"time"
)

// Outlook-style object: Windows zone ID as TZID, with its own VTIMEZONE.
const windowsTZIDICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VTIMEZONE
TZID:W. Europe Standard Time
BEGIN:STANDARD
DTSTART:19701025T030000
RRULE:FREQ=YEARLY;BYMINUTE=0;BYHOUR=3;BYDAY=-1SU;BYMONTH=10
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:19700329T020000
RRULE:FREQ=YEARLY;BYMINUTE=0;BYHOUR=2;BYDAY=-1SU;BYMONTH=3
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
END:DAYLIGHT
END:VTIMEZONE
BEGIN:VEVENT
UID:windows-tzid@example.com
DTSTAMP:20251001T120000Z
DTSTART;TZID=W. Europe Standard Time:20251020T160000
DTEND;TZID=W. Europe Standard Time:20251020T173000
RRULE:FREQ=WEEKLY;INTERVAL=4;BYDAY=MO
EXDATE;TZID=W. Europe Standard Time:20251215T160000
SUMMARY:Board meeting
END:VEVENT
END:VCALENDAR
`

const unknownTZIDICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:unknown-tzid@example.com
DTSTAMP:20251001T120000Z
DTSTART;TZID=Custom Zone:20251020T160000
DTEND;TZID=Custom Zone:20251020T173000
SUMMARY:Somewhere
END:VEVENT
END:VCALENDAR
`

func TestParseCalendarObject_WindowsTZID(t *testing.T) {
	parsed, err := ParseCalendarObject(windowsTZIDICS)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := parsed.Master.DTStartUnix, time.Date(2025, 10, 20, 14, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Errorf("DTStartUnix = %d, want %d (16:00 CEST)", got, want)
	}
	if got, want := parsed.Master.DTEndUnix, time.Date(2025, 10, 20, 15, 30, 0, 0, time.UTC).Unix(); got != want {
		t.Errorf("DTEndUnix = %d, want %d", got, want)
	}
	if parsed.Master.TZName != "Europe/Berlin" {
		t.Errorf("TZName = %q, want Europe/Berlin", parsed.Master.TZName)
	}
}

func TestParseCalendarObject_UnknownTZIDFallsBackToConfiguredTZ(t *testing.T) {
	SetConfiguredTimezone("America/Los_Angeles")
	t.Cleanup(func() { SetConfiguredTimezone("") })

	parsed, err := ParseCalendarObject(unknownTZIDICS)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := parsed.Master.DTStartUnix, time.Date(2025, 10, 20, 23, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Errorf("DTStartUnix = %d, want %d (16:00 PDT)", got, want)
	}
	if parsed.Master.TZName != "" {
		t.Errorf("TZName = %q, want empty", parsed.Master.TZName)
	}
}

func TestExpand_WindowsTZID(t *testing.T) {
	ev := parsedToEvent(t, windowsTZIDICS)

	tests := []struct {
		name     string
		from, to time.Time
		want     []time.Time
	}{
		{
			name: "winter instance follows CET",
			from: time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
			want: []time.Time{time.Date(2025, 11, 17, 15, 0, 0, 0, time.UTC)},
		},
		{
			name: "EXDATE with Windows TZID removes the instance",
			from: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: nil,
		},
		{
			name: "a year later",
			from: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			want: []time.Time{time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := ExpandInRange(ev, nil, tt.from, tt.to)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			if len(inst) != len(tt.want) {
				t.Fatalf("got %d instances, want %d", len(inst), len(tt.want))
			}
			for i, w := range tt.want {
				if inst[i].InstanceStartUnix != w.Unix() {
					t.Errorf("instance %d starts %s, want %s", i, time.Unix(inst[i].InstanceStartUnix, 0).UTC(), w)
				}
			}
		})
	}
}
