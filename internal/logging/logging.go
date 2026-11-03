// Package logging provides structured logging for the application
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	// Logger is the global logger instance
	Logger zerolog.Logger

	// current records the config the logger was last initialised with so
	// the main process can forward the same options to child processes
	// (the detached composer) without threading them through App.
	current Config

	// maxLogFileBytes is the rollover threshold for the log file. A package
	// var (not a const) so tests can lower it.
	maxLogFileBytes int64 = 10 << 20
)

func init() {
	// Default to fatal-only logging until Init() is called
	// This prevents log spam before the app fully initializes
	Logger = zerolog.New(os.Stderr).Level(zerolog.FatalLevel)
}

// Config holds logging configuration
type Config struct {
	Level   string // fatal, error, warn, info, debug
	Console bool   // Enable console output (stderr)
	File    string // Also write to this file (empty to disable); 10 MB rollover to <File>.1
}

// Init initializes the global logger
func Init(cfg Config) error {
	// Parse level
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}

	var writers []io.Writer

	// Console output
	if cfg.Console {
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		}
		writers = append(writers, consoleWriter)
	}

	// File output — same human-readable format as the terminal, minus
	// colour, so a captured file reads and greps like the dev console.
	if cfg.File != "" {
		rf, err := openRotatingFile(cfg.File)
		if err != nil {
			return err
		}
		writers = append(writers, zerolog.ConsoleWriter{
			Out:        rf,
			NoColor:    true,
			TimeFormat: time.RFC3339,
		})
	}

	// Combine writers
	var output io.Writer
	switch len(writers) {
	case 0:
		output = os.Stderr
	case 1:
		output = writers[0]
	default:
		output = zerolog.MultiLevelWriter(writers...)
	}

	// Create logger
	Logger = zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Caller().
		Logger()

	current = cfg
	return nil
}

// LevelName returns the level name Init was last called with ("" before Init).
func LevelName() string {
	return current.Level
}

// FilePath returns the log file path Init was last called with ("" if none).
func FilePath() string {
	return current.File
}

// rotatingFile is an append-only log file that rolls over to <path>.1 once
// it exceeds maxLogFileBytes. One previous generation is kept; older ones
// are discarded. It is safe for concurrent writers.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func openRotatingFile(path string) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	rf := &rotatingFile{path: path}
	if err := rf.open(); err != nil {
		return nil, err
	}
	return rf, nil
}

// open (re)opens the file for appending and seeds size from disk so the
// cap counts bytes written by previous runs too. Caller holds mu (or is
// the constructor).
func (rf *rotatingFile) open() error {
	f, err := os.OpenFile(rf.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	rf.file = f
	rf.size = info.Size()
	return nil
}

func (rf *rotatingFile) Write(p []byte) (int, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.size+int64(len(p)) > maxLogFileBytes {
		if err := rf.rotate(); err != nil {
			return 0, fmt.Errorf("log rotate: %w", err)
		}
	}
	n, err := rf.file.Write(p)
	rf.size += int64(n)
	return n, err
}

// rotate closes the current file, moves it to <path>.1 (replacing any
// previous .1) and opens a fresh file. Caller holds mu.
func (rf *rotatingFile) rotate() error {
	if err := rf.file.Close(); err != nil {
		return err
	}
	backup := rf.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(rf.path, backup); err != nil {
		return err
	}
	return rf.open()
}

// WithComponent returns a logger with a component field
func WithComponent(component string) zerolog.Logger {
	return Logger.With().Str("component", component).Logger()
}

// WithAccountID returns a logger with an account ID field
func WithAccountID(accountID string) zerolog.Logger {
	return Logger.With().Str("account_id", accountID).Logger()
}

// Debug logs a debug message
func Debug() *zerolog.Event {
	return Logger.Debug()
}

// Info logs an info message
func Info() *zerolog.Event {
	return Logger.Info()
}

// Warn logs a warning message
func Warn() *zerolog.Event {
	return Logger.Warn()
}

// Error logs an error message
func Error() *zerolog.Event {
	return Logger.Error()
}

// Fatal logs a fatal message and exits
func Fatal() *zerolog.Event {
	return Logger.Fatal()
}
