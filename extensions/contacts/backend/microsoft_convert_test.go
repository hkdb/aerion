package backend

import (
	"strings"
	"testing"

	"github.com/hkdb/aerion/internal/contact"
	"github.com/rs/zerolog"
)

func TestRecordToMicrosoftContact_BasicFields(t *testing.T) {
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
		},
		Categories: []string{"VIP", "Engineering"},
	}

	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if c.GivenName != "Alice" || c.Surname != "Wonder" || c.DisplayName != "Alice Wonder" {
		t.Errorf("name: got given=%q surname=%q dn=%q", c.GivenName, c.Surname, c.DisplayName)
	}
	if c.NickName != "Ali" {
		t.Errorf("nickname: got %q", c.NickName)
	}
	if c.CompanyName != "Acme" || c.JobTitle != "Engineer" {
		t.Errorf("org/title: got %q / %q", c.CompanyName, c.JobTitle)
	}
	if c.Personal != "Met at conf" {
		t.Errorf("personalNotes: got %q", c.Personal)
	}
	if c.Birthday != "1990-04-15T00:00:00Z" {
		t.Errorf("birthday: got %q", c.Birthday)
	}
	if len(c.EmailAddresses) != 1 || c.EmailAddresses[0].Address != "alice@example.com" || c.EmailAddresses[0].Name != "work" {
		t.Errorf("emails: got %+v", c.EmailAddresses)
	}
	if len(c.Categories) != 2 || c.Categories[0] != "VIP" {
		t.Errorf("categories: got %+v", c.Categories)
	}
}

func TestRecordToMicrosoftContact_PhoneBucketing(t *testing.T) {
	rec := &contact.Record{
		Fn: "X",
		Phones: []contact.RecordPhone{
			{Number: "+1-555-0100", PhoneType: "mobile"},
			{Number: "+1-555-0200", PhoneType: "home"},
			{Number: "+1-555-0300", PhoneType: "work"},
			{Number: "+1-555-0400", PhoneType: "fax"}, // unknown → business catch-all
			{Number: "+1-555-0500", PhoneType: ""},    // empty type → business catch-all
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if c.MobilePhone != "+1-555-0100" {
		t.Errorf("mobile: got %q", c.MobilePhone)
	}
	if len(c.HomePhones) != 1 || c.HomePhones[0] != "+1-555-0200" {
		t.Errorf("home phones: got %+v", c.HomePhones)
	}
	// businessPhones gets work + fax + empty-type → 3 entries.
	if len(c.BusinessPhones) != 3 {
		t.Errorf("business phones: got %+v", c.BusinessPhones)
	}
}

func TestRecordToMicrosoftContact_PhoneNoDropOnUnknownType(t *testing.T) {
	// Any phone with a non-empty number must land in some bucket — never dropped.
	rec := &contact.Record{
		Fn: "X",
		Phones: []contact.RecordPhone{
			{Number: "+1-555-9999", PhoneType: "satellite"},
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if len(c.BusinessPhones) != 1 {
		t.Errorf("unknown-type phone should land in businessPhones: %+v", c)
	}
}

func TestRecordToMicrosoftContact_AddressSlotDistribution(t *testing.T) {
	rec := &contact.Record{
		Fn: "X",
		Addresses: []contact.RecordAddress{
			{AddrType: "home", Street: "1 Home St", City: "HomeCity"},
			{AddrType: "work", Street: "2 Work Ave", City: "WorkCity"},
			{AddrType: "other", Street: "3 Other Way", City: "OtherCity"},
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if c.HomeAddress == nil || c.HomeAddress.City != "HomeCity" {
		t.Errorf("home address: got %+v", c.HomeAddress)
	}
	if c.BusinessAddress == nil || c.BusinessAddress.City != "WorkCity" {
		t.Errorf("business address: got %+v", c.BusinessAddress)
	}
	if c.OtherAddress == nil || c.OtherAddress.City != "OtherCity" {
		t.Errorf("other address: got %+v", c.OtherAddress)
	}
}

func TestRecordToMicrosoftContact_AddressOverflow(t *testing.T) {
	// 4 home addresses → first lands in slot, rest dropped (warn).
	rec := &contact.Record{
		Fn: "X",
		Addresses: []contact.RecordAddress{
			{AddrType: "home", Street: "1"},
			{AddrType: "home", Street: "2"}, // dropped
			{AddrType: "home", Street: "3"}, // dropped
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if c.HomeAddress == nil || c.HomeAddress.Street != "1" {
		t.Errorf("first home should win: got %+v", c.HomeAddress)
	}
}

func TestRecordToMicrosoftContact_URLCollapse(t *testing.T) {
	rec := &contact.Record{
		Fn: "X",
		URLs: []contact.RecordURL{
			{URL: "https://first.example.com", URLType: "work"},
			{URL: "https://second.example.com", URLType: "home"},
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if c.BusinessHomePage != "https://first.example.com" {
		t.Errorf("first URL should win: got %q", c.BusinessHomePage)
	}
}

func TestRecordToMicrosoftContact_IMRoundTrip(t *testing.T) {
	rec := &contact.Record{
		Fn: "X",
		IMPPs: []contact.RecordIMPP{
			{Handle: "xmpp:alice@xmpp.example.com", IMPPType: "work"},
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if len(c.IMAddresses) != 1 || c.IMAddresses[0] != "xmpp:alice@xmpp.example.com" {
		t.Errorf("imAddresses: got %+v", c.IMAddresses)
	}
}

func TestMicrosoftContactToRecord_RoundTripPhonesByBucket(t *testing.T) {
	c := &msContact{
		DisplayName:    "X",
		MobilePhone:    "+1-555-0100",
		HomePhones:     []string{"+1-555-0200"},
		BusinessPhones: []string{"+1-555-0300", "+1-555-0400"},
	}
	rec := microsoftContactToRecord(c)
	if rec == nil {
		t.Fatal("nil record")
	}
	// Order: business, home, mobile (helpers' append order). Type metadata
	// derived from bucket.
	if len(rec.Phones) != 4 {
		t.Fatalf("expected 4 phones, got %d", len(rec.Phones))
	}
	if rec.Phones[0].PhoneType != "work" || rec.Phones[1].PhoneType != "work" {
		t.Errorf("business should map to type=work: %+v", rec.Phones[:2])
	}
	if rec.Phones[2].PhoneType != "home" {
		t.Errorf("home: %+v", rec.Phones[2])
	}
	if rec.Phones[3].PhoneType != "mobile" || rec.Phones[3].Number != "+1-555-0100" {
		t.Errorf("mobile: %+v", rec.Phones[3])
	}
}

func TestMicrosoftContactToRecord_AddressFromSlots(t *testing.T) {
	c := &msContact{
		DisplayName: "X",
		HomeAddress: &msPhysicalAddress{Street: "1 Home", City: "HC", State: "CA"},
		OtherAddress: &msPhysicalAddress{Street: "3 Other", City: "OC"},
	}
	rec := microsoftContactToRecord(c)
	if len(rec.Addresses) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(rec.Addresses))
	}
	if rec.Addresses[0].AddrType != "home" || rec.Addresses[0].Region != "CA" {
		t.Errorf("home addr: got %+v", rec.Addresses[0])
	}
	if rec.Addresses[1].AddrType != "other" {
		t.Errorf("other addr: got %+v", rec.Addresses[1])
	}
}

func TestMicrosoftContactToRecord_NilSafe(t *testing.T) {
	if got := microsoftContactToRecord(nil); got != nil {
		t.Errorf("nil → nil, got %+v", got)
	}
}

func TestParseAddressbookFolderID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"ms-default:src-1", ""},
		{"ms-folder:AAMkAGI2THz", "AAMkAGI2THz"},
		{"random", ""},
	}
	for _, tc := range tests {
		got := parseAddressbookFolderID(tc.in)
		if got != tc.want {
			t.Errorf("parseAddressbookFolderID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMicrosoftBirthday_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantOut string // microsoftBirthdayFromString output
		wantBack string // microsoftBirthdayToString of that output
	}{
		{"full date", "1990-04-15", "1990-04-15T00:00:00Z", "1990-04-15"},
		{"no year shorthand", "--04-15", "1604-04-15T00:00:00Z", "--04-15"},
		{"empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fwd := microsoftBirthdayFromString(tc.in)
			if fwd != tc.wantOut {
				t.Errorf("forward: got %q, want %q", fwd, tc.wantOut)
			}
			if fwd == "" {
				return
			}
			back := microsoftBirthdayToString(fwd)
			if back != tc.wantBack {
				t.Errorf("backward: got %q, want %q", back, tc.wantBack)
			}
		})
	}
}

func TestMicrosoftBirthday_HandlesTimezones(t *testing.T) {
	// Graph may return birthday with a TZ offset; we only care about the date.
	got := microsoftBirthdayToString("1990-04-15T10:30:00+02:00")
	if got != "1990-04-15" {
		t.Errorf("got %q, want 1990-04-15", got)
	}
}

func TestRecordToMicrosoftContact_EmptyRecord(t *testing.T) {
	c := recordToMicrosoftContact(&contact.Record{}, zerolog.Nop())
	if c.DisplayName != "" || len(c.EmailAddresses) != 0 || len(c.Categories) != 0 {
		t.Errorf("empty record should produce empty contact, got %+v", c)
	}
}

func TestRecordToMicrosoftContact_PhoneEmptyNumberSkipped(t *testing.T) {
	rec := &contact.Record{
		Fn: "X",
		Phones: []contact.RecordPhone{
			{Number: "", PhoneType: "home"}, // skipped
			{Number: "+1-555-0100", PhoneType: "home"},
		},
	}
	c := recordToMicrosoftContact(rec, zerolog.Nop())
	if len(c.HomePhones) != 1 {
		t.Errorf("empty phone number should be skipped: %+v", c.HomePhones)
	}
}

func TestMicrosoftConvertRoundTrip(t *testing.T) {
	// Whole-record round-trip sanity: Record → msContact → Record preserves
	// the round-trip-safe field subset.
	original := &contact.Record{
		Fn:      "Alice",
		NGiven:  "Alice",
		NFamily: "W",
		Emails:  []contact.RecordEmail{{Email: "ALICE@EXAMPLE.COM"}},
	}
	c := recordToMicrosoftContact(original, zerolog.Nop())
	back := microsoftContactToRecord(c)
	if back.Fn != "Alice" {
		t.Errorf("Fn round-trip: got %q", back.Fn)
	}
	if len(back.Emails) != 1 || !strings.EqualFold(back.Emails[0].Email, "alice@example.com") {
		t.Errorf("email round-trip lowercased: got %+v", back.Emails)
	}
}
