package credentials

import (
	"database/sql"
	"encoding/json"
	"fmt"

	gokeyring "github.com/zalando/go-keyring"
)

// User-supplied OAuth client credentials (Settings → OAuth Credentials).
// Distinct from oauth_tokens (which holds per-account access/refresh tokens);
// these are the user's overrides for the PROJECT-level client_id + client_secret
// values that would otherwise come from the shipped build (`aerion-creds` shim
// or per-extension .env). When present, they take priority over shipped values
// in `oauth2.ClientConfigForID`.
//
// Storage: OS keyring primary (one entry per config id, JSON-encoded), encrypted
// DB fallback (`user_oauth_clients` table created on demand below).
//
// Values are NEVER read back to the frontend via the Wails surface — only their
// existence (status). Replacement only via SetUserClientCreds; users cannot
// observe what's currently set, only overwrite it.

const userOAuthKeyringPrefix = "oauth_user_client:"

// userClientCredsRecord is the JSON shape stored in the keyring entry / DB row.
type userClientCredsRecord struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// ensureUserClientsTable creates the fallback DB table if needed. Idempotent
// — safe to call on every NewStore.
func (s *Store) ensureUserClientsTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS user_oauth_clients (
			config_id  TEXT PRIMARY KEY,
			encrypted  TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// SetUserClientCreds stores a user-supplied client_id + client_secret for the
// given OAuth client config id. Pass empty clientSecret for providers that
// don't use one (Microsoft desktop / PKCE).
func (s *Store) SetUserClientCreds(configID, clientID, clientSecret string) error {
	if configID == "" {
		return fmt.Errorf("credentials: config id is required")
	}
	if clientID == "" {
		return fmt.Errorf("credentials: client id is required")
	}

	rec := userClientCredsRecord{ClientID: clientID, ClientSecret: clientSecret}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal user oauth creds: %w", err)
	}

	if s.keyringEnabled {
		kerr := gokeyring.Set(serviceName, userOAuthKeyringPrefix+configID, string(payload))
		if kerr == nil {
			s.log.Debug().Str("config_id", configID).Msg("user OAuth client creds stored in OS keyring")
			// Keyring is primary — clear any encrypted-DB copy.
			s.clearUserClientCredsDB(configID)
			return nil
		}
		// Keyring write failed. Log explicitly before falling back to the
		// encrypted-DB path so keyring corruption / permission issues are
		// visible in diagnostics instead of silently masked.
		s.log.Warn().Err(kerr).Str("config_id", configID).Msg("Failed to store user OAuth creds in OS keyring, falling back to encrypted database")
	}

	if err := s.ensureUserClientsTable(); err != nil {
		return fmt.Errorf("ensure user_oauth_clients table: %w", err)
	}
	encrypted, err := s.encryptor.Encrypt(string(payload))
	if err != nil {
		return fmt.Errorf("encrypt user oauth creds: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO user_oauth_clients (config_id, encrypted, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(config_id) DO UPDATE SET
			encrypted  = excluded.encrypted,
			updated_at = excluded.updated_at
	`, configID, encrypted); err != nil {
		return fmt.Errorf("store user oauth creds: %w", err)
	}
	s.log.Debug().Str("config_id", configID).Msg("user OAuth client creds stored in encrypted database")
	return nil
}

// GetUserClientCreds retrieves a user-supplied client_id + client_secret pair,
// or returns (zero, false, nil) if none has been set for that config id.
// Errors are returned only for real failures (corruption, DB issues, etc.).
func (s *Store) GetUserClientCreds(configID string) (clientID, clientSecret string, ok bool, err error) {
	if configID == "" {
		return "", "", false, nil
	}

	if s.keyringEnabled {
		payload, kerr := gokeyring.Get(serviceName, userOAuthKeyringPrefix+configID)
		if kerr == nil {
			var rec userClientCredsRecord
			if jerr := json.Unmarshal([]byte(payload), &rec); jerr != nil {
				return "", "", false, fmt.Errorf("parse user oauth creds from keyring: %w", jerr)
			}
			return rec.ClientID, rec.ClientSecret, true, nil
		}
		if kerr != gokeyring.ErrNotFound {
			s.log.Warn().Err(kerr).Msg("Error reading user OAuth client creds from keyring, trying fallback")
		}
	}

	if err := s.ensureUserClientsTable(); err != nil {
		return "", "", false, fmt.Errorf("ensure user_oauth_clients table: %w", err)
	}
	var encrypted sql.NullString
	row := s.db.QueryRow(`SELECT encrypted FROM user_oauth_clients WHERE config_id = ?`, configID)
	switch err := row.Scan(&encrypted); {
	case err == sql.ErrNoRows:
		return "", "", false, nil
	case err != nil:
		return "", "", false, fmt.Errorf("query user oauth creds: %w", err)
	}
	if !encrypted.Valid || encrypted.String == "" {
		return "", "", false, nil
	}
	plaintext, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return "", "", false, fmt.Errorf("decrypt user oauth creds: %w", err)
	}
	var rec userClientCredsRecord
	if err := json.Unmarshal([]byte(plaintext), &rec); err != nil {
		return "", "", false, fmt.Errorf("parse user oauth creds: %w", err)
	}
	return rec.ClientID, rec.ClientSecret, true, nil
}

// HasUserClientCreds returns true iff user-supplied creds exist for this id.
// Convenience for status checks without invoking the full read path.
func (s *Store) HasUserClientCreds(configID string) bool {
	_, _, ok, err := s.GetUserClientCreds(configID)
	if err != nil {
		s.log.Warn().Err(err).Str("config_id", configID).Msg("Failed to check user OAuth creds")
		return false
	}
	return ok
}

// ClearUserClientCreds removes any user-supplied creds for the given config id.
// Idempotent — succeeds even when nothing was stored.
func (s *Store) ClearUserClientCreds(configID string) error {
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, userOAuthKeyringPrefix+configID)
	}
	s.clearUserClientCredsDB(configID)
	return nil
}

func (s *Store) clearUserClientCredsDB(configID string) {
	_, _ = s.db.Exec(`DELETE FROM user_oauth_clients WHERE config_id = ?`, configID)
}
