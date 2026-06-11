package credentials

import (
	"database/sql"
	"fmt"

	gokeyring "github.com/zalando/go-keyring"
)

// User-pickable OAuth slot alias (Settings → OAuth Credentials → pick
// "Aerion mail client"). When the user wants a non-default mapping for one
// of the OAuth slots (e.g., route google-contacts OAuth flows through the
// google-mail client rather than the shipped contacts client), we store
// the chosen target slot ID here. oauth2.ClientConfigForID consults this
// after the user-override step and before the provider chain.
//
// Distinct from user_oauth_clients (which holds user-supplied client_id +
// client_secret pairs); these aliases just redirect lookups between
// existing slot IDs. Both states are exclusive: either the user picked
// "Custom" (and we keep credentials in user_oauth_clients) or the user
// picked an aerion-shipped option (and we keep an alias entry here, or
// neither if the choice is the slot's own default).
//
// Storage: OS keyring primary (one entry per source config id, plaintext
// target slot id), encrypted DB fallback (`user_oauth_slot_aliases` table
// created on demand below). Keyring entry is plaintext because the value
// is non-secret (just a slot id like "google-mail").

const oauthSlotAliasKeyringPrefix = "oauth_slot_alias:"

func (s *Store) ensureSlotAliasTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS user_oauth_slot_aliases (
			config_id    TEXT PRIMARY KEY,
			target_slot  TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// SetOAuthSlotAlias stores configID → targetSlot. Calling with an empty
// targetSlot is treated as a clear. Idempotent.
func (s *Store) SetOAuthSlotAlias(configID, targetSlot string) error {
	if configID == "" {
		return fmt.Errorf("credentials: config id is required")
	}
	if targetSlot == "" {
		return s.ClearOAuthSlotAlias(configID)
	}

	if s.keyringEnabled {
		kerr := gokeyring.Set(serviceName, oauthSlotAliasKeyringPrefix+configID, targetSlot)
		if kerr == nil {
			s.log.Debug().Str("config_id", configID).Str("target", targetSlot).Msg("OAuth slot alias stored in OS keyring")
			s.clearSlotAliasDB(configID)
			return nil
		}
		s.log.Warn().Err(kerr).Str("config_id", configID).Msg("Failed to store OAuth slot alias in OS keyring, falling back to encrypted database")
	}

	if err := s.ensureSlotAliasTable(); err != nil {
		return fmt.Errorf("ensure user_oauth_slot_aliases table: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO user_oauth_slot_aliases (config_id, target_slot, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(config_id) DO UPDATE SET
			target_slot = excluded.target_slot,
			updated_at  = excluded.updated_at
	`, configID, targetSlot); err != nil {
		return fmt.Errorf("store OAuth slot alias: %w", err)
	}
	s.log.Debug().Str("config_id", configID).Str("target", targetSlot).Msg("OAuth slot alias stored in database")
	return nil
}

// GetOAuthSlotAlias returns the stored target slot id for configID, or
// ("", false, nil) if no alias is set.
func (s *Store) GetOAuthSlotAlias(configID string) (string, bool, error) {
	if configID == "" {
		return "", false, nil
	}

	if s.keyringEnabled {
		target, kerr := gokeyring.Get(serviceName, oauthSlotAliasKeyringPrefix+configID)
		if kerr == nil {
			return target, true, nil
		}
		if kerr != gokeyring.ErrNotFound {
			s.log.Warn().Err(kerr).Msg("Error reading OAuth slot alias from keyring, trying fallback")
		}
	}

	if err := s.ensureSlotAliasTable(); err != nil {
		return "", false, fmt.Errorf("ensure user_oauth_slot_aliases table: %w", err)
	}
	var target sql.NullString
	row := s.db.QueryRow(`SELECT target_slot FROM user_oauth_slot_aliases WHERE config_id = ?`, configID)
	switch err := row.Scan(&target); {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("query OAuth slot alias: %w", err)
	}
	if !target.Valid || target.String == "" {
		return "", false, nil
	}
	return target.String, true, nil
}

// ClearOAuthSlotAlias removes any alias set for configID. Idempotent.
func (s *Store) ClearOAuthSlotAlias(configID string) error {
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, oauthSlotAliasKeyringPrefix+configID)
	}
	s.clearSlotAliasDB(configID)
	return nil
}

func (s *Store) clearSlotAliasDB(configID string) {
	_, _ = s.db.Exec(`DELETE FROM user_oauth_slot_aliases WHERE config_id = ?`, configID)
}
