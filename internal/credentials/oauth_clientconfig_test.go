package credentials

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hkdb/aerion/internal/database"
)

func newTestStore(t *testing.T) (*Store, *database.DB) {
	t.Helper()
	tmp := t.TempDir()
	db, err := database.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := NewStore(db.DB, tmp)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, db
}

func insertTestAccount(t *testing.T, db *database.DB, id string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		VALUES (?, 'Test', ?, 'imap.example.com', 'smtp.example.com', ?)
	`, id, id+"@example.com", id+"@example.com")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

// TestUpdateOAuthTokensForClientConfig_RefreshRotation guards the broker's
// refresh-persistence fix: a refresh that returns a NEW refresh token must
// persist BOTH columns, and a refresh that returns an empty refresh token must
// NOT overwrite the still-valid stored one.
func TestUpdateOAuthTokensForClientConfig_RefreshRotation(t *testing.T) {
	store, db := newTestStore(t)
	insertTestAccount(t, db, "acct")
	// Non-mail slot → exercises the per-(account,client_config) oauth_tokens path.
	const cfg = "google-contacts"

	if err := store.SetOAuthTokensForClientConfig("acct", cfg, &OAuthTokens{
		Provider:     "google",
		AccessToken:  "A0",
		RefreshToken: "R0",
		ExpiresAt:    time.Now().Add(time.Hour),
		Scopes:       []string{"contacts"},
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	// Refresh that ROTATES the refresh token — both must be persisted.
	if err := store.UpdateOAuthTokensForClientConfig("acct", cfg, "A1", "R1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("update with rotation: %v", err)
	}
	got, err := store.GetOAuthTokensForClientConfig("acct", cfg)
	if err != nil {
		t.Fatalf("get after rotation: %v", err)
	}
	if got.AccessToken != "A1" {
		t.Errorf("access = %q, want A1", got.AccessToken)
	}
	if got.RefreshToken != "R1" {
		t.Errorf("refresh = %q, want R1 (rotated refresh token must be persisted)", got.RefreshToken)
	}

	// Refresh that returns NO new refresh token — must keep R1, not wipe it.
	if err := store.UpdateOAuthTokensForClientConfig("acct", cfg, "A2", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("update without rotation: %v", err)
	}
	got, err = store.GetOAuthTokensForClientConfig("acct", cfg)
	if err != nil {
		t.Fatalf("get after empty refresh: %v", err)
	}
	if got.AccessToken != "A2" {
		t.Errorf("access = %q, want A2", got.AccessToken)
	}
	if got.RefreshToken != "R1" {
		t.Errorf("refresh = %q, want R1 preserved (empty refresh must not overwrite a good token)", got.RefreshToken)
	}
}
