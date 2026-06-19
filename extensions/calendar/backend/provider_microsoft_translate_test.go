package backend

import (
	"strings"
	"testing"
)

// graphRecurrenceToRRule must cover all 6 Microsoft Graph pattern types and the
// range. The previous converter dropped relativeMonthly/relativeYearly (index →
// BYSETPOS), yearly month → BYMONTH, and WKST — so recurring series were stored
// as dead non-recurring stubs at their origin date (#278).
func TestGraphRecurrenceToRRule(t *testing.T) {
	tests := []struct {
		name string
		rec  *graphRecurrence
		want string
	}{
		{"nil", nil, ""},
		{"unknown type", &graphRecurrence{Pattern: graphPattern{Type: "weird", Interval: 1}}, ""},
		{
			"daily",
			&graphRecurrence{Pattern: graphPattern{Type: "daily", Interval: 1}, Range: graphRange{Type: "noEnd"}},
			"FREQ=DAILY",
		},
		{
			"daily interval+count",
			&graphRecurrence{Pattern: graphPattern{Type: "daily", Interval: 3}, Range: graphRange{Type: "numbered", NumberOfOccurrences: 5}},
			"FREQ=DAILY;INTERVAL=3;COUNT=5",
		},
		{
			"weekly with WKST + until",
			&graphRecurrence{
				Pattern: graphPattern{Type: "weekly", Interval: 2, DaysOfWeek: []string{"monday", "wednesday"}, FirstDayOfWeek: "sunday"},
				Range:   graphRange{Type: "endDate", EndDate: "2026-12-31"},
			},
			"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;WKST=SU;UNTIL=20261231T235959Z",
		},
		{
			"absoluteMonthly",
			&graphRecurrence{Pattern: graphPattern{Type: "absoluteMonthly", Interval: 3, DayOfMonth: 15}, Range: graphRange{Type: "noEnd"}},
			"FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=15",
		},
		{
			"relativeMonthly second Thursday",
			&graphRecurrence{Pattern: graphPattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"thursday"}, Index: "second"}, Range: graphRange{Type: "noEnd"}},
			"FREQ=MONTHLY;BYDAY=TH;BYSETPOS=2",
		},
		{
			"relativeMonthly last Friday",
			&graphRecurrence{Pattern: graphPattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"friday"}, Index: "last"}, Range: graphRange{Type: "noEnd"}},
			"FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1",
		},
		{
			"relativeMonthly default index (first)",
			&graphRecurrence{Pattern: graphPattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"monday"}}, Range: graphRange{Type: "noEnd"}},
			"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=1",
		},
		{
			"absoluteYearly March 15",
			&graphRecurrence{Pattern: graphPattern{Type: "absoluteYearly", Interval: 1, Month: 3, DayOfMonth: 15}, Range: graphRange{Type: "noEnd"}},
			"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=15",
		},
		{
			"relativeYearly last Wednesday of November",
			&graphRecurrence{Pattern: graphPattern{Type: "relativeYearly", Interval: 1, Month: 11, DaysOfWeek: []string{"wednesday"}, Index: "last"}, Range: graphRange{Type: "noEnd"}},
			"FREQ=YEARLY;BYMONTH=11;BYDAY=WE;BYSETPOS=-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphRecurrenceToRRule(tt.rec)
			if got != tt.want {
				t.Errorf("graphRecurrenceToRRule()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// Regression (#278): a Graph event whose subject/body/location contains CRLF
// (Outlook HTML bodies do) must still translate. go-ical's encoder rejects raw
// CR/LF; icsText normalizes them so the event isn't dropped.
func TestMicrosoftTranslate_CRLFContentEncodes(t *testing.T) {
	src := graphEvent{
		ICalUID:  "crlf@example.com",
		Subject:  "Subject with\r\na newline",
		Body:     &graphBody{Content: "<html>\r\nline1\r\nline2\r\n</html>"},
		Location: &graphLocation{DisplayName: "Room\r\n123"},
		Start:    &graphTimePoint{DateTime: "2026-01-01T09:00:00.0000000", TimeZone: "UTC"},
		End:      &graphTimePoint{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
	}
	blob, err := translateGraphEventToICS(src)
	if err != nil {
		t.Fatalf("translate failed on CRLF content: %v", err)
	}
	if _, perr := ParseCalendarObject(blob); perr != nil {
		t.Fatalf("translated blob is not parseable: %v", perr)
	}
}

// Graph showAs ⇄ iCal TRANSP (2-state). showAs "free" → TRANSPARENT and back to
// showAs "free"; anything else → busy.
func TestMicrosoftTranslate_ShowAs(t *testing.T) {
	mk := func(showAs string) graphEvent {
		return graphEvent{
			ICalUID: "u", Subject: "s", ShowAs: showAs,
			Start: &graphTimePoint{DateTime: "2026-01-01T09:00:00.0000000", TimeZone: "UTC"},
			End:   &graphTimePoint{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
		}
	}

	freeBlob, err := translateGraphEventToICS(mk("free"))
	if err != nil {
		t.Fatalf("translate free: %v", err)
	}
	if !strings.Contains(freeBlob, "TRANSP:TRANSPARENT") {
		t.Errorf("showAs=free should map to TRANSP:TRANSPARENT:\n%s", freeBlob)
	}
	if g, _ := translateICSToGraphEvent(freeBlob); g.ShowAs != "free" {
		t.Errorf("free ICS → showAs %q, want free", g.ShowAs)
	}

	busyBlob, err := translateGraphEventToICS(mk("busy"))
	if err != nil {
		t.Fatalf("translate busy: %v", err)
	}
	if strings.Contains(busyBlob, "TRANSP:TRANSPARENT") {
		t.Errorf("showAs=busy should not be TRANSPARENT:\n%s", busyBlob)
	}
	if g, _ := translateICSToGraphEvent(busyBlob); g.ShowAs != "busy" {
		t.Errorf("busy ICS → showAs %q, want busy", g.ShowAs)
	}
}

// Graph sensitivity ⇄ iCal CLASS (3-state).
func TestMicrosoftTranslate_Sensitivity(t *testing.T) {
	mk := func(sens string) graphEvent {
		return graphEvent{
			ICalUID: "u", Subject: "s", Sensitivity: sens,
			Start: &graphTimePoint{DateTime: "2026-01-01T09:00:00.0000000", TimeZone: "UTC"},
			End:   &graphTimePoint{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
		}
	}
	cases := []struct{ sens, wantClass, wantSens string }{
		{"normal", "", "normal"},
		{"private", "CLASS:PRIVATE", "private"},
		{"confidential", "CLASS:CONFIDENTIAL", "confidential"},
	}
	for _, c := range cases {
		blob, err := translateGraphEventToICS(mk(c.sens))
		if err != nil {
			t.Fatalf("translate %s: %v", c.sens, err)
		}
		if c.wantClass == "" && strings.Contains(blob, "CLASS:") {
			t.Errorf("%s should omit CLASS:\n%s", c.sens, blob)
		}
		if c.wantClass != "" && !strings.Contains(blob, c.wantClass) {
			t.Errorf("%s → %s missing:\n%s", c.sens, c.wantClass, blob)
		}
		if g, _ := translateICSToGraphEvent(blob); g.Sensitivity != c.wantSens {
			t.Errorf("%s round-trip sensitivity = %q, want %q", c.sens, g.Sensitivity, c.wantSens)
		}
	}
}
