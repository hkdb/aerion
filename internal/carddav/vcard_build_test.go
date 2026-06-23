package carddav

import (
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav/carddav"
	"github.com/hkdb/aerion/internal/contact"
)

func TestBuildVCard_RoundTripPreservesUnknownProperties(t *testing.T) {
	original := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"UID:abc-123",
		"FN:Old Name",
		"EMAIL;TYPE=HOME:old@example.com",
		"X-CUSTOM-FIELD:keep-me-please",
		"END:VCARD",
		"",
	}, "\r\n")

	rec := &contact.Record{
		ID:  "abc-123",
		Fn:  "New Name",
		Emails: []contact.RecordEmail{
			{Email: "new@example.com", EmailType: "work", IsPrimary: true},
		},
	}

	out, err := BuildVCard(rec, original)
	if err != nil {
		t.Fatalf("BuildVCard: %v", err)
	}

	if !strings.Contains(string(out), "X-CUSTOM-FIELD:keep-me-please") {
		t.Errorf("unknown property X-CUSTOM-FIELD was dropped\nOutput:\n%s", out)
	}
	if !strings.Contains(string(out), "FN:New Name") {
		t.Errorf("FN was not updated to new value\nOutput:\n%s", out)
	}
	if strings.Contains(string(out), "old@example.com") {
		t.Errorf("old email leaked through\nOutput:\n%s", out)
	}
	if !strings.Contains(string(out), "new@example.com") {
		t.Errorf("new email missing\nOutput:\n%s", out)
	}
}

func TestBuildVCard_MutatesKnownFields(t *testing.T) {
	rec := &contact.Record{
		ID:       "uid-1",
		Fn:       "Alice Smith",
		NGiven:   "Alice",
		NFamily:  "Smith",
		Org:      "Acme",
		Title:    "Engineer",
		Note:     "Met at conference",
		Nickname: "Al",
		Bday:     "1990-01-01",
		Emails: []contact.RecordEmail{
			{Email: "alice@home.com", EmailType: "home", IsPrimary: true},
			{Email: "alice@work.com", EmailType: "work"},
		},
		Phones: []contact.RecordPhone{
			{Number: "+1-555-0100", PhoneType: "cell", IsPrimary: true},
		},
		Addresses: []contact.RecordAddress{
			{AddrType: "home", Street: "1 Main St", City: "Springfield", Region: "IL", Postcode: "62701", Country: "USA"},
		},
		URLs: []contact.RecordURL{
			{URL: "https://example.com", URLType: "home"},
		},
		IMPPs: []contact.RecordIMPP{
			{Handle: "xmpp:alice@chat.com", IMPPType: "personal"},
		},
		Categories: []string{"friend", "team"},
	}

	out, err := BuildVCard(rec, "")
	if err != nil {
		t.Fatalf("BuildVCard: %v", err)
	}

	// Re-parse to verify the encoded card holds the expected values rather
	// than asserting exact wire-format strings (which can vary in spacing).
	dec := vcard.NewDecoder(strings.NewReader(string(out)))
	card, err := dec.Decode()
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	if got := card.Value(vcard.FieldFormattedName); got != "Alice Smith" {
		t.Errorf("FN = %q, want %q", got, "Alice Smith")
	}
	if n := card.Name(); n == nil || n.GivenName != "Alice" || n.FamilyName != "Smith" {
		t.Errorf("N mismatch: %+v", n)
	}
	if got := card.Value(vcard.FieldOrganization); got != "Acme" {
		t.Errorf("ORG = %q", got)
	}
	if got := card.Value(vcard.FieldTitle); got != "Engineer" {
		t.Errorf("TITLE = %q", got)
	}
	if got := card.Value(vcard.FieldNote); got != "Met at conference" {
		t.Errorf("NOTE = %q", got)
	}
	if got := card.Value(vcard.FieldNickname); got != "Al" {
		t.Errorf("NICKNAME = %q", got)
	}
	if got := card.Value(vcard.FieldBirthday); got != "1990-01-01" {
		t.Errorf("BDAY = %q", got)
	}
	emails := card[vcard.FieldEmail]
	if len(emails) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(emails))
	}
	if emails[0].Value != "alice@home.com" || emails[1].Value != "alice@work.com" {
		t.Errorf("email values: [%s, %s]", emails[0].Value, emails[1].Value)
	}
	if len(card.Addresses()) != 1 {
		t.Errorf("expected 1 address, got %d", len(card.Addresses()))
	}
	if cats := card.Categories(); len(cats) != 2 || cats[0] != "friend" || cats[1] != "team" {
		t.Errorf("categories = %v", cats)
	}
	if got := card.Value(vcard.FieldUID); got != "uid-1" {
		t.Errorf("UID was not synthesized from rec.ID: got %q", got)
	}
}

func TestBuildVCard_EmptyOriginalRawBuildsFromScratch(t *testing.T) {
	rec := &contact.Record{
		ID: "uid-2",
		Fn: "Bob",
		Emails: []contact.RecordEmail{
			{Email: "bob@example.com", IsPrimary: true},
		},
	}
	out, err := BuildVCard(rec, "")
	if err != nil {
		t.Fatalf("BuildVCard: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "VERSION:3.0") {
		t.Errorf("missing VERSION:3.0 line:\n%s", s)
	}
	if !strings.Contains(s, "FN:Bob") {
		t.Errorf("FN line not present:\n%s", s)
	}
	if !strings.Contains(s, "EMAIL:bob@example.com") {
		t.Errorf("EMAIL line not present:\n%s", s)
	}
}

func TestBuildVCard_NilRecord(t *testing.T) {
	if _, err := BuildVCard(nil, ""); err == nil {
		t.Fatal("expected error on nil record")
	}
}

// ============================================================================
// PHOTO field — Phase 2b.2.b.2
// ============================================================================

func TestParseVCard_InlinePhoto(t *testing.T) {
	// Build a parseable AddressObject via decoding our own minimal vCard,
	// then run parseVCard on the resulting Card.
	raw := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:Photo Person",
		"PHOTO;ENCODING=b;TYPE=JPEG:VEVTVERBVEE=",
		"END:VCARD",
		"",
	}, "\r\n")
	dec := vcard.NewDecoder(strings.NewReader(raw))
	card, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode test vcard: %v", err)
	}
	rec := parseVCard(carddavAddressObject(card, "/test/p1.vcf", "etag-1"))
	if rec == nil {
		t.Fatal("parseVCard returned nil")
	}
	if rec.PhotoData != "VEVTVERBVEE=" {
		t.Errorf("PhotoData = %q, want VEVTVERBVEE=", rec.PhotoData)
	}
	if rec.PhotoMediaType != "image/jpeg" {
		t.Errorf("PhotoMediaType = %q, want image/jpeg", rec.PhotoMediaType)
	}
	if rec.PhotoURL != "" {
		t.Errorf("PhotoURL should be empty for inline, got %q", rec.PhotoURL)
	}
}

func TestParseVCard_URLRefPhoto(t *testing.T) {
	raw := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:URL Person",
		"PHOTO;VALUE=URI:http://example.com/photo.jpg",
		"END:VCARD",
		"",
	}, "\r\n")
	dec := vcard.NewDecoder(strings.NewReader(raw))
	card, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode test vcard: %v", err)
	}
	rec := parseVCard(carddavAddressObject(card, "/test/p2.vcf", "etag-2"))
	if rec == nil {
		t.Fatal("parseVCard returned nil")
	}
	if rec.PhotoURL != "http://example.com/photo.jpg" {
		t.Errorf("PhotoURL = %q, want http://example.com/photo.jpg", rec.PhotoURL)
	}
	if rec.PhotoData != "" || rec.PhotoMediaType != "" {
		t.Errorf("inline fields should be empty for URL ref; got data=%q media=%q", rec.PhotoData, rec.PhotoMediaType)
	}
}

func TestBuildVCard_EmitsInlinePhoto(t *testing.T) {
	rec := &contact.Record{
		ID:             "rec-photo",
		Fn:             "Has Photo",
		PhotoData:      "VEVTVERBVEE=",
		PhotoMediaType: "image/png",
	}
	out, err := BuildVCard(rec, "")
	if err != nil {
		t.Fatalf("BuildVCard: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "PHOTO") {
		t.Errorf("expected PHOTO line in output:\n%s", s)
	}
	if !strings.Contains(s, "ENCODING=b") {
		t.Errorf("expected ENCODING=b in PHOTO line:\n%s", s)
	}
	if !strings.Contains(s, "TYPE=PNG") {
		t.Errorf("expected TYPE=PNG (derived from image/png):\n%s", s)
	}
	if !strings.Contains(s, "VEVTVERBVEE=") {
		t.Errorf("expected base64 data in output:\n%s", s)
	}
}

func TestBuildVCard_RemovesPhoto(t *testing.T) {
	// Originalraw has a PHOTO; record has no photo → output should drop PHOTO.
	original := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:Old Name",
		"PHOTO;ENCODING=b;TYPE=JPEG:VEVTVERBVEE=",
		"END:VCARD",
		"",
	}, "\r\n")
	rec := &contact.Record{
		ID: "rec-no-photo",
		Fn: "Now No Photo",
		// PhotoData, PhotoMediaType, PhotoURL all empty.
	}
	out, err := BuildVCard(rec, original)
	if err != nil {
		t.Fatalf("BuildVCard: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "PHOTO") {
		t.Errorf("PHOTO line should be absent after rec has no photo:\n%s", s)
	}
	if strings.Contains(s, "VEVTVERBVEE=") {
		t.Errorf("old base64 should be gone:\n%s", s)
	}
}

// carddavAddressObject wraps a vcard.Card into a carddav.AddressObject suitable
// for parseVCard — keeps the test setup minimal without spinning up an httptest
// server.
func carddavAddressObject(card vcard.Card, path, etag string) carddav.AddressObject {
	return carddav.AddressObject{Path: path, ETag: etag, Card: card}
}
