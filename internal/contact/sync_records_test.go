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

// routeRT serves per-path bodies (matched by URL path substring, first hit
// wins) — needed since the Microsoft syncer now also enumerates
// contactFolders before running per-folder delta loops (#278).
type routeRT struct {
	routes []struct{ match, body string }
}

func (r routeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, rt := range r.routes {
		if strings.Contains(req.URL.Path, rt.match) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(rt.body)),
				Header:     make(http.Header),
			}, nil
		}
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

// msRoutes builds the routing table for a Microsoft sync test: a
// contactFolders listing plus a default-folder delta body (folder deltas can
// be appended by callers). Order matters — contactFolders must match before
// the broader /contacts path.
func msRoutes(foldersBody, defaultDeltaBody string) routeRT {
	return routeRT{routes: []struct{ match, body string }{
		{"/contactFolders", foldersBody},
		{"/me/contacts", defaultDeltaBody},
	}}
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
	s.httpClient = &http.Client{Transport: msRoutes(`{"value":[]}`, body)}

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

// Regression for #278: contacts living in user-created contactFolders must
// sync too — /me/contacts alone only covers the default folder. Also proves
// the per-folder delta tokens round-trip as a JSON map.
func TestMicrosoftSyncContactsDelta_SyncsAllContactFolders(t *testing.T) {
	defaultBody := `{
		"value": [{"id":"D1","displayName":"Default Dana","emailAddresses":[{"address":"dana@example.com"}]}],
		"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/contacts/delta?$deltatoken=def"
	}`
	folderBody := `{
		"value": [{"id":"F1C1","displayName":"Folder Fred","emailAddresses":[{"address":"fred@example.com"}]}],
		"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/contactFolders/FOLDER1/contacts/delta?$deltatoken=fol"
	}`
	rt := routeRT{routes: []struct{ match, body string }{
		{"/contactFolders/FOLDER1/contacts", folderBody},
		{"/contactFolders", `{"value":[{"id":"FOLDER1"}]}`},
		{"/me/contacts", defaultBody},
	}}
	s := NewMicrosoftContactsSyncer()
	s.httpClient = &http.Client{Transport: rt}

	res, err := s.SyncContactsDelta("tok", "")
	if err != nil {
		t.Fatalf("SyncContactsDelta: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("want 2 records (default + folder), got %d", len(res.Records))
	}
	if findRecord(res.Records, "Folder Fred") == nil {
		t.Fatal("contact from non-default contactFolder was not synced")
	}
	tokens := parseMSFolderTokens(res.NextSyncToken)
	if tokens[msFolderDefault] == "" || tokens["FOLDER1"] == "" {
		t.Fatalf("expected per-folder delta tokens, got %q", res.NextSyncToken)
	}

	// Second sync with the stored token map must run incrementally
	res2, err := s.SyncContactsDelta("tok", res.NextSyncToken)
	if err != nil {
		t.Fatalf("incremental SyncContactsDelta: %v", err)
	}
	if res2.IsFullSync {
		t.Fatal("expected incremental sync when all folder tokens are present")
	}
}

// A legacy plain-string delta token (pre-folder-sync format) must trigger
// one clean full resync, not an error.
func TestMicrosoftSyncContactsDelta_LegacyTokenFullResync(t *testing.T) {
	body := `{
		"value": [{"id":"A","displayName":"Alice Adams","emailAddresses":[{"address":"alice@example.com"}]}],
		"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/contacts/delta?$deltatoken=xyz"
	}`
	s := NewMicrosoftContactsSyncer()
	s.httpClient = &http.Client{Transport: msRoutes(`{"value":[]}`, body)}

	res, err := s.SyncContactsDelta("tok", "https://graph.microsoft.com/v1.0/me/contacts/delta?$deltatoken=legacy")
	if err != nil {
		t.Fatalf("SyncContactsDelta with legacy token: %v", err)
	}
	if !res.IsFullSync {
		t.Fatal("legacy plain-string token should force a full resync")
	}
	if len(res.Records) != 1 {
		t.Fatalf("want 1 record, got %d", len(res.Records))
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
