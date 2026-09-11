package customcss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPath(t *testing.T) {
	configDir := t.TempDir()
	got := Path(configDir)
	want := filepath.Join(configDir, Filename)
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestLoad(t *testing.T) {
	configDir := t.TempDir()
	path := Path(configDir)

	css, err := Load(path)
	if err != nil {
		t.Fatalf("Load() missing file returned error: %v", err)
	}
	if css != "" {
		t.Fatalf("Load() missing file = %q, want empty string", css)
	}

	want := ":root { --primary: 190 80% 55%; }\n"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatalf("write custom CSS: %v", err)
	}

	css, err = Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if css != want {
		t.Fatalf("Load() = %q, want %q", css, want)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxSize+1)), 0600); err != nil {
		t.Fatalf("write oversized custom CSS: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() oversized file returned nil error")
	}
}

func TestWatcherLifecycle(t *testing.T) {
	configDir := t.TempDir()
	path := Path(configDir)
	watcher, err := NewWatcher(configDir)
	if err != nil {
		t.Fatalf("NewWatcher() returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	changes := make(chan string, 10)
	errors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		watcher.Run(ctx, func(css string) {
			changes <- css
		}, func(err error) {
			errors <- err
		})
		close(done)
	}()

	if err := os.WriteFile(filepath.Join(configDir, "unrelated.css"), []byte("body {}"), 0600); err != nil {
		t.Fatalf("write unrelated CSS: %v", err)
	}
	select {
	case css := <-changes:
		t.Fatalf("watcher emitted change for unrelated file: %q", css)
	case err := <-errors:
		t.Fatalf("watcher returned error: %v", err)
	case <-time.After(2 * debounceDelay):
	}

	writeAndExpect := func(contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatalf("write custom CSS: %v", err)
		}
		expectChange(t, changes, errors, contents)
	}

	writeAndExpect(":root { --primary: red; }")
	writeAndExpect(":root { --primary: blue; }")

	temporaryPath := filepath.Join(configDir, "custom.css.tmp")
	atomicContents := ":root { --primary: green; }"
	if err := os.WriteFile(temporaryPath, []byte(atomicContents), 0600); err != nil {
		t.Fatalf("write temporary custom CSS: %v", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		t.Fatalf("replace custom CSS atomically: %v", err)
	}
	expectChange(t, changes, errors, atomicContents)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove custom CSS: %v", err)
	}
	expectChange(t, changes, errors, "")

	writeAndExpect("body { font-size: 15px; }")

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher shutdown")
	}
}

func expectChange(t *testing.T, changes <-chan string, errors <-chan error, want string) {
	t.Helper()
	select {
	case got := <-changes:
		if got != want {
			t.Fatalf("watcher change = %q, want %q", got, want)
		}
	case err := <-errors:
		t.Fatalf("watcher returned error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for watcher change %q", want)
	}
}
