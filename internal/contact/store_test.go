package contact

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkdb/aerion/internal/database"
)

func openTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsert(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.AddOrUpdate("alice@example.com", "Alice"); err != nil {
		t.Fatalf("AddOrUpdate failed: %v", err)
	}

	got, err := store.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected contact, got nil")
	}
	if got.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Alice")
	}
	if got.SendCount != 1 {
		t.Errorf("SendCount = %d, want 1", got.SendCount)
	}

	// Upsert again to increment send count
	if err := store.AddOrUpdate("alice@example.com", "Alice Smith"); err != nil {
		t.Fatalf("AddOrUpdate (second) failed: %v", err)
	}

	got, err = store.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get after second upsert failed: %v", err)
	}
	if got.SendCount != 2 {
		t.Errorf("SendCount = %d, want 2", got.SendCount)
	}
	if got.DisplayName != "Alice Smith" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Alice Smith")
	}
}

func TestSearch(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	contacts := []struct {
		email, name string
	}{
		{"alice@example.com", "Alice"},
		{"bob@example.com", "Bob"},
		{"alicia@test.com", "Alicia"},
	}
	for _, c := range contacts {
		if err := store.AddOrUpdate(c.email, c.name); err != nil {
			t.Fatalf("AddOrUpdate failed: %v", err)
		}
	}

	results, err := store.Search("ali", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Search returned %d results, want 2", len(results))
	}

	// Verify both alice and alicia are returned
	emails := make(map[string]bool)
	for _, r := range results {
		emails[r.Email] = true
	}
	if !emails["alice@example.com"] {
		t.Error("expected alice@example.com in results")
	}
	if !emails["alicia@test.com"] {
		t.Error("expected alicia@test.com in results")
	}
}

func TestSearchEmpty(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	results, err := store.Search("nonexistent", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search returned %d results, want 0", len(results))
	}
}

func TestUpdateName(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.AddOrUpdate("alice@example.com", "Alice Auto"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	// Renaming should succeed.
	if err := store.UpdateName("alice@example.com", "Alice User-Edit"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	c, err := store.Get("alice@example.com")
	if err != nil || c == nil {
		t.Fatalf("Get after UpdateName: c=%v err=%v", c, err)
	}
	if c.DisplayName != "Alice User-Edit" {
		t.Errorf("got name %q, want %q", c.DisplayName, "Alice User-Edit")
	}

	// Subsequent AddOrUpdate (auto-collection on sent mail) should NOT clobber
	// the user-edited name once name_overridden=1.
	if err := store.AddOrUpdate("alice@example.com", "Alice Auto-2"); err != nil {
		t.Fatalf("AddOrUpdate after edit: %v", err)
	}
	c, _ = store.Get("alice@example.com")
	if c.DisplayName != "Alice User-Edit" {
		t.Errorf("AddOrUpdate clobbered user-overridden name; got %q, want %q", c.DisplayName, "Alice User-Edit")
	}
	// But send_count should still bump.
	if c.SendCount < 2 {
		t.Errorf("send_count = %d, want >= 2 (user edit shouldn't block auto-collection counters)", c.SendCount)
	}
}

func TestUpdateName_NonExistent(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	err := store.UpdateName("nobody@example.com", "Nobody")
	if err == nil {
		t.Fatal("expected error for non-existent contact, got nil")
	}
}

func TestCreate_Manual(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.Create("manual@example.com", "Manual Person"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	c, err := store.Get("manual@example.com")
	if err != nil || c == nil {
		t.Fatalf("Get after Create: c=%v err=%v", c, err)
	}
	if c.DisplayName != "Manual Person" {
		t.Errorf("DisplayName = %q, want %q", c.DisplayName, "Manual Person")
	}
	if c.Kind != "manual" {
		t.Errorf("Kind = %q, want manual", c.Kind)
	}
	if c.SendCount != 0 {
		t.Errorf("SendCount = %d, want 0 for manual add", c.SendCount)
	}
}

func TestCreate_Conflict(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.Create("dup@example.com", "First"); err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	err := store.Create("dup@example.com", "Second")
	if !errors.Is(err, ErrContactExists) {
		t.Fatalf("Create (second): got %v, want ErrContactExists", err)
	}
	// Original value should be untouched.
	c, _ := store.Get("dup@example.com")
	if c == nil || c.DisplayName != "First" {
		t.Errorf("conflict clobbered original: got %v", c)
	}
}

func TestCreate_NormalizesEmail(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.Create("  MIXED@Example.COM  ", "Mixed"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	c, _ := store.Get("mixed@example.com")
	if c == nil {
		t.Fatal("Create should normalize email to lowercase + trimmed")
	}
}

// TestKindPreservedOnConflict verifies the key invariant: once a contact is
// kind='manual', subsequent AddOrUpdate (auto-collection from sent mail) does
// NOT downgrade it to 'collected'. Mirrors the name_overridden protection.
func TestKindPreservedOnConflict(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	// User manually adds the contact.
	if err := store.Create("alice@example.com", "Alice Manual"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Later, the user sends mail to them. AddOrUpdate fires.
	if err := store.AddOrUpdate("alice@example.com", "Alice From Mail"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	c, err := store.Get("alice@example.com")
	if err != nil || c == nil {
		t.Fatalf("Get: c=%v err=%v", c, err)
	}
	if c.Kind != "manual" {
		t.Errorf("Kind = %q, want manual (must not downgrade to collected on AddOrUpdate)", c.Kind)
	}
	// Manual contacts have name_overridden=1, so display_name is preserved too.
	if c.DisplayName != "Alice Manual" {
		t.Errorf("DisplayName = %q, want %q (name_overridden protection)", c.DisplayName, "Alice Manual")
	}
	if c.SendCount != 1 {
		t.Errorf("SendCount = %d, want 1 (AddOrUpdate should still bump counter)", c.SendCount)
	}
}

// TestKindDefaultsToCollected verifies that the auto-collection path
// (AddOrUpdate on a brand-new email) sets kind='collected'.
func TestKindDefaultsToCollected(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.AddOrUpdate("bob@example.com", "Bob"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}
	c, _ := store.Get("bob@example.com")
	if c == nil || c.Kind != "collected" {
		t.Errorf("Kind = %q, want collected", c.Kind)
	}
}

func TestListByKind(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	// Two manual + two collected.
	if err := store.Create("m1@example.com", "Manual 1"); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := store.Create("m2@example.com", "Manual 2"); err != nil {
		t.Fatalf("Create m2: %v", err)
	}
	if err := store.AddOrUpdate("c1@example.com", "Collected 1"); err != nil {
		t.Fatalf("AddOrUpdate c1: %v", err)
	}
	if err := store.AddOrUpdate("c2@example.com", "Collected 2"); err != nil {
		t.Fatalf("AddOrUpdate c2: %v", err)
	}

	manual, err := store.ListByKind("manual", 0)
	if err != nil {
		t.Fatalf("ListByKind(manual): %v", err)
	}
	if len(manual) != 2 {
		t.Errorf("manual kind: got %d, want 2", len(manual))
	}
	for _, c := range manual {
		if c.Kind != "manual" {
			t.Errorf("non-manual contact in manual filter: %v", c)
		}
	}

	collected, err := store.ListByKind("collected", 0)
	if err != nil {
		t.Fatalf("ListByKind(collected): %v", err)
	}
	if len(collected) != 2 {
		t.Errorf("collected kind: got %d, want 2", len(collected))
	}
	for _, c := range collected {
		if c.Kind != "collected" {
			t.Errorf("non-collected contact in collected filter: %v", c)
		}
	}

	all, err := store.ListByKind("", 0)
	if err != nil {
		t.Fatalf("ListByKind(\"\"): %v", err)
	}
	if len(all) != 4 {
		t.Errorf("unfiltered: got %d, want 4", len(all))
	}
}

// isUUID returns true for canonical 8-4-4-4-12 hex with dashes.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// TestAddOrUpdate_GeneratesUUID — migration 32 invariant: new local records
// get a UUID identity, NOT the legacy "local-<email>" synthetic id.
func TestAddOrUpdate_GeneratesUUID(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.AddOrUpdate("alice@example.com", "Alice"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM contact_records WHERE source='local' LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("query record id: %v", err)
	}
	if !isUUID(id) {
		t.Errorf("expected UUID record id, got %q", id)
	}
	if strings.HasPrefix(id, "local-") {
		t.Errorf("record id still has the legacy 'local-' prefix: %q", id)
	}
}

// TestAddOrUpdate_FindsExistingByEmail — idempotency: two calls with the same
// email produce ONE record (lookup happens via contact_emails, not via the
// synthetic id). send_count bumps to 2.
func TestAddOrUpdate_FindsExistingByEmail(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.AddOrUpdate("bob@example.com", "Bob"); err != nil {
		t.Fatalf("first AddOrUpdate: %v", err)
	}
	if err := store.AddOrUpdate("bob@example.com", "Bob"); err != nil {
		t.Fatalf("second AddOrUpdate: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM contact_records WHERE source='local'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record after idempotent AddOrUpdate, got %d", count)
	}

	got, err := store.Get("bob@example.com")
	if err != nil || got == nil {
		t.Fatalf("Get: c=%v err=%v", got, err)
	}
	if got.SendCount != 2 {
		t.Errorf("send_count = %d, want 2", got.SendCount)
	}
}

// TestCreate_GeneratesUUID — Create (manual Add Contact path) also uses UUIDs.
func TestCreate_GeneratesUUID(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	if err := store.Create("carol@example.com", "Carol"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM contact_records WHERE source='local' LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("query record id: %v", err)
	}
	if !isUUID(id) {
		t.Errorf("expected UUID record id, got %q", id)
	}
}

// Regression for #278: a contact that lists the same address twice (e.g. an
// MS365 export, or two case variants normalizing to the same value) must not
// fail the record on the contact_emails PRIMARY KEY(record_id, email).
func TestUpsertRecord_DuplicateEmailDeduped(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db.DB)

	rec := &Record{
		ID:     "rec-dup",
		Source: "carddav",
		Fn:     "Dup Dave",
		Emails: []RecordEmail{
			{Email: "dave@example.com"},
			{Email: "Dave@example.com"}, // case variant -> normalizes to the same address
		},
	}
	if err := store.UpsertRecord(rec); err != nil {
		t.Fatalf("UpsertRecord with a duplicate email should not error: %v", err)
	}

	got, err := store.GetRecord("rec-dup")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if len(got.Emails) != 1 {
		t.Fatalf("want 1 deduped email row, got %d: %+v", len(got.Emails), got.Emails)
	}
	if got.Emails[0].Email != "dave@example.com" {
		t.Errorf("email = %q, want normalized dave@example.com", got.Emails[0].Email)
	}
}
