package backend

import (
	"testing"

	"github.com/hkdb/aerion/internal/contact"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Regression for issue #278: the "All" view (SourceID == "") must include
// contacts that have no email address. It previously routed through the
// email-keyed Search, which silently dropped phone-only / email-less records.
func TestAPI_ListContacts_AllIncludesEmaillessRecords(t *testing.T) {
	api, local, _ := setupAPI(t)

	// Phone-only contact — no email at all.
	rec := &contact.Record{
		ID:     "rec-phone-only",
		Source: "local",
		Kind:   "manual",
		Fn:     "Phone Only Pat",
		Phones: []contact.RecordPhone{{Number: "+1-555-9999"}},
	}
	if err := local.UpsertRecord(rec); err != nil {
		t.Fatalf("seed email-less record: %v", err)
	}
	// A normal emailed contact, to confirm the view still returns those too.
	if err := local.AddOrUpdate("alice@example.com", "Alice"); err != nil {
		t.Fatalf("seed emailed contact: %v", err)
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: ""})
	if err != nil {
		t.Fatalf("ListContacts(All): %v", err)
	}

	var foundPhoneOnly, foundAlice bool
	for _, c := range got {
		switch c.Name {
		case "Phone Only Pat":
			foundPhoneOnly = true
		case "Alice":
			foundAlice = true
		}
	}
	if !foundPhoneOnly {
		t.Fatalf("email-less record missing from 'All' view (got %d contacts)", len(got))
	}
	if !foundAlice {
		t.Fatalf("emailed record missing from 'All' view (got %d contacts)", len(got))
	}
}
