package backend

import (
	"strings"
	"testing"

	"github.com/hkdb/aerion/internal/contact"
	"github.com/rs/zerolog"
)

func TestRecordToGooglePerson_BasicFields(t *testing.T) {
	rec := &contact.Record{
		Fn:       "Alice Wonder",
		NGiven:   "Alice",
		NFamily:  "Wonder",
		Nickname: "Ali",
		Org:      "Acme",
		Title:    "Engineer",
		Note:     "Met at conf",
		Bday:     "1990-04-15",
		Emails: []contact.RecordEmail{
			{Email: "alice@example.com", EmailType: "work", IsPrimary: true},
			{Email: "alice@personal.com", EmailType: "home"},
		},
		Phones: []contact.RecordPhone{
			{Number: "+1-555-0100", PhoneType: "mobile"},
		},
		Addresses: []contact.RecordAddress{
			{AddrType: "home", Street: "1 Way", City: "SF", Region: "CA", Postcode: "94110", Country: "US"},
		},
		URLs: []contact.RecordURL{
			{URL: "https://example.com", URLType: "work"},
		},
		IMPPs: []contact.RecordIMPP{
			{Handle: "xmpp:alice@xmpp.example.com", IMPPType: "work"},
		},
	}

	p := recordToGooglePerson(rec, zerolog.Nop())
	if len(p.Names) != 1 || p.Names[0].DisplayName != "Alice Wonder" {
		t.Errorf("names: got %+v", p.Names)
	}
	if len(p.Nicknames) != 1 || p.Nicknames[0].Value != "Ali" {
		t.Errorf("nicknames: got %+v", p.Nicknames)
	}
	if len(p.Organizations) != 1 || p.Organizations[0].Name != "Acme" {
		t.Errorf("organizations: got %+v", p.Organizations)
	}
	if len(p.Biographies) != 1 || p.Biographies[0].Value != "Met at conf" {
		t.Errorf("biographies: got %+v", p.Biographies)
	}
	if len(p.Birthdays) != 1 || p.Birthdays[0].Date == nil || p.Birthdays[0].Date.Year != 1990 || p.Birthdays[0].Date.Month != 4 || p.Birthdays[0].Date.Day != 15 {
		t.Errorf("birthdays: got %+v", p.Birthdays)
	}
	if len(p.EmailAddresses) != 2 {
		t.Errorf("emails: got %+v", p.EmailAddresses)
	}
	if len(p.PhoneNumbers) != 1 || p.PhoneNumbers[0].Value != "+1-555-0100" {
		t.Errorf("phones: got %+v", p.PhoneNumbers)
	}
	if len(p.Addresses) != 1 || p.Addresses[0].City != "SF" {
		t.Errorf("addresses: got %+v", p.Addresses)
	}
	if len(p.URLs) != 1 || p.URLs[0].Value != "https://example.com" {
		t.Errorf("urls: got %+v", p.URLs)
	}
	if len(p.IMClients) != 1 || p.IMClients[0].Username != "alice@xmpp.example.com" || p.IMClients[0].Protocol != "xmpp" {
		t.Errorf("imClients: got %+v", p.IMClients)
	}
}

func TestRecordToGooglePerson_NoBirthdayOnEmpty(t *testing.T) {
	p := recordToGooglePerson(&contact.Record{Fn: "Bob"}, zerolog.Nop())
	if len(p.Birthdays) != 0 {
		t.Errorf("birthdays should be empty when Bday is empty, got %+v", p.Birthdays)
	}
}

func TestRecordToGooglePerson_PartialBirthday(t *testing.T) {
	rec := &contact.Record{Fn: "X", Bday: "--04-15"} // vCard no-year shorthand
	p := recordToGooglePerson(rec, zerolog.Nop())
	if len(p.Birthdays) != 1 || p.Birthdays[0].Date == nil || p.Birthdays[0].Date.Year != 0 {
		t.Errorf("expected partial birthday with year=0, got %+v", p.Birthdays)
	}
}

func TestGooglePersonToRecord_RoundTrip(t *testing.T) {
	original := &contact.Record{
		Fn:      "Alice",
		NGiven:  "Alice",
		NFamily: "Wonder",
		Emails:  []contact.RecordEmail{{Email: "ALICE@EXAMPLE.COM"}},
		Phones:  []contact.RecordPhone{{Number: "+1 555 0100", PhoneType: "mobile"}},
	}
	p := recordToGooglePerson(original, zerolog.Nop())
	back := googlePersonToRecord(p)
	if back.Fn != "Alice" {
		t.Errorf("Fn: got %q", back.Fn)
	}
	// Email should be lowercased on the round-trip.
	if len(back.Emails) != 1 || back.Emails[0].Email != "alice@example.com" {
		t.Errorf("emails: got %+v", back.Emails)
	}
	if len(back.Phones) != 1 || back.Phones[0].Number != "+1 555 0100" {
		t.Errorf("phones: got %+v", back.Phones)
	}
}

func TestGooglePersonToRecord_NilSafe(t *testing.T) {
	if got := googlePersonToRecord(nil); got != nil {
		t.Errorf("nil input should yield nil, got %+v", got)
	}
}

func TestFieldMaskForRecord_CoversAllWritableFields(t *testing.T) {
	mask := fieldMaskForRecord(&contact.Record{Fn: "x"})
	for _, want := range []string{"names", "nicknames", "emailAddresses", "phoneNumbers", "addresses", "urls", "imClients", "organizations", "biographies", "birthdays"} {
		if !strings.Contains(mask, want) {
			t.Errorf("mask missing %q: %q", want, mask)
		}
	}
}

func TestFieldMaskForRecord_NilRecord(t *testing.T) {
	if got := fieldMaskForRecord(nil); got != "" {
		t.Errorf("nil rec: got %q, want empty", got)
	}
}

func TestParseAddressbookGroupID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"google-mycontacts:src-1", ""},
		{"google-group:contactGroups/abc", "contactGroups/abc"},
		{"google-group:contactGroups/myContacts", "contactGroups/myContacts"},
		{"random-string", ""},
	}
	for _, tc := range tests {
		got := parseAddressbookGroupID(tc.in)
		if got != tc.want {
			t.Errorf("parseAddressbookGroupID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEtagFromPerson(t *testing.T) {
	tests := []struct {
		name string
		in   *googlePerson
		want string
	}{
		{"nil", nil, ""},
		{"no metadata", &googlePerson{}, ""},
		{
			"contact source",
			&googlePerson{Metadata: &googlePersonMetadata{Sources: []googlePersonSource{{Type: "CONTACT", ETag: "ETAG"}}}},
			"ETAG",
		},
		{
			"skips non-CONTACT sources",
			&googlePerson{Metadata: &googlePersonMetadata{Sources: []googlePersonSource{
				{Type: "PROFILE", ETag: "WRONG"},
				{Type: "CONTACT", ETag: "RIGHT"},
			}}},
			"RIGHT",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := etagFromPerson(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatGoogleBirthday(t *testing.T) {
	tests := []struct {
		name string
		in   googleBirthday
		want string
	}{
		{"full date", googleBirthday{Date: &googleDate{Year: 1990, Month: 4, Day: 15}}, "1990-04-15"},
		{"no year", googleBirthday{Date: &googleDate{Month: 4, Day: 15}}, "--04-15"},
		{"text only", googleBirthday{Text: "next Tuesday"}, "next Tuesday"},
		{"empty", googleBirthday{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatGoogleBirthday(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitIMPP(t *testing.T) {
	tests := []struct {
		in, wantU, wantP string
	}{
		{"xmpp:alice@x.com", "alice@x.com", "xmpp"},
		{"alice@x.com", "alice@x.com", ""},
		{"sip:bob@y.com", "bob@y.com", "sip"},
		{"", "", ""},
	}
	for _, tc := range tests {
		u, p := splitIMPP(tc.in)
		if u != tc.wantU || p != tc.wantP {
			t.Errorf("splitIMPP(%q) = (%q, %q), want (%q, %q)", tc.in, u, p, tc.wantU, tc.wantP)
		}
	}
}
