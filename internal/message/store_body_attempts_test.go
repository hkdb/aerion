package message

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hkdb/aerion/internal/database"
)

// newTestStore opens a migrated temp database and returns a message Store plus
// a seeded (accountID, folderID) to attach messages to.
func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const accountID = "acct-1"
	const folderID = "folder-1"

	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		accountID, "Test", "test@example.com", "imap.example.com", "smtp.example.com", "test@example.com",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO folders (id, account_id, name, path, folder_type)
		 VALUES (?, ?, ?, ?, ?)`,
		folderID, accountID, "INBOX", "INBOX", "inbox",
	); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	return NewStore(db), accountID, folderID
}

func mustCreate(t *testing.T, s *Store, m *Message) {
	t.Helper()
	if err := s.Create(m); err != nil {
		t.Fatalf("Create(%s): %v", m.ID, err)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestGetMessagesWithoutBody_Selection verifies which messages are considered to
// still need a body fetch.
func TestGetMessagesWithoutBody_Selection(t *testing.T) {
	s, accountID, folderID := newTestStore(t)
	now := time.Now().UTC()

	// A: never fetched -> needs body.
	mustCreate(t, s, &Message{ID: "A", AccountID: accountID, FolderID: folderID, UID: 1, Date: now, BodyFetched: false})
	// B: fetched with content -> does NOT need body.
	mustCreate(t, s, &Message{ID: "B", AccountID: accountID, FolderID: folderID, UID: 2, Date: now, BodyFetched: true, BodyText: "hello"})
	// C: fetched but empty (failed parse) -> needs body (until attempts exhausted).
	mustCreate(t, s, &Message{ID: "C", AccountID: accountID, FolderID: folderID, UID: 3, Date: now, BodyFetched: true})

	ids, err := s.GetMessagesWithoutBody(folderID, 100, time.Time{})
	if err != nil {
		t.Fatalf("GetMessagesWithoutBody: %v", err)
	}

	if !contains(ids, "A") {
		t.Errorf("expected never-fetched message A to need a body, got %v", ids)
	}
	if contains(ids, "B") {
		t.Errorf("did not expect fetched-with-content message B, got %v", ids)
	}
	if !contains(ids, "C") {
		t.Errorf("expected empty-body message C to need a body, got %v", ids)
	}
}

// TestIncrementBodyAttempts_ExhaustsAndExcludes verifies that the persistent
// attempt counter eventually removes a permanently-unparseable message from the
// fetch queue, and that the threshold-crossing IDs are reported exactly once.
func TestIncrementBodyAttempts_ExhaustsAndExcludes(t *testing.T) {
	s, accountID, folderID := newTestStore(t)
	now := time.Now().UTC()

	// Empty-body message that will never parse.
	mustCreate(t, s, &Message{ID: "C", AccountID: accountID, FolderID: folderID, UID: 3, Date: now, BodyFetched: true})

	stillNeedsBody := func() bool {
		ids, err := s.GetMessagesWithoutBody(folderID, 100, time.Time{})
		if err != nil {
			t.Fatalf("GetMessagesWithoutBody: %v", err)
		}
		return contains(ids, "C")
	}

	// Attempts 1 and 2: still under the budget, still queued, not yet "exhausted".
	for i := 1; i < MaxBodyParseAttempts; i++ {
		exhausted, err := s.IncrementBodyAttempts([]string{"C"})
		if err != nil {
			t.Fatalf("IncrementBodyAttempts #%d: %v", i, err)
		}
		if len(exhausted) != 0 {
			t.Errorf("attempt %d: expected no exhausted IDs, got %v", i, exhausted)
		}
		if !stillNeedsBody() {
			t.Errorf("attempt %d: message should still be queued for body fetch", i)
		}
	}

	// Final attempt reaches the budget: reported as exhausted and dropped from the queue.
	exhausted, err := s.IncrementBodyAttempts([]string{"C"})
	if err != nil {
		t.Fatalf("IncrementBodyAttempts final: %v", err)
	}
	if len(exhausted) != 1 || exhausted[0] != "C" {
		t.Errorf("expected final attempt to report [C] exhausted, got %v", exhausted)
	}
	if stillNeedsBody() {
		t.Errorf("after %d attempts the message must no longer be queued for body fetch", MaxBodyParseAttempts)
	}

	// ResetBodyAttempts re-queues it (escape hatch for an improved parser).
	if err := s.ResetBodyAttempts(); err != nil {
		t.Fatalf("ResetBodyAttempts: %v", err)
	}
	if !stillNeedsBody() {
		t.Errorf("after ResetBodyAttempts the message should be queued again")
	}
}

// TestIncrementBodyAttempts_GivesUpOnNeverFetched verifies the attempt budget also
// applies to messages the server never returned a body for (body_fetched stays 0),
// so they don't loop forever either.
func TestIncrementBodyAttempts_GivesUpOnNeverFetched(t *testing.T) {
	s, accountID, folderID := newTestStore(t)
	now := time.Now().UTC()
	mustCreate(t, s, &Message{ID: "A", AccountID: accountID, FolderID: folderID, UID: 1, Date: now, BodyFetched: false})

	for i := 0; i < MaxBodyParseAttempts; i++ {
		if _, err := s.IncrementBodyAttempts([]string{"A"}); err != nil {
			t.Fatalf("IncrementBodyAttempts #%d: %v", i, err)
		}
	}

	ids, err := s.GetMessagesWithoutBody(folderID, 100, time.Time{})
	if err != nil {
		t.Fatalf("GetMessagesWithoutBody: %v", err)
	}
	if contains(ids, "A") {
		t.Errorf("never-fetched message should be given up after %d attempts, got %v", MaxBodyParseAttempts, ids)
	}
}
