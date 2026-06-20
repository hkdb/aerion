package contact

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubRT serves a single canned JSON body for every request, so a syncer's
// delta loop terminates after one page (the body carries a final delta/sync
// token and no next-page link).
type stubRT struct{ body string }

func (r stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     make(http.Header),
	}, nil
}

func findRecord(recs []SyncedRecord, fn string) *Record {
	for _, r := range recs {
		if r.Record != nil && r.Record.Fn == fn {
			return r.Record
		}
	}
	return nil
}

// Regression for issue #278: Microsoft contacts with no email address must be
// retained (phone-only contacts are valid records), and phones must map.
func TestMicrosoftSyncContactsDelta_RetainsEmaillessAndMapsPhones(t *testing.T) {
	body := `{
		"value": [
			{"id":"A","displayName":"Alice Adams","givenName":"Alice","surname":"Adams","emailAddresses":[{"address":"alice@example.com"}],"businessPhones":["+1-555-0001"]},
			{"id":"B","displayName":"Bob Builder","mobilePhone":"+1-555-0002"},
			{"id":"C","@removed":{"reason":"deleted"}}
		],
		"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/contacts/delta?$deltatoken=xyz"
	}`
	s := NewMicrosoftContactsSyncer()
	s.httpClient = &http.Client{Transport: stubRT{body}}

	res, err := s.SyncContactsDelta("tok", "")
	if err != nil {
		t.Fatalf("SyncContactsDelta: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("want 2 records (email-less retained), got %d", len(res.Records))
	}

	alice := findRecord(res.Records, "Alice Adams")
	if alice == nil || len(alice.Emails) != 1 || alice.Emails[0].Email != "alice@example.com" {
		t.Fatalf("Alice email not mapped: %+v", alice)
	}
	if len(alice.Phones) != 1 || alice.Phones[0].Number != "+1-555-0001" || alice.Phones[0].PhoneType != "work" {
		t.Fatalf("Alice business phone not mapped: %+v", alice.Phones)
	}

	bob := findRecord(res.Records, "Bob Builder")
	if bob == nil {
		t.Fatal("email-less contact Bob was dropped")
	}
	if len(bob.Emails) != 0 {
		t.Fatalf("Bob should have no email, got %+v", bob.Emails)
	}
	if len(bob.Phones) != 1 || bob.Phones[0].Number != "+1-555-0002" || bob.Phones[0].PhoneType != "cell" {
		t.Fatalf("Bob mobile phone not mapped: %+v", bob.Phones)
	}

	if len(res.DeletedIDs) != 1 || res.DeletedIDs[0] != "C" {
		t.Fatalf("want DeletedIDs [C], got %v", res.DeletedIDs)
	}
	if res.NextSyncToken == "" {
		t.Fatal("expected a delta token to be carried forward")
	}
}

// Parity for Google: email-less People connections retained, phones mapped.
func TestGoogleSyncContactsDelta_RetainsEmaillessAndMapsPhones(t *testing.T) {
	body := `{
		"connections": [
			{"resourceName":"people/A","names":[{"displayName":"Alice Adams","givenName":"Alice","familyName":"Adams"}],"emailAddresses":[{"value":"alice@example.com"}],"phoneNumbers":[{"value":"+1-555-0001","type":"work"}]},
			{"resourceName":"people/B","names":[{"displayName":"Bob Builder"}],"phoneNumbers":[{"value":"+1-555-0002","type":"mobile"}]},
			{"resourceName":"people/C","metadata":{"deleted":true}}
		],
		"nextSyncToken":"synctok"
	}`
	s := NewGoogleContactsSyncer()
	s.httpClient = &http.Client{Transport: stubRT{body}}

	res, err := s.SyncContactsDelta("tok", "")
	if err != nil {
		t.Fatalf("SyncContactsDelta: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("want 2 records (email-less retained), got %d", len(res.Records))
	}

	bob := findRecord(res.Records, "Bob Builder")
	if bob == nil {
		t.Fatal("email-less contact Bob was dropped")
	}
	if len(bob.Emails) != 0 {
		t.Fatalf("Bob should have no email, got %+v", bob.Emails)
	}
	if len(bob.Phones) != 1 || bob.Phones[0].Number != "+1-555-0002" {
		t.Fatalf("Bob phone not mapped: %+v", bob.Phones)
	}

	if len(res.DeletedIDs) != 1 || res.DeletedIDs[0] != "people/C" {
		t.Fatalf("want DeletedIDs [people/C], got %v", res.DeletedIDs)
	}
	if res.NextSyncToken != "synctok" {
		t.Fatalf("want NextSyncToken synctok, got %q", res.NextSyncToken)
	}
}
