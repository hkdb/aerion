package backend

import (
	"strings"
	"testing"
)

// Google attendee round-trip: googleEvent JSON → ICS blob → googleEvent.
func TestGoogleTranslate_AttendeesRoundTrip(t *testing.T) {
	src := googleEvent{
		ICalUID: "evt-att-google@aerion",
		Status:  "confirmed",
		Summary: "Quarterly review",
		Start: &googleTimePoint{
			DateTime: "2026-06-10T14:00:00Z",
			TimeZone: "UTC",
		},
		End: &googleTimePoint{
			DateTime: "2026-06-10T15:00:00Z",
			TimeZone: "UTC",
		},
		Organizer: &googleAttendee{
			Email:       "lead@example.com",
			DisplayName: "Team Lead",
		},
		Attendees: []googleAttendee{
			{Email: "bob@example.com", DisplayName: "Bob B", ResponseStatus: "accepted"},
			{Email: "carol@example.com", DisplayName: "Carol C", ResponseStatus: "needsAction", Optional: true},
			{Email: "room@example.com", DisplayName: "Conf Room 1", Resource: true},
		},
	}

	blob, err := translateGoogleEventToICS(src)
	if err != nil {
		t.Fatalf("translateGoogleEventToICS: %v", err)
	}
	if !strings.Contains(blob, "ATTENDEE") || !strings.Contains(blob, "ORGANIZER") {
		t.Errorf("blob missing ATTENDEE/ORGANIZER lines:\n%s", blob)
	}
	if !strings.Contains(blob, "bob@example.com") {
		t.Errorf("blob missing bob:\n%s", blob)
	}
	if !strings.Contains(blob, "PARTSTAT=ACCEPTED") {
		t.Errorf("blob missing ACCEPTED partstat:\n%s", blob)
	}
	if !strings.Contains(blob, "ROLE=OPT-PARTICIPANT") {
		t.Errorf("blob missing OPT-PARTICIPANT (carol):\n%s", blob)
	}
	if !strings.Contains(blob, "CUTYPE=RESOURCE") {
		t.Errorf("blob missing RESOURCE CUType (room):\n%s", blob)
	}

	back, err := translateICSToGoogleJSON(blob)
	if err != nil {
		t.Fatalf("translateICSToGoogleJSON: %v", err)
	}
	if back.Organizer == nil || back.Organizer.Email != "lead@example.com" {
		t.Errorf("organizer round-trip: %+v", back.Organizer)
	}
	if len(back.Attendees) != 3 {
		t.Fatalf("attendees: got %d, want 3", len(back.Attendees))
	}
	if back.Attendees[0].Email != "bob@example.com" || back.Attendees[0].ResponseStatus != "accepted" {
		t.Errorf("bob round-trip: %+v", back.Attendees[0])
	}
	if !back.Attendees[1].Optional {
		t.Errorf("carol optional flag lost: %+v", back.Attendees[1])
	}
	if !back.Attendees[2].Resource {
		t.Errorf("room resource flag lost: %+v", back.Attendees[2])
	}
}

// Microsoft attendee round-trip: graphEvent JSON → ICS blob → graphEvent.
func TestMicrosoftTranslate_AttendeesRoundTrip(t *testing.T) {
	src := graphEvent{
		ICalUID: "evt-att-graph@aerion",
		Subject: "Quarterly review",
		Start: &graphTimePoint{
			DateTime: "2026-06-10T14:00:00.0000000",
			TimeZone: "UTC",
		},
		End: &graphTimePoint{
			DateTime: "2026-06-10T15:00:00.0000000",
			TimeZone: "UTC",
		},
		Organizer: &graphRecipient{
			EmailAddress: graphEmailAddress{Address: "lead@example.com", Name: "Team Lead"},
		},
		Attendees: []graphAttendee{
			{
				EmailAddress: graphEmailAddress{Address: "bob@example.com", Name: "Bob B"},
				Type:         "required",
				Status:       &graphResponseStatus{Response: "accepted"},
			},
			{
				EmailAddress: graphEmailAddress{Address: "carol@example.com", Name: "Carol C"},
				Type:         "optional",
				Status:       &graphResponseStatus{Response: "tentativelyAccepted"},
			},
			{
				EmailAddress: graphEmailAddress{Address: "room@example.com", Name: "Conf Room 1"},
				Type:         "resource",
				Status:       &graphResponseStatus{Response: "none"},
			},
		},
	}

	blob, err := translateGraphEventToICS(src)
	if err != nil {
		t.Fatalf("translateGraphEventToICS: %v", err)
	}
	if !strings.Contains(blob, "ATTENDEE") || !strings.Contains(blob, "ORGANIZER") {
		t.Errorf("blob missing ATTENDEE/ORGANIZER lines:\n%s", blob)
	}
	if !strings.Contains(blob, "PARTSTAT=ACCEPTED") {
		t.Errorf("blob missing ACCEPTED partstat:\n%s", blob)
	}
	if !strings.Contains(blob, "PARTSTAT=TENTATIVE") {
		t.Errorf("blob missing TENTATIVE partstat:\n%s", blob)
	}
	if !strings.Contains(blob, "ROLE=OPT-PARTICIPANT") {
		t.Errorf("blob missing OPT-PARTICIPANT:\n%s", blob)
	}
	if !strings.Contains(blob, "CUTYPE=RESOURCE") {
		t.Errorf("blob missing RESOURCE CUType:\n%s", blob)
	}

	back, err := translateICSToGraphEvent(blob)
	if err != nil {
		t.Fatalf("translateICSToGraphEvent: %v", err)
	}
	if back.Organizer == nil || back.Organizer.EmailAddress.Address != "lead@example.com" {
		t.Errorf("organizer round-trip: %+v", back.Organizer)
	}
	if len(back.Attendees) != 3 {
		t.Fatalf("attendees: got %d, want 3", len(back.Attendees))
	}
	if back.Attendees[0].Type != "required" || back.Attendees[0].Status == nil || back.Attendees[0].Status.Response != "accepted" {
		t.Errorf("bob round-trip: %+v", back.Attendees[0])
	}
	if back.Attendees[1].Type != "optional" || back.Attendees[1].Status.Response != "tentativelyAccepted" {
		t.Errorf("carol round-trip: %+v", back.Attendees[1])
	}
	if back.Attendees[2].Type != "resource" || back.Attendees[2].Status.Response != "notResponded" {
		t.Errorf("room round-trip: %+v (note: Graph `none` collapses to NEEDS-ACTION which re-emits as notResponded)", back.Attendees[2])
	}
}
