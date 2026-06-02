package backend

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/credentials"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/database"
)

// setupAPIWithCreds builds an API plus a real credentials.Store so the
// CardDAV write paths (which need GetCardDAVPassword) can be exercised
// end-to-end. The plain setupAPI passes credStore=nil since most tests
// don't touch the write path.
func setupAPIWithCreds(t *testing.T) (*API, *contact.Store, *carddav.Store, *credentials.Store) {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	localStore := contact.NewStore(db.DB)
	carddavStore := carddav.NewStore(db.DB)
	credStore, err := credentials.NewStore(db.DB, tmp)
	if err != nil {
		t.Fatalf("credentials.NewStore: %v", err)
	}
	// API takes a closure, not a *credentials.Store directly (D1 refactor).
	// The closure here points at the real test credStore so the CardDAV
	// write path can resolve real passwords end-to-end. The internal/
	// credentials import is fine in test code — the rule against
	// internal-package imports applies to production extension runtime
	// code only.
	return NewAPI(localStore, carddavStore, nil, nil, db.DB, credStore.GetCardDAVPassword), localStore, carddavStore, credStore
}

func setupAPI(t *testing.T) (*API, *contact.Store, *carddav.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	localStore := contact.NewStore(db.DB)
	carddavStore := carddav.NewStore(db.DB)

	// As of 2b.2.a (migration 31), the carddav-search bridge is no longer
	// needed: both local and carddav contacts live in the same unified tables,
	// and contact.Store.Search natively walks them. The legacy SetCardDAVSearchFunc
	// wiring was deleted from app.go + this test setup at the same time.

	return NewAPI(localStore, carddavStore, nil, nil, db.DB, nil), localStore, carddavStore
}

func TestAPI_SearchContacts_LocalOnly(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.AddOrUpdate("alice@example.com", "Alice"); err != nil {
		t.Fatalf("add local: %v", err)
	}
	if err := local.AddOrUpdate("bob@example.com", "Bob"); err != nil {
		t.Fatalf("add local: %v", err)
	}

	got, err := api.SearchContacts("alice", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].ID != "alice@example.com" {
		t.Fatalf("expected id=alice@example.com, got %q", got[0].ID)
	}
	if got[0].Name != "Alice" {
		t.Fatalf("expected name=Alice, got %q", got[0].Name)
	}
	if got[0].SourceID != "aerion" {
		t.Fatalf("expected source=aerion, got %q", got[0].SourceID)
	}
}

func TestAPI_GetContact_ByEmail(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.AddOrUpdate("alice@example.com", "Alice"); err != nil {
		t.Fatalf("add local: %v", err)
	}

	got, err := api.GetContact("alice@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatalf("expected hit, got nil")
	}
	if got.Name != "Alice" || len(got.Emails) != 1 || got.Emails[0] != "alice@example.com" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestAPI_GetContact_ByEmail_Missing(t *testing.T) {
	api, _, _ := setupAPI(t)

	got, err := api.GetContact("nobody@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing, got %+v", got)
	}
}

func TestAPI_GetContact_ByCardDAVID(t *testing.T) {
	api, _, carddavStore := setupAPI(t)

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "Test", Type: carddav.SourceTypeCardDAV, URL: "https://example", Enabled: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID:            "cdv-uuid-1",
		AddressbookID: ab.ID,
		Email:         "carol@example.com",
		DisplayName:   "Carol",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := api.GetContact("cdv-uuid-1")
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got == nil {
		t.Fatalf("expected hit, got nil")
	}
	if got.ID != "cdv-uuid-1" || got.Name != "Carol" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestAPI_ListContacts_Local(t *testing.T) {
	api, local, _ := setupAPI(t)

	for _, e := range []struct{ email, name string }{
		{"a@x", "A"},
		{"b@x", "B"},
		{"c@x", "C"},
	} {
		if err := local.AddOrUpdate(e.email, e.name); err != nil {
			t.Fatalf("add local: %v", err)
		}
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: SourceIDLocal, Limit: 10})
	if err != nil {
		t.Fatalf("list local: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

func TestAPI_ListContacts_CardDAVScoped(t *testing.T) {
	api, _, carddavStore := setupAPI(t)
	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	for i, name := range []string{"Alice", "Bob", "Carol"} {
		if err := carddavStore.UpsertContact(&carddav.Contact{
			ID:            "cdv-" + name,
			AddressbookID: ab.ID,
			Email:         name + "@example.com",
			DisplayName:   name,
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: src.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list carddav: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// SourceID should be propagated to results
	for _, c := range got {
		if c.SourceID != src.ID {
			t.Fatalf("expected SourceID=%s, got %s", src.ID, c.SourceID)
		}
	}
}

func TestAPI_ListContacts_MergedAcrossSources(t *testing.T) {
	// "All" view (SourceID == "") merges local + CardDAV regardless of
	// whether a query is set. Empty query → match-all in each source.
	api, local, carddavStore := setupAPI(t)

	if err := local.AddOrUpdate("local-a@x", "Local A"); err != nil {
		t.Fatalf("add local: %v", err)
	}
	if err := local.AddOrUpdate("local-b@x", "Local B"); err != nil {
		t.Fatalf("add local: %v", err)
	}

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID: "cdv-1", AddressbookID: ab.ID, Email: "carddav-c@x", DisplayName: "CardDAV C",
	}); err != nil {
		t.Fatalf("upsert carddav: %v", err)
	}

	got, err := api.ListContacts(coreapi.ContactFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list merged: %v", err)
	}
	// Expect all three: 2 local + 1 carddav.
	if len(got) != 3 {
		t.Fatalf("expected 3 merged results, got %d: %+v", len(got), got)
	}

	emails := map[string]bool{}
	for _, c := range got {
		if len(c.Emails) > 0 {
			emails[c.Emails[0]] = true
		}
	}
	for _, want := range []string{"local-a@x", "local-b@x", "carddav-c@x"} {
		if !emails[want] {
			t.Fatalf("expected merged result to include %q; got emails=%v", want, emails)
		}
	}
}

func strPtr(s string) *string { return &s }

func TestAPI_UpdateContact_Local(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.AddOrUpdate("alice@example.com", "Alice Auto"); err != nil {
		t.Fatalf("seed local: %v", err)
	}

	// Happy path: rename a local contact.
	if err := api.UpdateContact("alice@example.com", coreapi.ContactPatch{Name: strPtr("Alice Edit")}); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}

	got, err := api.GetContact("alice@example.com")
	if err != nil || got == nil {
		t.Fatalf("GetContact after update: got=%v err=%v", got, err)
	}
	if got.Name != "Alice Edit" {
		t.Fatalf("name after update: got %q, want %q", got.Name, "Alice Edit")
	}

	// Auto-collection on next send must NOT clobber the user edit.
	if err := local.AddOrUpdate("alice@example.com", "Alice Auto-2"); err != nil {
		t.Fatalf("AddOrUpdate after edit: %v", err)
	}
	got, _ = api.GetContact("alice@example.com")
	if got.Name != "Alice Edit" {
		t.Fatalf("auto-collection clobbered user edit: got %q, want %q", got.Name, "Alice Edit")
	}
}

func TestAPI_UpdateContact_NilPatchIsNoOp(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.AddOrUpdate("bob@example.com", "Bob Auto"); err != nil {
		t.Fatalf("seed local: %v", err)
	}

	// Empty patch — name should stay as auto-collected.
	if err := api.UpdateContact("bob@example.com", coreapi.ContactPatch{}); err != nil {
		t.Fatalf("UpdateContact with empty patch: %v", err)
	}

	got, _ := api.GetContact("bob@example.com")
	if got == nil || got.Name != "Bob Auto" {
		t.Fatalf("empty patch should not have mutated; got %+v", got)
	}
}

func TestAPI_UpdateContact_CardDAVNotWritable_Refused(t *testing.T) {
	api, _, carddavStore := setupAPI(t)

	// Seed a CardDAV record on a source that is NOT marked writable. Per
	// 2b.2.b.1 the dispatch refuses to write rather than ErrUnimplemented;
	// the user has to flip the per-source "Enable write access" flag first.
	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, Writable: false, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID: "cdv-uuid-1", AddressbookID: ab.ID, Email: "carddav@example.com", DisplayName: "CD",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	err = api.UpdateContact("cdv-uuid-1", coreapi.ContactPatch{Name: strPtr("renamed")})
	if err == nil {
		t.Fatal("expected refusal when source is not writable")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("error should mention writability; got: %v", err)
	}
}

func TestAPI_UpdateContact_EmptyIDRejected(t *testing.T) {
	api, _, _ := setupAPI(t)
	if err := api.UpdateContact("", coreapi.ContactPatch{Name: strPtr("x")}); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestAPI_DeleteContact_Local(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.AddOrUpdate("carol@example.com", "Carol"); err != nil {
		t.Fatalf("seed local: %v", err)
	}

	if err := api.DeleteContact("carol@example.com"); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}

	got, err := api.GetContact("carol@example.com")
	if err != nil {
		t.Fatalf("GetContact after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestAPI_DeleteContact_CardDAVNotWritable_Refused(t *testing.T) {
	api, _, carddavStore := setupAPI(t)

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, Writable: false, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID: "cdv-uuid-1", AddressbookID: ab.ID, Email: "carddav@example.com", DisplayName: "CD",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	err = api.DeleteContact("cdv-uuid-1")
	if err == nil {
		t.Fatal("expected refusal when source is not writable")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("error should mention writability; got: %v", err)
	}
}

func TestAPI_DeleteContact_EmptyIDRejected(t *testing.T) {
	api, _, _ := setupAPI(t)
	if err := api.DeleteContact(""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestAPI_SubscribeToContactEvents_Unimplemented(t *testing.T) {
	api, _, _ := setupAPI(t)
	_, _, err := api.SubscribeToContactEvents(nil)
	if err != coreapi.ErrUnimplemented {
		t.Fatalf("expected ErrUnimplemented, got %v", err)
	}
}

func TestAPI_NilStores_GracefulDegradation(t *testing.T) {
	api := NewAPI(nil, nil, nil, nil, nil, nil)
	if got, err := api.SearchContacts("anything", 10); err != nil || got != nil {
		t.Fatalf("search with nil stores: got=%v err=%v", got, err)
	}
	if got, err := api.GetContact("a@x"); err != nil || got != nil {
		t.Fatalf("get with nil stores: got=%v err=%v", got, err)
	}
	if got, err := api.ListContacts(coreapi.ContactFilter{SourceID: SourceIDLocal}); err != nil || got != nil {
		t.Fatalf("list local with nil stores: got=%v err=%v", got, err)
	}
}

// TestAPI_GetContact_CardDAVByEmailFallback verifies the bug-fix path: when an
// email-shaped id surfaces from the "All" view (because the carddav-search
// bridge drops the UUID and fromLocal sets ID=email), GetContact must fall
// back to a CardDAV-by-email lookup when the local store has no entry for
// that email. Without this fallback the detail pane shows the empty
// placeholder for CardDAV-only contacts.
func TestAPI_GetContact_CardDAVByEmailFallback(t *testing.T) {
	api, _, carddavStore := setupAPI(t)

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID: "cdv-only", AddressbookID: ab.ID, Email: "carddav-only@example.com", DisplayName: "CardDAV Only",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The email is NOT in the local store. GetContact(email) must fall through
	// to carddavStore.GetContactByEmail.
	got, err := api.GetContact("carddav-only@example.com")
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if got == nil {
		t.Fatal("expected CardDAV-by-email fallback to hit, got nil")
	}
	if got.Name != "CardDAV Only" {
		t.Errorf("expected name=%q, got %q", "CardDAV Only", got.Name)
	}
}

// TestAPI_GetContact_LocalPreferredOverCardDAV verifies precedence: when an
// email exists in BOTH stores, the local hit wins (so a user-edited name is
// preserved over the CardDAV display_name).
func TestAPI_GetContact_LocalPreferredOverCardDAV(t *testing.T) {
	api, local, carddavStore := setupAPI(t)

	if err := local.AddOrUpdate("dup@example.com", "Local Name"); err != nil {
		t.Fatalf("add local: %v", err)
	}

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "S1", Type: carddav.SourceTypeCardDAV, URL: "https://x", Enabled: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/ab/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID: "cdv-1", AddressbookID: ab.ID, Email: "dup@example.com", DisplayName: "CardDAV Name",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := api.GetContact("dup@example.com")
	if err != nil || got == nil {
		t.Fatalf("GetContact: got=%v err=%v", got, err)
	}
	if got.Name != "Local Name" {
		t.Errorf("expected local entry to win, got name=%q", got.Name)
	}
}

func TestAPI_CreateContact_LocalManual(t *testing.T) {
	api, local, _ := setupAPI(t)

	id, err := api.CreateContact(coreapi.ContactCreateInput{
		Email: "new@example.com", Name: "Newly Added",
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if id != "new@example.com" {
		t.Errorf("returned id = %q, want %q", id, "new@example.com")
	}

	// Verify via direct store: kind=manual, send_count=0.
	c, err := local.Get("new@example.com")
	if err != nil || c == nil {
		t.Fatalf("Get after CreateContact: c=%v err=%v", c, err)
	}
	if c.Kind != "manual" {
		t.Errorf("Kind = %q, want manual", c.Kind)
	}
	if c.SendCount != 0 {
		t.Errorf("SendCount = %d, want 0", c.SendCount)
	}
}

func TestAPI_CreateContact_ExplicitSourceManual(t *testing.T) {
	api, _, _ := setupAPI(t)

	id, err := api.CreateContact(coreapi.ContactCreateInput{
		SourceID: SourceIDLocalManual,
		Email:    "explicit@example.com",
		Name:     "Explicit",
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if id != "explicit@example.com" {
		t.Errorf("id = %q", id)
	}
}

func TestAPI_CreateContact_NormalizesEmail(t *testing.T) {
	api, _, _ := setupAPI(t)
	id, err := api.CreateContact(coreapi.ContactCreateInput{
		Email: "  MIXED@Example.COM  ", Name: "Mixed",
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if id != "mixed@example.com" {
		t.Errorf("expected normalized id, got %q", id)
	}
}

func TestAPI_CreateContact_RejectsCollectedSource(t *testing.T) {
	api, _, _ := setupAPI(t)
	_, err := api.CreateContact(coreapi.ContactCreateInput{
		SourceID: SourceIDLocalCollected,
		Email:    "x@y.com",
	})
	if err == nil {
		t.Fatal("expected error rejecting Collected source, got nil")
	}
}

func TestAPI_CreateContact_RejectsEmptyEmail(t *testing.T) {
	api, _, _ := setupAPI(t)
	if _, err := api.CreateContact(coreapi.ContactCreateInput{}); err == nil {
		t.Fatal("expected error for empty email")
	}
}

func TestAPI_CreateContact_RejectsInvalidEmail(t *testing.T) {
	api, _, _ := setupAPI(t)
	if _, err := api.CreateContact(coreapi.ContactCreateInput{Email: "not-an-email"}); err == nil {
		t.Fatal("expected error for invalid email (no @)")
	}
}

func TestAPI_CreateContact_Conflict(t *testing.T) {
	api, _, _ := setupAPI(t)
	if _, err := api.CreateContact(coreapi.ContactCreateInput{Email: "dup@example.com", Name: "First"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := api.CreateContact(coreapi.ContactCreateInput{Email: "dup@example.com", Name: "Second"})
	if err == nil {
		t.Fatal("expected conflict error for duplicate email, got nil")
	}
	if err == coreapi.ErrUnimplemented {
		t.Fatalf("got ErrUnimplemented; conflict should surface ErrContactExists or similar, not Unimplemented")
	}
}

func TestAPI_CreateContact_UnknownSourceErrors(t *testing.T) {
	// Post-Track-B: unknown CardDAV-shaped source UUIDs surface a "not found"
	// error rather than ErrUnimplemented. ErrUnimplemented is reserved for
	// known sources of types Aerion hasn't wired write paths for (Google /
	// Microsoft); see TestAPI_CreateContact_OAuthSourceUnimplemented.
	api, _, _ := setupAPI(t)
	_, err := api.CreateContact(coreapi.ContactCreateInput{
		SourceID: "some-carddav-uuid",
		Email:    "x@y.com",
	})
	if err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
	if err == coreapi.ErrUnimplemented {
		t.Fatalf("expected a real error for unknown source, got ErrUnimplemented")
	}
}

func TestAPI_ListContacts_LocalManualSubsource(t *testing.T) {
	api, local, _ := setupAPI(t)

	// One manual + one collected.
	if err := local.Create("manual@example.com", "Manual"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := local.AddOrUpdate("collected@example.com", "Collected"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: SourceIDLocalManual, Limit: 10})
	if err != nil {
		t.Fatalf("ListContacts manual: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 manual contact, got %d: %+v", len(got), got)
	}
	if got[0].Emails[0] != "manual@example.com" {
		t.Errorf("got %s, want manual@example.com", got[0].Emails[0])
	}
}

func TestAPI_ListContacts_LocalCollectedSubsource(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("manual@example.com", "Manual"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := local.AddOrUpdate("collected@example.com", "Collected"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: SourceIDLocalCollected, Limit: 10})
	if err != nil {
		t.Fatalf("ListContacts collected: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 collected contact, got %d: %+v", len(got), got)
	}
	if got[0].Emails[0] != "collected@example.com" {
		t.Errorf("got %s, want collected@example.com", got[0].Emails[0])
	}
}

func TestAPI_ListContacts_LocalParentReturnsBoth(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("manual@example.com", "Manual"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := local.AddOrUpdate("collected@example.com", "Collected"); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}

	got, err := api.ListContacts(coreapi.ContactFilter{SourceID: SourceIDLocal, Limit: 10})
	if err != nil {
		t.Fatalf("ListContacts local-parent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 (manual + collected), got %d: %+v", len(got), got)
	}
}

// ============================================================================
// CardDAV write paths (Phase 2b.2.b.1)
// ============================================================================

// seedWritableCardDAVRecord stands up a fake CardDAV server, creates a
// writable source pointed at it, persists basic-auth creds, and seeds one
// record in the unified store. Returns the fake server (close it via t.Cleanup
// the caller registers), the record id, and the source id.
func seedWritableCardDAVRecord(
	t *testing.T,
	carddavStore *carddav.Store,
	credStore *credentials.Store,
	handler http.HandlerFunc,
) (server *httptest.Server, recordID, sourceID, addressbookID string) {
	t.Helper()
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)

	src, err := carddavStore.CreateSource(&carddav.SourceConfig{
		Name: "Fake", Type: carddav.SourceTypeCardDAV, URL: server.URL,
		Username: "user", Enabled: true, Writable: true, SyncInterval: 60,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	// Bump writable on after create (CreateSource doesn't write the writable column).
	if err := carddavStore.SetSourceWritable(src.ID, true); err != nil {
		t.Fatalf("set writable: %v", err)
	}
	ab, err := carddavStore.CreateAddressbook(src.ID, "/addressbook/", "ab", true)
	if err != nil {
		t.Fatalf("create addressbook: %v", err)
	}
	if err := credStore.SetCardDAVPassword(src.ID, "pass"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if err := carddavStore.UpsertContact(&carddav.Contact{
		ID:            "cdv-uuid-1",
		AddressbookID: ab.ID,
		Email:         "carddav@example.com",
		DisplayName:   "Original",
		Href:          "/addressbook/contact.vcf",
		ETag:          "etag-v1",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return server, "cdv-uuid-1", src.ID, ab.ID
}

func TestAPI_UpdateContact_CardDAV_HappyPath(t *testing.T) {
	api, _, carddavStore, credStore := setupAPIWithCreds(t)

	var gotMethod, gotPath, gotIfMatch string
	var gotBody []byte
	handler := func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotIfMatch = r.Header.Get("If-Match")
		gotBody = readAll(t, r)
		w.Header().Set("ETag", `"etag-v2"`)
		w.WriteHeader(http.StatusNoContent)
	}
	_, recordID, _, _ := seedWritableCardDAVRecord(t, carddavStore, credStore, handler)

	if err := api.UpdateContact(recordID, coreapi.ContactPatch{Name: strPtr("Renamed")}); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("server saw method = %q, want PUT", gotMethod)
	}
	if gotPath != "/addressbook/contact.vcf" {
		t.Errorf("server saw path = %q", gotPath)
	}
	if gotIfMatch != `"etag-v1"` {
		t.Errorf("server saw If-Match = %q, want %q", gotIfMatch, `"etag-v1"`)
	}
	if !strings.Contains(string(gotBody), "FN:Renamed") {
		t.Errorf("PUT body missing renamed FN:\n%s", gotBody)
	}

	// Local cache should reflect the new ETag from the server response.
	c, err := carddavStore.GetContactByHref("", "/addressbook/contact.vcf")
	if err != nil {
		t.Fatalf("post-write lookup: %v", err)
	}
	_ = c // we don't currently expose the new ETag on the public Contact shape;
	// the assertion that the write succeeded + body shape is enough for this
	// unit-level test. Integration coverage of the ETag round-trip lives at
	// the manual / smoke-test layer per the plan's verification section.
}

func TestAPI_UpdateContact_CardDAV_Conflict(t *testing.T) {
	api, _, carddavStore, credStore := setupAPIWithCreds(t)

	// First request (PUT) returns 412; second request (FetchContactByPath via
	// MultiGetAddressBook) returns a normal multistatus body for the refresh.
	// For the unit test we only need to verify the API returns *ErrConflict
	// and doesn't bubble the raw precondition error — the refresh path is
	// allowed to fail silently here (its job is best-effort cache resync).
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}
	_, recordID, _, _ := seedWritableCardDAVRecord(t, carddavStore, credStore, handler)

	err := api.UpdateContact(recordID, coreapi.ContactPatch{Name: strPtr("Renamed")})
	var conflict *coreapi.ErrConflict
	if !asConflict(err, &conflict) {
		t.Fatalf("expected *coreapi.ErrConflict, got %T: %v", err, err)
	}
	if conflict.ContactID != recordID {
		t.Errorf("conflict.ContactID = %q, want %q", conflict.ContactID, recordID)
	}
}

func TestAPI_DeleteContact_CardDAV_HappyPath(t *testing.T) {
	api, local, carddavStore, credStore := setupAPIWithCreds(t)

	var gotMethod, gotIfMatch string
	handler := func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotIfMatch = r.Header.Get("If-Match")
		w.WriteHeader(http.StatusNoContent)
	}
	_, recordID, _, _ := seedWritableCardDAVRecord(t, carddavStore, credStore, handler)

	if err := api.DeleteContact(recordID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("server saw method = %q, want DELETE", gotMethod)
	}
	if gotIfMatch != `"etag-v1"` {
		t.Errorf("server saw If-Match = %q", gotIfMatch)
	}

	// Local record should be cascade-deleted.
	rec, err := local.GetRecord(recordID)
	if err != nil {
		t.Fatalf("GetRecord post-delete: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected local record gone, still got: %+v", rec)
	}
}

// readAll is a small helper so we don't have to thread io/ioutil through the
// individual handler closures.
func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	defer r.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf
}

// asConflict is a thin wrapper around errors.As to keep test sites readable.
func asConflict(err error, target **coreapi.ErrConflict) bool {
	return errors.As(err, target)
}

// ============================================================================
// Multi-field patch dispatch — Phase 2b.2.b.2
// ============================================================================

func TestAPI_UpdateContact_Local_MultiField(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("multi@example.com", "Multi Field"); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	got, _ := api.GetContact("multi@example.com")
	recID := got.ID

	work := "work"
	cell := "cell"
	patch := coreapi.ContactPatch{
		Org:   strPtr("Acme Corp"),
		Title: strPtr("Engineer"),
		Phones: &[]coreapi.ContactPhone{
			{Number: "+1-555-0100", Type: cell, IsPrimary: true},
		},
		Categories: &[]string{"friend", "team"},
		Emails: &[]coreapi.ContactEmail{
			{Email: "multi@example.com", Type: work, IsPrimary: true},
			{Email: "multi-secondary@example.com", Type: "home"},
		},
	}
	if err := api.UpdateContact(recID, patch); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}

	got, err := api.GetContact(recID)
	if err != nil || got == nil {
		t.Fatalf("GetContact: got=%v err=%v", got, err)
	}
	if got.Org != "Acme Corp" {
		t.Errorf("Org = %q, want Acme Corp", got.Org)
	}
	if got.Title != "Engineer" {
		t.Errorf("Title = %q, want Engineer", got.Title)
	}
	if len(got.Phones) != 1 || got.Phones[0].Number != "+1-555-0100" {
		t.Errorf("Phones = %+v, want one row with +1-555-0100", got.Phones)
	}
	if len(got.Categories) != 2 || got.Categories[0] != "friend" || got.Categories[1] != "team" {
		t.Errorf("Categories = %v, want [friend team]", got.Categories)
	}
	if len(got.Emails) != 2 {
		t.Errorf("Emails = %v, want 2 entries", got.Emails)
	}
}

func TestAPI_UpdateContact_Local_NilFieldsNoOp(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("noop@example.com", "Original Name"); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	got, _ := api.GetContact("noop@example.com")
	recID := got.ID

	// Send a patch with all-nil fields — should be no-op.
	if err := api.UpdateContact(recID, coreapi.ContactPatch{}); err != nil {
		t.Fatalf("UpdateContact (nil patch): %v", err)
	}

	got, _ = api.GetContact(recID)
	if got.Name != "Original Name" {
		t.Errorf("Name was clobbered by nil patch: %q", got.Name)
	}
}

func TestAPI_UpdateContact_Local_EmptySliceClears(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("clear@example.com", "Has Many Phones"); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	got, _ := api.GetContact("clear@example.com")
	recID := got.ID

	// Add some phones first.
	if err := api.UpdateContact(recID, coreapi.ContactPatch{
		Phones: &[]coreapi.ContactPhone{
			{Number: "+1-555-0100", Type: "cell"},
			{Number: "+1-555-0101", Type: "home"},
		},
	}); err != nil {
		t.Fatalf("seed phones: %v", err)
	}
	got, _ = api.GetContact(recID)
	if len(got.Phones) != 2 {
		t.Fatalf("phones not seeded: %+v", got.Phones)
	}

	// Now clear with pointer-to-empty-slice (the "set to empty" contract).
	emptyPhones := []coreapi.ContactPhone{}
	if err := api.UpdateContact(recID, coreapi.ContactPatch{
		Phones: &emptyPhones,
	}); err != nil {
		t.Fatalf("clear phones: %v", err)
	}
	got, _ = api.GetContact(recID)
	if len(got.Phones) != 0 {
		t.Errorf("phones should be cleared, got %+v", got.Phones)
	}
}

func TestAPI_UpdateContact_Local_Photo(t *testing.T) {
	api, local, _ := setupAPI(t)

	if err := local.Create("photo@example.com", "Photo Person"); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	got, _ := api.GetContact("photo@example.com")
	recID := got.ID

	// Set a photo.
	if err := api.UpdateContact(recID, coreapi.ContactPatch{
		Photo: &coreapi.ContactPhoto{
			Data:      "VEVTVERBVEE=",
			MediaType: "image/jpeg",
		},
	}); err != nil {
		t.Fatalf("set photo: %v", err)
	}
	got, _ = api.GetContact(recID)
	if got.PhotoData != "VEVTVERBVEE=" {
		t.Errorf("PhotoData = %q, want VEVTVERBVEE=", got.PhotoData)
	}
	if got.PhotoMediaType != "image/jpeg" {
		t.Errorf("PhotoMediaType = %q, want image/jpeg", got.PhotoMediaType)
	}

	// Remove with empty struct.
	if err := api.UpdateContact(recID, coreapi.ContactPatch{
		Photo: &coreapi.ContactPhoto{},
	}); err != nil {
		t.Fatalf("clear photo: %v", err)
	}
	got, _ = api.GetContact(recID)
	if got.PhotoData != "" || got.PhotoMediaType != "" || got.PhotoURL != "" {
		t.Errorf("photo not cleared: data=%q media=%q url=%q", got.PhotoData, got.PhotoMediaType, got.PhotoURL)
	}
}
