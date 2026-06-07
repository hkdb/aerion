package backend

import (
	"testing"

	"github.com/hkdb/aerion/internal/contact"
)

// Bug M-C step 2: sidecar persists email types + URL list across calls.
// Round-trip via Set + Get matches what was stored, including case-
// normalized email keys.
func TestMSSidecar_RoundTrip(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	side := MSSidecar{
		EmailTypes: map[string]string{
			"alice@example.com":     "work",
			"alice@home.example.com": "home",
		},
		URLs: []MSSidecarURL{
			{URL: "https://work.example.com", Type: "work"},
			{URL: "https://home.example.com", Type: "home"},
			{URL: "https://other.example.com", Type: "other"},
		},
	}
	if err := store.SetMSSidecar("rec-1", side); err != nil {
		t.Fatalf("SetMSSidecar: %v", err)
	}

	got, err := store.GetMSSidecar("rec-1")
	if err != nil {
		t.Fatalf("GetMSSidecar: %v", err)
	}
	if got.EmailTypes["alice@example.com"] != "work" {
		t.Errorf("email type for alice@example.com: got %q, want work", got.EmailTypes["alice@example.com"])
	}
	if len(got.URLs) != 3 {
		t.Errorf("URLs: got %d entries, want 3", len(got.URLs))
	}
	if got.URLs[1].URL != "https://home.example.com" || got.URLs[1].Type != "home" {
		t.Errorf("URLs[1] order/content lost: got %+v", got.URLs[1])
	}
}

// Bug M-C step 2: addresses are lowercased on write so case-mismatched
// reads (e.g., sync sees lowercase, original write had mixed case) still
// find the type.
func TestMSSidecar_EmailKeyLowercased(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	side := MSSidecar{
		EmailTypes: map[string]string{
			"Alice@Example.COM": "work",
		},
	}
	if err := store.SetMSSidecar("rec-1", side); err != nil {
		t.Fatalf("SetMSSidecar: %v", err)
	}

	got, _ := store.GetMSSidecar("rec-1")
	if got.EmailTypes["alice@example.com"] != "work" {
		t.Errorf("lowercase lookup failed: got %q (full map: %+v)", got.EmailTypes["alice@example.com"], got.EmailTypes)
	}
	if _, ok := got.EmailTypes["Alice@Example.COM"]; ok {
		t.Errorf("mixed-case key should NOT be present after lowercase normalization: %+v", got.EmailTypes)
	}
}

// GetMSSidecar on a missing record returns a zero value + nil error (not
// sql.ErrNoRows).
func TestMSSidecar_GetMissing(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := store.GetMSSidecar("never-set")
	if err != nil {
		t.Fatalf("GetMSSidecar: %v", err)
	}
	if len(got.EmailTypes) != 0 || len(got.URLs) != 0 {
		t.Errorf("expected zero sidecar, got %+v", got)
	}
}

// DeleteMSSidecar is idempotent — deleting a non-existent row is not an
// error.
func TestMSSidecar_DeleteIdempotent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.DeleteMSSidecar("never-set"); err != nil {
		t.Errorf("DeleteMSSidecar on missing row should be no-op: %v", err)
	}

	// And after deleting a real row, the row is gone.
	_ = store.SetMSSidecar("rec-1", MSSidecar{
		EmailTypes: map[string]string{"a@b.com": "work"},
	})
	if err := store.DeleteMSSidecar("rec-1"); err != nil {
		t.Errorf("DeleteMSSidecar: %v", err)
	}
	got, _ := store.GetMSSidecar("rec-1")
	if len(got.EmailTypes) != 0 {
		t.Errorf("sidecar should be gone after delete: %+v", got)
	}
}

// sidecarFromRecord extracts email types + URLs from a Record, dropping
// empties.
func TestSidecarFromRecord(t *testing.T) {
	rec := &contact.Record{
		Emails: []contact.RecordEmail{
			{Email: "Alice@Example.com", EmailType: "work"},
			{Email: "untyped@example.com", EmailType: ""}, // no type → excluded
			{Email: "", EmailType: "stray"},               // no address → excluded
		},
		URLs: []contact.RecordURL{
			{URL: "https://a.com", URLType: "work"},
			{URL: "", URLType: "junk"}, // empty URL → excluded
			{URL: "https://b.com"},     // no type, kept (URL is required, type optional)
		},
	}
	side := sidecarFromRecord(rec)
	if side.EmailTypes["alice@example.com"] != "work" {
		t.Errorf("EmailTypes: got %+v", side.EmailTypes)
	}
	if _, ok := side.EmailTypes["untyped@example.com"]; ok {
		t.Errorf("untyped email should be excluded: %+v", side.EmailTypes)
	}
	if len(side.URLs) != 2 {
		t.Errorf("URLs: got %d entries, want 2 (empty URL excluded): %+v", len(side.URLs), side.URLs)
	}
}

// applyMSExtrasToRecord stamps types from src.Emails onto dst.Emails by
// lowercase address, and wholesale-replaces dst.URLs with src.URLs.
func TestApplyMSExtrasToRecord(t *testing.T) {
	dst := &contact.Record{
		Emails: []contact.RecordEmail{
			{Email: "alice@example.com"},
			{Email: "bob@example.com"},
		},
		URLs: []contact.RecordURL{
			{URL: "https://only-on-graph.example.com"},
		},
	}
	src := &contact.Record{
		Emails: []contact.RecordEmail{
			{Email: "Alice@Example.com", EmailType: "work"},
			{Email: "bob@example.com", EmailType: "home"},
		},
		URLs: []contact.RecordURL{
			{URL: "https://from-src-1.example.com", URLType: "work"},
			{URL: "https://from-src-2.example.com", URLType: "home"},
		},
	}
	applyMSExtrasToRecord(dst, src)

	if dst.Emails[0].EmailType != "work" {
		t.Errorf("alice email type not stamped: %+v", dst.Emails[0])
	}
	if dst.Emails[1].EmailType != "home" {
		t.Errorf("bob email type not stamped: %+v", dst.Emails[1])
	}
	if len(dst.URLs) != 2 || dst.URLs[0].URLType != "work" {
		t.Errorf("URLs should be replaced wholesale from src: got %+v", dst.URLs)
	}
}

// applyMSSidecarToRecord stamps EmailType on emails by lowercase address
// (tolerating address case differences) and replaces URL list from sidecar.
func TestApplyMSSidecarToRecord(t *testing.T) {
	rec := &contact.Record{
		Emails: []contact.RecordEmail{
			{Email: "alice@example.com"},
			{Email: "stale@example.com"}, // not in sidecar → kept, no type
		},
		URLs: []contact.RecordURL{
			{URL: "https://graph-default.example.com"},
		},
	}
	side := MSSidecar{
		EmailTypes: map[string]string{
			"alice@example.com": "work",
		},
		URLs: []MSSidecarURL{
			{URL: "https://from-sidecar.example.com", Type: "home"},
		},
	}
	applyMSSidecarToRecord(rec, side)

	if rec.Emails[0].EmailType != "work" {
		t.Errorf("alice email type not stamped from sidecar: %+v", rec.Emails[0])
	}
	if rec.Emails[1].EmailType != "" {
		t.Errorf("stale email should keep empty type: %+v", rec.Emails[1])
	}
	if len(rec.URLs) != 1 || rec.URLs[0].URL != "https://from-sidecar.example.com" || rec.URLs[0].URLType != "home" {
		t.Errorf("URLs should be replaced from sidecar: %+v", rec.URLs)
	}
}
