// Package customcss loads and watches Aerion's optional user stylesheet.
package customcss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// Filename is the user stylesheet name inside Aerion's configuration directory.
	Filename = "custom.css"

	// MaxSize limits how much user CSS is read and sent to frontend processes.
	MaxSize = 1 << 20

	debounceDelay = 75 * time.Millisecond
)

// Path returns the custom stylesheet path for an Aerion configuration directory.
func Path(configDir string) string {
	return filepath.Join(configDir, Filename)
}

// Load reads a custom stylesheet. A missing file is equivalent to an empty stylesheet.
func Load(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open custom CSS: %w", err)
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil {
		return "", fmt.Errorf("read custom CSS: %w", err)
	}
	if len(contents) > MaxSize {
		return "", fmt.Errorf("custom CSS exceeds %d bytes", MaxSize)
	}

	return string(contents), nil
}

// Watcher watches the configuration directory so atomic replacements of custom.css
// are detected even when the original file inode is removed.
type Watcher struct {
	path       string
	watcher    *fsnotify.Watcher
	initialCSS string
	initialErr error
}

// NewWatcher creates a watcher for custom.css in configDir.
func NewWatcher(configDir string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create custom CSS watcher: %w", err)
	}

	if err := watcher.Add(configDir); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watch custom CSS directory: %w", err)
	}

	path := Path(configDir)
	initialCSS, initialErr := Load(path)

	return &Watcher{
		path:       path,
		watcher:    watcher,
		initialCSS: initialCSS,
		initialErr: initialErr,
	}, nil
}

// Run processes filesystem events until ctx is cancelled. onChange receives the
// complete effective stylesheet, including an empty string when the file is removed.
func (w *Watcher) Run(ctx context.Context, onChange func(string), onError func(error)) {
	defer w.watcher.Close()

	lastCSS := w.initialCSS
	if w.initialErr != nil && onError != nil {
		onError(w.initialErr)
	}

	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	scheduleReload := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounceDelay)
	}

	reload := func() {
		css, err := Load(w.path)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		if css == lastCSS {
			return
		}
		lastCSS = css
		if onChange != nil {
			onChange(css)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != w.path {
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
				scheduleReload()
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if onError != nil {
				onError(fmt.Errorf("custom CSS watcher: %w", err))
			}
		case <-timer.C:
			reload()
		}
	}
}
