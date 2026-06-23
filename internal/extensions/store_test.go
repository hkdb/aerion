package extensions

import (
	"testing"
)

func TestOpenStore_AppliesMigrations(t *testing.T) {
	dir := t.TempDir()
	migs := []Migration{
		{Version: 1, SQL: `CREATE TABLE foo (id INTEGER PRIMARY KEY, name TEXT)`},
		{Version: 2, SQL: `CREATE TABLE bar (id INTEGER PRIMARY KEY, value TEXT)`},
	}

	s, err := OpenStore(dir, "testext", migs)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Verify both tables exist by inserting and querying
	if _, err := s.DB().Exec(`INSERT INTO foo (name) VALUES (?)`, "hello"); err != nil {
		t.Fatalf("insert into foo: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT INTO bar (value) VALUES (?)`, "world"); err != nil {
		t.Fatalf("insert into bar: %v", err)
	}

	var migCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM migrations`).Scan(&migCount); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if migCount != 2 {
		t.Fatalf("expected 2 applied migrations, got %d", migCount)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen with same migrations — should be idempotent
	s2, err := OpenStore(dir, "testext", migs)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer s2.Close()

	var foundName string
	if err := s2.DB().QueryRow(`SELECT name FROM foo LIMIT 1`).Scan(&foundName); err != nil {
		t.Fatalf("query foo after reopen: %v", err)
	}
	if foundName != "hello" {
		t.Fatalf("expected name 'hello', got %q", foundName)
	}
}

func TestKVStore_GetSetDeleteList(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, "kvtest", nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	kv := s.KV()

	// Get on missing key returns "" + nil (no error)
	v, err := kv.Get("missing")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if v != "" {
		t.Fatalf("expected empty string for missing key, got %q", v)
	}

	if err := kv.Set("foo", "bar"); err != nil {
		t.Fatalf("set foo: %v", err)
	}
	if err := kv.Set("foo:nested", "1"); err != nil {
		t.Fatalf("set foo:nested: %v", err)
	}
	if err := kv.Set("baz", "qux"); err != nil {
		t.Fatalf("set baz: %v", err)
	}

	if v, _ := kv.Get("foo"); v != "bar" {
		t.Fatalf("expected bar, got %q", v)
	}

	// Set on existing key replaces
	if err := kv.Set("foo", "bar2"); err != nil {
		t.Fatalf("set foo (replace): %v", err)
	}
	if v, _ := kv.Get("foo"); v != "bar2" {
		t.Fatalf("expected bar2 after replace, got %q", v)
	}

	// List all
	all, err := kv.List("")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(all), all)
	}

	// List by prefix
	foos, err := kv.List("foo")
	if err != nil {
		t.Fatalf("list foo prefix: %v", err)
	}
	if len(foos) != 2 {
		t.Fatalf("expected 2 keys with 'foo' prefix, got %d: %v", len(foos), foos)
	}

	// Delete
	if err := kv.Delete("foo"); err != nil {
		t.Fatalf("delete foo: %v", err)
	}
	if v, _ := kv.Get("foo"); v != "" {
		t.Fatalf("expected empty after delete, got %q", v)
	}
}

func TestOpenStore_EmptyNameRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenStore(dir, "", nil); err == nil {
		t.Fatal("expected error for empty extension name")
	}
}
