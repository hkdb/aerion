package backend

// Event CRUD test coverage. Focuses on the serializer's ICS shape (round
// trips through go-ical correctly) + the RRULE clamping helper, since
// those are the load-bearing pieces that downstream rrule_expand.go and
// alarm.go consume. Full scope-edge tests (this/this-and-future across
// past+future overrides) need a wired Store with DB; tracked as a
// follow-up integration test.

import (
	"strings"
	"testing"
	"time"
)

func TestSerializeVEVENT_NonRecurring(t *testing.T) {
	uid := "test-uid@aerion-local"
	start := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix()

	blob, err := serializeVEVENT(uid, EventInput{
		CalendarID:  "cal1",
		Summary:     "Lunch",
		Description: "with Alice",
		Location:    "Cafe",
		DTStartUnix: start,
		DTEndUnix:   end,
	})
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	if !strings.Contains(blob, "BEGIN:VEVENT") {
		t.Errorf("blob missing VEVENT marker:\n%s", blob)
	}
	if !strings.Contains(blob, "UID:"+uid) {
		t.Errorf("blob missing UID:\n%s", blob)
	}
	if !strings.Contains(blob, "SUMMARY:Lunch") {
		t.Errorf("blob missing SUMMARY:\n%s", blob)
	}
	if !strings.Contains(blob, "DESCRIPTION:with Alice") {
		t.Errorf("blob missing DESCRIPTION:\n%s", blob)
	}
	if !strings.Contains(blob, "LOCATION:Cafe") {
		t.Errorf("blob missing LOCATION:\n%s", blob)
	}
	// Round-trip via ExtractAlarms to confirm parsability with no reminders.
	ev := Event{
		ID:          "ev1",
		Summary:     "Lunch",
		DTStartUnix: start,
		DTEndUnix:   end,
		ICSBlob:     blob,
	}
	alarms, err := ExtractAlarms(ev, nil, []EventInstance{
		{Event: ev, InstanceStartUnix: start, InstanceEndUnix: end},
	})
	if err != nil {
		t.Fatalf("ExtractAlarms on serialized blob: %v", err)
	}
	if len(alarms) != 0 {
		t.Errorf("non-recurring no-reminder event should produce 0 alarms, got %d", len(alarms))
	}
}

func TestSerializeVEVENT_AllDay(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC).Unix()
	blob, err := serializeVEVENT("alld@aerion", EventInput{
		CalendarID:  "cal1",
		Summary:     "Holiday",
		DTStartUnix: start,
		DTEndUnix:   end,
		IsAllDay:    true,
	})
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	// DATE form should appear (no T separator, has VALUE=DATE param).
	if !strings.Contains(blob, "DTSTART;VALUE=DATE:20260605") {
		t.Errorf("blob missing DATE-form DTSTART:\n%s", blob)
	}
}

func TestSerializeVEVENT_Recurring(t *testing.T) {
	start := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix()
	blob, err := serializeVEVENT("rec@aerion", EventInput{
		CalendarID:  "cal1",
		Summary:     "Standup",
		DTStartUnix: start,
		DTEndUnix:   end,
		Recurrence: &RecurrenceSpec{
			Freq:  "WEEKLY",
			Count: 4,
		},
	})
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	if !strings.Contains(blob, "RRULE:FREQ=WEEKLY;COUNT=4") {
		t.Errorf("blob missing RRULE:\n%s", blob)
	}
}

func TestSerializeVEVENT_WithReminder(t *testing.T) {
	start := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 6, 5, 15, 0, 0, 0, time.UTC).Unix()
	blob, err := serializeVEVENT("rem@aerion", EventInput{
		CalendarID:  "cal1",
		Summary:     "Doctor",
		DTStartUnix: start,
		DTEndUnix:   end,
		Reminder:    &ReminderSpec{OffsetMinutes: 15},
	})
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	if !strings.Contains(blob, "BEGIN:VALARM") {
		t.Errorf("blob missing VALARM:\n%s", blob)
	}
	if !strings.Contains(blob, "TRIGGER:-PT15M") {
		t.Errorf("blob missing TRIGGER:\n%s", blob)
	}

	// Round-trip via ExtractAlarms to confirm the alarm parses correctly.
	ev := Event{
		ID:          "ev1",
		Summary:     "Doctor",
		DTStartUnix: start,
		DTEndUnix:   end,
		ICSBlob:     blob,
	}
	alarms, err := ExtractAlarms(ev, nil, []EventInstance{
		{Event: ev, InstanceStartUnix: start, InstanceEndUnix: end},
	})
	if err != nil {
		t.Fatalf("ExtractAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(alarms))
	}
	want := start - 15*60
	if alarms[0].TriggerUnix != want {
		t.Errorf("alarm trigger: got %d want %d", alarms[0].TriggerUnix, want)
	}
}

func TestRRuleText(t *testing.T) {
	cases := []struct {
		name string
		spec *RecurrenceSpec
		want string
	}{
		{"nil", nil, ""},
		{"daily", &RecurrenceSpec{Freq: "DAILY"}, "FREQ=DAILY"},
		{"weekly with count", &RecurrenceSpec{Freq: "WEEKLY", Count: 4}, "FREQ=WEEKLY;COUNT=4"},
		{"weekly with until", &RecurrenceSpec{
			Freq:      "WEEKLY",
			UntilUnix: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC).Unix(),
		}, "FREQ=WEEKLY;UNTIL=20261231T000000Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rruleText(c.spec)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestClampRRuleUntil(t *testing.T) {
	until := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			"add UNTIL to open-ended weekly",
			"FREQ=WEEKLY",
			"FREQ=WEEKLY;UNTIL=20260601T000000Z",
		},
		{
			"replace COUNT with UNTIL",
			"FREQ=WEEKLY;COUNT=10",
			"FREQ=WEEKLY;UNTIL=20260601T000000Z",
		},
		{
			"replace existing UNTIL",
			"FREQ=WEEKLY;UNTIL=20271231T000000Z",
			"FREQ=WEEKLY;UNTIL=20260601T000000Z",
		},
		{
			"empty stays empty",
			"",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampRRuleUntil(c.input, until)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestValidateInput(t *testing.T) {
	base := EventInput{
		CalendarID:  "cal1",
		Summary:     "Test",
		DTStartUnix: 1000,
		DTEndUnix:   2000,
	}

	if err := validateInput(base); err != nil {
		t.Errorf("base valid input rejected: %v", err)
	}

	bad := base
	bad.CalendarID = ""
	if err := validateInput(bad); err == nil {
		t.Error("missing CalendarID accepted")
	}

	bad = base
	bad.Summary = ""
	if err := validateInput(bad); err == nil {
		t.Error("empty summary accepted")
	}

	bad = base
	bad.DTEndUnix = bad.DTStartUnix - 1
	if err := validateInput(bad); err == nil {
		t.Error("DTEnd < DTStart accepted")
	}

	bad = base
	bad.Recurrence = &RecurrenceSpec{Freq: "BOGUS"}
	if err := validateInput(bad); err == nil {
		t.Error("invalid freq accepted")
	}

	bad = base
	bad.Recurrence = &RecurrenceSpec{Freq: "DAILY", UntilUnix: 1, Count: 1}
	if err := validateInput(bad); err == nil {
		t.Error("both UntilUnix and Count accepted")
	}
}
