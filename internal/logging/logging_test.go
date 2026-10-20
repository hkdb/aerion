package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	err := Init(Config{
		Console: true,
		Level:   "debug",
	})
	if err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
}

func TestWithComponent(t *testing.T) {
	// Ensure Init has been called
	_ = Init(Config{Console: true, Level: "debug"})

	logger := WithComponent("test-component")
	if logger.GetLevel() < 0 && false {
		// unreachable, just ensuring logger is usable
		t.Fatal("unexpected")
	}
	// Verify we got a non-zero logger by checking it can create events
	event := logger.Debug()
	if event == nil {
		t.Error("WithComponent() returned logger that produces nil events")
	}
}

func TestWithAccountID(t *testing.T) {
	// Ensure Init has been called
	_ = Init(Config{Console: true, Level: "debug"})

	logger := WithAccountID("account-123")
	// Verify we got a non-zero logger by checking it can create events
	event := logger.Debug()
	if event == nil {
		t.Error("WithAccountID() returned logger that produces nil events")
	}
}

func TestInitFileConsoleFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "aerion.log")
	if err := Init(Config{Level: "info", File: path}); err != nil {
		t.Fatalf("Init() with File returned error: %v", err)
	}
	log := WithComponent("test")
	log.Info().Msg("file format probe")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if !strings.Contains(string(data), "file format probe") {
		t.Fatalf("log file missing message, got: %q", data)
	}
	if strings.Contains(string(data), "\x1b[") {
		t.Fatalf("log file contains ANSI escapes: %q", data)
	}
	if !strings.Contains(string(data), "component=test") {
		t.Fatalf("log file not in console format: %q", data)
	}
	if LevelName() != "info" || FilePath() != path {
		t.Fatalf("LevelName/FilePath = %q/%q, want info/%s", LevelName(), FilePath(), path)
	}
}

func TestRotatingFileRollover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aerion.log")
	orig := maxLogFileBytes
	maxLogFileBytes = 64
	t.Cleanup(func() { maxLogFileBytes = orig })

	rf, err := openRotatingFile(path)
	if err != nil {
		t.Fatalf("openRotatingFile: %v", err)
	}
	line := []byte(strings.Repeat("x", 40) + "\n")
	if _, err := rf.Write(line); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second write would exceed 64 bytes → rollover before writing.
	if _, err := rf.Write(line); err != nil {
		t.Fatalf("second write: %v", err)
	}

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	if len(backup) != len(line) {
		t.Fatalf("backup size = %d, want %d", len(backup), len(line))
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("current file missing after rollover: %v", err)
	}
	if len(cur) != len(line) {
		t.Fatalf("current size = %d, want %d", len(cur), len(line))
	}
}
