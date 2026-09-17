package appstate

import (
	"database/sql"
	"encoding/json"

	_ "modernc.org/sqlite"
)

// KeyWindowGeometry is the app_state key holding the main window's last
// geometry. Kept separate from ui_state — the frontend never reads it, and a
// separate row avoids merge hazards with SaveUIState round-trips.
const KeyWindowGeometry = "window_geometry"

// WindowGeometry is the persisted main-window geometry. Width/Height are the
// last NORMAL (unmaximized) size; Maximized restores the maximized state
// without clobbering the normal size underneath it.
type WindowGeometry struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Maximized bool `json:"maximized"`
}

// GetWindowGeometry returns the stored geometry, or ok=false when never saved.
func (s *Store) GetWindowGeometry() (WindowGeometry, bool) {
	var g WindowGeometry
	value, err := s.Get(KeyWindowGeometry)
	if err != nil || value == "" {
		return g, false
	}
	if err := json.Unmarshal([]byte(value), &g); err != nil {
		return g, false
	}
	return g, true
}

// SetWindowGeometry persists the geometry.
func (s *Store) SetWindowGeometry(g WindowGeometry) error {
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return s.Set(KeyWindowGeometry, string(data))
}

// ReadWindowGeometry opens the database directly to read the persisted window
// geometry. Used in main.go before wails.Run() when the full DB isn't
// initialized yet (same pattern as settings.ReadNativeTitleBar). Returns
// ok=false on any error (first run, missing DB, malformed row).
func ReadWindowGeometry(dbPath string) (WindowGeometry, bool) {
	var g WindowGeometry
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return g, false
	}
	defer db.Close()

	var value string
	if err := db.QueryRow("SELECT value FROM app_state WHERE key = ?", KeyWindowGeometry).Scan(&value); err != nil {
		return g, false
	}
	if err := json.Unmarshal([]byte(value), &g); err != nil {
		return g, false
	}
	return g, true
}
