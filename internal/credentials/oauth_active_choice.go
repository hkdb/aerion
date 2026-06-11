package credentials

import (
	"database/sql"
	"fmt"
)

// Active-choice marker for the OAuth Credentials picker (Settings →
// Accounts → OAuth Credentials, plus the equivalent extension settings
// dialogs). Recorded per slot to decouple "what the user picked" from
// "what credentials/alias rows happen to exist." Without this marker the
// picker has to delete rows to make a different option take effect —
// which means switching from Custom to "Aerion - Microsoft" silently
// destroys the user's saved Custom credentials. The marker lets the
// resolver route by choice instead, so stored values survive picker
// switches.
//
// Stored in the existing `settings` key/value table (no schema
// migration). Key shape: `oauth_active_choice:<slot_id>`. Value is one
// of "custom" | "aerion-shipped" | "aerion-mail". Empty / missing means
// "no explicit choice recorded yet" — the resolver falls back to
// inferring the active choice from row presence, preserving pre-marker
// behavior for installs upgrading from v0.3.0-build1.

const oauthActiveChoicePrefix = "oauth_active_choice:"

func oauthActiveChoiceKey(configID string) string {
	return oauthActiveChoicePrefix + configID
}

// SetOAuthActiveChoice records the user's explicit picker selection
// for the slot. Choice must be one of "custom" / "aerion-shipped" /
// "aerion-mail" — caller (the Wails-bound SetOAuthCredsChoice handler)
// already validates the value.
func (s *Store) SetOAuthActiveChoice(configID, choice string) error {
	if configID == "" {
		return fmt.Errorf("credentials: config id is required")
	}
	if choice == "" {
		return fmt.Errorf("credentials: choice is required")
	}
	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, oauthActiveChoiceKey(configID), choice); err != nil {
		return fmt.Errorf("store oauth active choice: %w", err)
	}
	return nil
}

// GetOAuthActiveChoice returns the recorded choice for the slot, or
// empty string when no explicit choice has been recorded (in which case
// the resolver falls back to row-presence inference). Errors are
// returned only for real failures.
func (s *Store) GetOAuthActiveChoice(configID string) (string, error) {
	if configID == "" {
		return "", nil
	}
	var value string
	err := s.db.QueryRow(
		`SELECT value FROM settings WHERE key = ?`,
		oauthActiveChoiceKey(configID),
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query oauth active choice: %w", err)
	}
	return value, nil
}

// ClearOAuthActiveChoice removes the recorded choice for the slot.
// Idempotent — succeeds even when nothing was stored. Called by
// account-deletion / extension-disable paths if/when they need to wipe
// per-slot OAuth state.
func (s *Store) ClearOAuthActiveChoice(configID string) error {
	if _, err := s.db.Exec(
		`DELETE FROM settings WHERE key = ?`,
		oauthActiveChoiceKey(configID),
	); err != nil {
		return fmt.Errorf("clear oauth active choice: %w", err)
	}
	return nil
}
