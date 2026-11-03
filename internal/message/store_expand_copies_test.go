package message

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hkdb/aerion/internal/database"
)

// newExpandCopiesTestStore opens a migrated temp DB with one account and two
// folders, returning the raw DB too so a test can plant a negative-UID
// placeholder the way a local move does (`uid = -rowid`).
func newExpandCopiesTestStore(t *testing.T) (*Store, *database.DB) {
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

	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username) VALUES (?, ?, ?, ?, ?, ?)`,
		"acct-1", "Test", "test@example.com", "imap.example.com", "smtp.example.com", "test@example.com",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	for _, f := range [][3]string{{"inbox-1", "INBOX", "inbox"}, {"sent-1", "Sent", "sent"}} {
		if _, err := db.Exec(
			`INSERT INTO folders (id, account_id, name, path, folder_type) VALUES (?, ?, ?, ?, ?)`,
			f[0], "acct-1", f[1], f[1], f[2],
		); err != nil {
			t.Fatalf("seed folder %s: %v", f[0], err)
		}
	}
	return NewStore(db), db
}

func seedCopy(t *testing.T, s *Store, id, folderID, messageID string, uid uint32) {
	t.Helper()
	m := &Message{ID: id, AccountID: "acct-1", FolderID: folderID, UID: uid, MessageID: messageID, Date: time.Now().UTC()}
	if err := s.Create(m); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
}

func TestExpandToFolderCopies(t *testing.T) {
	s, db := newExpandCopiesTestStore(t)
	const mid = "dup@example.com"

	seedCopy(t, s, "a", "inbox-1", mid, 10)
	seedCopy(t, s, "b", "inbox-1", mid, 11)
	seedCopy(t, s, "c", "inbox-1", mid, 12)
	seedCopy(t, s, "sent", "sent-1", mid, 7)                // same Message-ID, other folder: untouched
	seedCopy(t, s, "other", "inbox-1", "x@example.com", 20) // unrelated
	seedCopy(t, s, "blank", "inbox-1", "", 21)              // empty Message-ID: never expands
	seedCopy(t, s, "temp", "inbox-1", mid, 13)
	// Turn "temp" into an in-flight move placeholder (negative UID) — excluded.
	if _, err := db.Exec(`UPDATE messages SET uid = -rowid WHERE id = 'temp'`); err != nil {
		t.Fatalf("plant placeholder: %v", err)
	}

	got, err := s.ExpandToFolderCopies([]string{"a"})
	if err != nil {
		t.Fatalf("ExpandToFolderCopies: %v", err)
	}
	want := map[string]bool{"a": true, "b": true, "c": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly the three inbox copies", got)
	}
	if got[0] != "a" {
		t.Fatalf("input id must come first, got %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected id %q in %v", id, got)
		}
	}

	// Two ids of the same group in the input → no duplicates in the output.
	got, err = s.ExpandToFolderCopies([]string{"a", "b"})
	if err != nil {
		t.Fatalf("ExpandToFolderCopies(a,b): %v", err)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	if len(got) != 3 || seen["a"] != 1 || seen["b"] != 1 || seen["c"] != 1 {
		t.Fatalf("got %v, want a,b,c once each", got)
	}

	// Empty Message-ID expands to itself only.
	got, err = s.ExpandToFolderCopies([]string{"blank"})
	if err != nil {
		t.Fatalf("ExpandToFolderCopies(blank): %v", err)
	}
	if len(got) != 1 || got[0] != "blank" {
		t.Fatalf("got %v, want [blank]", got)
	}

	// Empty input is a no-op.
	if got, err := s.ExpandToFolderCopies(nil); err != nil || len(got) != 0 {
		t.Fatalf("nil input: got %v, %v", got, err)
	}
}
