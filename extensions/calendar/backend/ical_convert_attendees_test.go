package backend

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// Helper: parse a raw ICS blob through the same path buildEvent uses.
func parseICSToVEVENT(t *testing.T, ics string) *ical.Event {
	t.Helper()
	dec := ical.NewDecoder(strings.NewReader(ics))
	cal, err := dec.Decode()
	if err != nil {
		t.Fatalf("ical decode: %v", err)
	}
	events := cal.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 VEVENT, got %d", len(events))
	}
	return &events[0]
}

func TestParseAttendees_BasicShape(t *testing.T) {
	ics := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:evt-1
DTSTAMP:20260101T120000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
SUMMARY:Meeting
ORGANIZER;CN="Alice Org":mailto:alice@example.com
ATTENDEE;CN="Bob B";PARTSTAT=ACCEPTED;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:bob@example.com
ATTENDEE;CN="Carol C";PARTSTAT=NEEDS-ACTION;ROLE=OPT-PARTICIPANT:mailto:carol@example.com
END:VEVENT
END:VCALENDAR
`
	ev := parseICSToVEVENT(t, ics)
	atts := parseAttendeesFromVEVENT(ev)
	if len(atts) != 2 {
		t.Fatalf("expected 2 attendees, got %d", len(atts))
	}
	if atts[0].Email != "bob@example.com" {
		t.Errorf("attendee[0].Email = %q", atts[0].Email)
	}
	if atts[0].CommonName != "Bob B" {
		t.Errorf("attendee[0].CommonName = %q", atts[0].CommonName)
	}
	if atts[0].PartStat != PartStatAccepted {
		t.Errorf("attendee[0].PartStat = %q", atts[0].PartStat)
	}
	if atts[0].Role != RoleReqParticipant {
		t.Errorf("attendee[0].Role = %q", atts[0].Role)
	}
	if !atts[0].RSVP {
		t.Errorf("attendee[0].RSVP should be true")
	}
	if atts[1].Role != RoleOptParticipant {
		t.Errorf("attendee[1].Role = %q (want OPT-PARTICIPANT)", atts[1].Role)
	}

	org := parseOrganizerFromVEVENT(ev)
	if org == nil {
		t.Fatal("expected organizer, got nil")
	}
	if org.Email != "alice@example.com" {
		t.Errorf("organizer.Email = %q", org.Email)
	}
	if org.CommonName != "Alice Org" {
		t.Errorf("organizer.CommonName = %q", org.CommonName)
	}
}

func TestParseAttendees_NoneWhenAbsent(t *testing.T) {
	ics := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:evt-2
DTSTAMP:20260101T120000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
SUMMARY:Solo
END:VEVENT
END:VCALENDAR
`
	ev := parseICSToVEVENT(t, ics)
	if a := parseAttendeesFromVEVENT(ev); a != nil {
		t.Errorf("expected nil attendees, got %+v", a)
	}
	if o := parseOrganizerFromVEVENT(ev); o != nil {
		t.Errorf("expected nil organizer, got %+v", o)
	}
}

func TestParseAttendees_CaseAndDefaults(t *testing.T) {
	// CN missing; PARTSTAT/ROLE/CUTYPE absent → defaults applied.
	// Mixed-case email + uppercase mailto: prefix normalized.
	ics := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:evt-3
DTSTAMP:20260101T120000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
SUMMARY:Defaults
ATTENDEE:MAILTO:Mixed@Example.COM
END:VEVENT
END:VCALENDAR
`
	ev := parseICSToVEVENT(t, ics)
	atts := parseAttendeesFromVEVENT(ev)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attendee, got %d", len(atts))
	}
	if atts[0].Email != "mixed@example.com" {
		t.Errorf("lowercased email expected, got %q", atts[0].Email)
	}
	if atts[0].PartStat != PartStatNeedsAction {
		t.Errorf("default PartStat NEEDS-ACTION expected, got %q", atts[0].PartStat)
	}
	if atts[0].Role != RoleReqParticipant {
		t.Errorf("default Role REQ-PARTICIPANT expected, got %q", atts[0].Role)
	}
	if atts[0].CUType != CUTypeIndividual {
		t.Errorf("default CUType INDIVIDUAL expected, got %q", atts[0].CUType)
	}
}

func TestParseAttendees_ResourceAndScheduleStatus(t *testing.T) {
	ics := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:evt-4
DTSTAMP:20260101T120000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
SUMMARY:Has room
ATTENDEE;CUTYPE=RESOURCE;SCHEDULE-STATUS=1.2:mailto:room-101@example.com
ATTENDEE;CN="Alice";SCHEDULE-STATUS=3.7:mailto:alice@example.com
END:VEVENT
END:VCALENDAR
`
	ev := parseICSToVEVENT(t, ics)
	atts := parseAttendeesFromVEVENT(ev)
	if len(atts) != 2 {
		t.Fatalf("expected 2 attendees, got %d", len(atts))
	}
	if atts[0].CUType != CUTypeResource {
		t.Errorf("expected RESOURCE CUType, got %q", atts[0].CUType)
	}
	if atts[0].ScheduleStatus != "1.2" {
		t.Errorf("expected SCHEDULE-STATUS=1.2 (delivered), got %q", atts[0].ScheduleStatus)
	}
	if atts[1].ScheduleStatus != "3.7" {
		t.Errorf("expected SCHEDULE-STATUS=3.7 (unrecognized), got %q", atts[1].ScheduleStatus)
	}
}

// Round-trip: parse → emit → parse → matches.
func TestAttendees_RoundTrip(t *testing.T) {
	originalICS := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:evt-rt
DTSTAMP:20260101T120000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
SUMMARY:Round-trip
ORGANIZER;CN="Alice O":mailto:alice@example.com
ATTENDEE;CN="Bob B";PARTSTAT=ACCEPTED;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:bob@example.com
ATTENDEE;CN="Carol C";PARTSTAT=NEEDS-ACTION;ROLE=OPT-PARTICIPANT;CUTYPE=INDIVIDUAL:mailto:carol@example.com
END:VEVENT
END:VCALENDAR
`
	ev1 := parseICSToVEVENT(t, originalICS)
	atts1 := parseAttendeesFromVEVENT(ev1)
	org1 := parseOrganizerFromVEVENT(ev1)

	// Build a fresh VEVENT and emit the attendees back out, then re-parse.
	ev2 := ical.NewEvent()
	ev2.Props.SetText(ical.PropUID, "evt-rt")
	attInputs := make([]AttendeeInput, len(atts1))
	for i, a := range atts1 {
		attInputs[i] = AttendeeInput{
			Email:      a.Email,
			CommonName: a.CommonName,
			PartStat:   a.PartStat,
			Role:       a.Role,
			RSVP:       a.RSVP,
			CUType:     a.CUType,
			Delegate:   a.Delegate,
		}
	}
	var orgInput *OrganizerInput
	if org1 != nil {
		orgInput = &OrganizerInput{Email: org1.Email, CommonName: org1.CommonName}
	}
	emitAttendeesIntoVEVENT(ev2, orgInput, attInputs)

	atts2 := parseAttendeesFromVEVENT(ev2)
	if len(atts2) != len(atts1) {
		t.Fatalf("round-trip attendee count: got %d, want %d", len(atts2), len(atts1))
	}
	for i := range atts1 {
		if atts2[i].Email != atts1[i].Email {
			t.Errorf("attendee[%d].Email: %q → %q", i, atts1[i].Email, atts2[i].Email)
		}
		if atts2[i].CommonName != atts1[i].CommonName {
			t.Errorf("attendee[%d].CommonName: %q → %q", i, atts1[i].CommonName, atts2[i].CommonName)
		}
		if atts2[i].PartStat != atts1[i].PartStat {
			t.Errorf("attendee[%d].PartStat: %q → %q", i, atts1[i].PartStat, atts2[i].PartStat)
		}
		// Role REQ-PARTICIPANT is the default and may not be emitted; check
		// only when source role was non-default.
		if atts1[i].Role != RoleReqParticipant && atts2[i].Role != atts1[i].Role {
			t.Errorf("attendee[%d].Role: %q → %q", i, atts1[i].Role, atts2[i].Role)
		}
		if atts2[i].RSVP != atts1[i].RSVP {
			t.Errorf("attendee[%d].RSVP: %v → %v", i, atts1[i].RSVP, atts2[i].RSVP)
		}
	}

	org2 := parseOrganizerFromVEVENT(ev2)
	if org2 == nil {
		t.Fatal("organizer lost in round-trip")
	}
	if org2.Email != org1.Email || org2.CommonName != org1.CommonName {
		t.Errorf("organizer drift: %+v → %+v", org1, org2)
	}
}

// Confirms serializeVEVENT (the production path) emits ATTENDEE lines too.
func TestSerializeVEVENT_EmitsAttendees(t *testing.T) {
	in := EventInput{
		Summary:     "Team sync",
		DTStartUnix: 1738396800, // 2025-02-01T08:00:00Z
		DTEndUnix:   1738400400,
		Organizer:   &OrganizerInput{Email: "Lead@Example.com", CommonName: "Team Lead"},
		Attendees: []AttendeeInput{
			{Email: "bob@example.com", CommonName: "Bob", PartStat: PartStatAccepted, RSVP: true},
			{Email: "Carol@example.com", PartStat: PartStatNeedsAction, Role: RoleOptParticipant},
		},
	}
	icsBlob, err := serializeVEVENT("uid-1", in)
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	if !strings.Contains(icsBlob, "ORGANIZER") || !strings.Contains(icsBlob, "lead@example.com") {
		t.Errorf("expected lowercased ORGANIZER mailto, got:\n%s", icsBlob)
	}
	if !strings.Contains(icsBlob, "ATTENDEE") {
		t.Errorf("expected ATTENDEE lines, got:\n%s", icsBlob)
	}
	if !strings.Contains(icsBlob, "bob@example.com") {
		t.Errorf("missing bob, got:\n%s", icsBlob)
	}
	if !strings.Contains(icsBlob, "carol@example.com") {
		t.Errorf("expected lowercased carol, got:\n%s", icsBlob)
	}
	// Round-trip back through the parser to confirm full fidelity.
	ev := parseICSToVEVENT(t, icsBlob)
	atts := parseAttendeesFromVEVENT(ev)
	if len(atts) != 2 {
		t.Fatalf("round-trip attendee count: got %d, want 2", len(atts))
	}
	if atts[0].PartStat != PartStatAccepted || !atts[0].RSVP {
		t.Errorf("Bob's PartStat/RSVP lost: %+v", atts[0])
	}
	if atts[1].Role != RoleOptParticipant {
		t.Errorf("Carol's OPT-PARTICIPANT role lost: %+v", atts[1])
	}
}
