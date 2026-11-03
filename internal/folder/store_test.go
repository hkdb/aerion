package folder

import (
	"path/filepath"
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

// createTestAccount inserts a minimal account row so foreign key constraints are satisfied.
func createTestAccount(t *testing.T, db *database.DB, id string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO accounts (id, name, email, imap_host, imap_port, smtp_host, smtp_port, auth_type, username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, "Test", id+"@test.com", "imap.test.com", 993, "smtp.test.com", 587, "password", id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndGet(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}

	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}
	if f.ID == "" {
		t.Fatal("expected ID to be set after create")
	}

	got, err := store.Get(f.ID)
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got == nil {
		t.Fatal("expected folder, got nil")
	}
	if got.Name != "Inbox" {
		t.Errorf("name: got %q, want %q", got.Name, "Inbox")
	}
	if got.Path != "INBOX" {
		t.Errorf("path: got %q, want %q", got.Path, "INBOX")
	}
	if got.Type != TypeInbox {
		t.Errorf("type: got %q, want %q", got.Type, TypeInbox)
	}
	if got.AccountID != "acc1" {
		t.Errorf("accountID: got %q, want %q", got.AccountID, "acc1")
	}
}

func TestGetByPath(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}

	got, err := store.GetByPath("acc1", "INBOX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected folder, got nil")
	}
	if got.ID != f.ID {
		t.Errorf("id: got %q, want %q", got.ID, f.ID)
	}

	// Nonexistent path
	got, err = store.GetByPath("acc1", "NONEXISTENT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent path")
	}
}

func TestList(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	for _, name := range []string{"Inbox", "Sent"} {
		f := &Folder{
			AccountID: "acc1",
			Name:      name,
			Path:      name,
			Type:      TypeFolder,
		}
		if err := store.Create(f); err != nil {
			t.Fatalf("unexpected error creating %s: %v", name, err)
		}
	}

	folders, err := store.List("acc1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(folders) != 2 {
		t.Errorf("got %d folders, want 2", len(folders))
	}
}

func TestUpdate(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "OldName",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}

	f.Name = "NewName"
	if err := store.Update(f); err != nil {
		t.Fatalf("unexpected error on update: %v", err)
	}

	got, err := store.Get(f.ID)
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got.Name != "NewName" {
		t.Errorf("name: got %q, want %q", got.Name, "NewName")
	}
}

func TestUpdateSyncState(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}

	if err := store.UpdateSyncState(f.ID, 100, 200, 300, 50, 10); err != nil {
		t.Fatalf("unexpected error on update sync state: %v", err)
	}

	got, err := store.Get(f.ID)
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got.UIDValidity != 100 {
		t.Errorf("uidValidity: got %d, want 100", got.UIDValidity)
	}
	if got.UIDNext != 200 {
		t.Errorf("uidNext: got %d, want 200", got.UIDNext)
	}
	if got.HighestModSeq != 300 {
		t.Errorf("highestModSeq: got %d, want 300", got.HighestModSeq)
	}
	if got.TotalCount != 50 {
		t.Errorf("totalCount: got %d, want 50", got.TotalCount)
	}
	if got.UnreadCount != 10 {
		t.Errorf("unreadCount: got %d, want 10", got.UnreadCount)
	}
	if got.LastSync == nil {
		t.Error("expected lastSync to be set")
	}
}

func TestDelete(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}

	if err := store.Delete(f.ID); err != nil {
		t.Fatalf("unexpected error on delete: %v", err)
	}

	got, err := store.Get(f.ID)
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete, got folder")
	}
}

func TestUpsert(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{
		AccountID: "acc1",
		Name:      "Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Create(f); err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}

	// Upsert with same account+path but different name
	f2 := &Folder{
		AccountID: "acc1",
		Name:      "Updated Inbox",
		Path:      "INBOX",
		Type:      TypeInbox,
	}
	if err := store.Upsert(f2); err != nil {
		t.Fatalf("unexpected error on upsert: %v", err)
	}

	// Should have reused the same ID
	if f2.ID != f.ID {
		t.Errorf("upsert should reuse ID: got %q, want %q", f2.ID, f.ID)
	}

	got, err := store.Get(f.ID)
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got.Name != "Updated Inbox" {
		t.Errorf("name: got %q, want %q", got.Name, "Updated Inbox")
	}

	// Verify only one folder exists
	folders, err := store.List("acc1")
	if err != nil {
		t.Fatalf("unexpected error on list: %v", err)
	}
	if len(folders) != 1 {
		t.Errorf("got %d folders, want 1", len(folders))
	}
}

func TestAdjustCounts(t *testing.T) {
	db := openTestDB(t)
	createTestAccount(t, db, "acc1")
	store := NewStore(db)

	f := &Folder{AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: TypeInbox, TotalCount: 7739, UnreadCount: 7739}
	if err := store.Create(f); err != nil {
		t.Fatal(err)
	}

	got, err := store.AdjustCounts(f.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 7739 || got.UnreadCount != 7738 {
		t.Fatalf("after -1 unread: total=%d unread=%d", got.TotalCount, got.UnreadCount)
	}

	got, err = store.AdjustCounts(f.ID, -2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 7737 || got.UnreadCount != 7741 {
		t.Fatalf("after -2 total / +3 unread: total=%d unread=%d", got.TotalCount, got.UnreadCount)
	}

	// Clamp at zero
	got, err = store.AdjustCounts(f.ID, -100000, -100000)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 0 || got.UnreadCount != 0 {
		t.Fatalf("clamp: total=%d unread=%d, want 0/0", got.TotalCount, got.UnreadCount)
	}

	if _, err := store.AdjustCounts("missing", 1, 1); err == nil {
		t.Fatal("AdjustCounts on an unknown folder returned nil error")
	}
}
