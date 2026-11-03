//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSaveDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No Downloads folder: fall back to home.
	if got := DefaultSaveDir(); got != home {
		t.Fatalf("without Downloads: got %q, want home %q", got, home)
	}

	// Downloads exists: prefer it.
	downloads := filepath.Join(home, "Downloads")
	if err := os.Mkdir(downloads, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := DefaultSaveDir(); got != downloads {
		t.Fatalf("with Downloads: got %q, want %q", got, downloads)
	}

	// A file named Downloads is not a directory: fall back to home.
	if err := os.Remove(downloads); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downloads, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultSaveDir(); got != home {
		t.Fatalf("Downloads is a file: got %q, want home %q", got, home)
	}

	// Home itself missing: nothing usable, let the dialog pick.
	t.Setenv("HOME", filepath.Join(home, "gone"))
	if got := DefaultSaveDir(); got != "" {
		t.Fatalf("missing home: got %q, want empty", got)
	}
}
