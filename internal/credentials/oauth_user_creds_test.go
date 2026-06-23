package credentials

import (
	"path/filepath"
	"testing"

	"github.com/hkdb/aerion/internal/database"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := NewStore(db.DB, t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Tests run without OS keyring access — verify DB fallback path explicitly.
	store.keyringEnabled = false
	return store
}

func TestUserClientCreds_RoundTrip(t *testing.T) {
	store := openTestStore(t)

	// Initially nothing stored.
	if store.HasUserClientCreds("google-contacts") {
		t.Fatal("expected no creds initially")
	}
	id, secret, ok, err := store.GetUserClientCreds("google-contacts")
	if err != nil {
		t.Fatalf("Get on missing: %v", err)
	}
	if ok || id != "" || secret != "" {
		t.Fatalf("Get on missing returned ok=%v id=%q secret=%q", ok, id, secret)
	}

	// Set.
	if err := store.SetUserClientCreds("google-contacts", "the-id", "the-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !store.HasUserClientCreds("google-contacts") {
		t.Fatal("expected HasUserClientCreds true after Set")
	}

	// Get back.
	id, secret, ok, err = store.GetUserClientCreds("google-contacts")
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after Set")
	}
	if id != "the-id" || secret != "the-secret" {
		t.Fatalf("got id=%q secret=%q, want the-id/the-secret", id, secret)
	}

	// Overwrite.
	if err := store.SetUserClientCreds("google-contacts", "new-id", "new-secret"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	id, secret, _, _ = store.GetUserClientCreds("google-contacts")
	if id != "new-id" || secret != "new-secret" {
		t.Fatalf("overwrite failed: got id=%q secret=%q", id, secret)
	}

	// Clear.
	if err := store.ClearUserClientCreds("google-contacts"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if store.HasUserClientCreds("google-contacts") {
		t.Fatal("expected no creds after Clear")
	}
}

func TestUserClientCreds_EmptySecretAllowed(t *testing.T) {
	// Microsoft desktop apps use PKCE and have no client_secret. Make sure
	// SetUserClientCreds accepts an empty secret.
	store := openTestStore(t)
	if err := store.SetUserClientCreds("microsoft-mail", "ms-id", ""); err != nil {
		t.Fatalf("Set with empty secret: %v", err)
	}
	id, secret, ok, _ := store.GetUserClientCreds("microsoft-mail")
	if !ok || id != "ms-id" || secret != "" {
		t.Fatalf("round-trip empty secret: ok=%v id=%q secret=%q", ok, id, secret)
	}
}

func TestUserClientCreds_RequiresClientID(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetUserClientCreds("google-contacts", "", "some-secret"); err == nil {
		t.Fatal("expected error when client_id is empty")
	}
}

func TestUserClientCreds_IsolatedPerConfigID(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetUserClientCreds("google-contacts", "a", "1"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := store.SetUserClientCreds("microsoft-contacts", "b", "2"); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if store.HasUserClientCreds("google-calendar") {
		t.Fatal("unrelated config id should not be present")
	}

	idA, _, _, _ := store.GetUserClientCreds("google-contacts")
	idB, _, _, _ := store.GetUserClientCreds("microsoft-contacts")
	if idA != "a" || idB != "b" {
		t.Fatalf("isolation broken: a=%q b=%q", idA, idB)
	}

	// Clearing one doesn't affect the other.
	if err := store.ClearUserClientCreds("google-contacts"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if store.HasUserClientCreds("google-contacts") {
		t.Fatal("google-contacts should be cleared")
	}
	if !store.HasUserClientCreds("microsoft-contacts") {
		t.Fatal("microsoft-contacts should still be set")
	}
}
