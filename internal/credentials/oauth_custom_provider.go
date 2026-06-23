package credentials

import (
	"database/sql"
	"encoding/json"
	"fmt"

	gokeyring "github.com/zalando/go-keyring"
)

// Per-account custom OAuth provider config — the storage that makes "bring your own
// OAuth app" generic-IMAP accounts work long-term. Unlike oauth_user_creds.go (which
// holds project-level client_id/secret overrides keyed by config SLOT), this stores the
// FULL provider definition — authorization + token endpoints, scopes, and client
// credentials — for a single ACCOUNT whose OAuth provider is not one Aerion ships
// (oauth_tokens.provider == "custom").
//
// It exists because oauth2.GetProvider("custom") intentionally fails: the refresh and
// re-authorization paths rebuild an oauth2.ProviderConfig from this record instead of
// looking one up by name. Without it, a custom-OAuth account could authenticate once and
// then never refresh.
//
// Storage mirrors oauth_user_creds.go: OS keyring primary (one JSON entry per account),
// encrypted-DB fallback (oauth_custom_providers, created on demand). The record carries
// the client_secret, so the DB fallback is always encrypted. Never read back to the
// frontend — used only internally for refresh/reauth.

const customOAuthProviderKeyringPrefix = "oauth_custom_provider:"

// CustomOAuthProvider is the JSON shape persisted per account. The field set mirrors the
// parts of oauth2.ProviderConfig needed to resume auth-code and refresh flows.
type CustomOAuthProvider struct {
	AuthURL          string   `json:"auth_url"`
	TokenURL         string   `json:"token_url"`
	UserinfoEndpoint string   `json:"userinfo_endpoint"`
	Scopes           []string `json:"scopes"`
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret"`
}

// ensureCustomProvidersTable creates the fallback DB table if needed. Idempotent.
func (s *Store) ensureCustomProvidersTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_custom_providers (
			account_id TEXT PRIMARY KEY,
			encrypted  TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// SetCustomOAuthProvider stores the custom provider config for an account.
func (s *Store) SetCustomOAuthProvider(accountID string, cfg CustomOAuthProvider) error {
	if accountID == "" {
		return fmt.Errorf("credentials: account id is required")
	}
	if cfg.AuthURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" {
		return fmt.Errorf("credentials: custom OAuth provider requires auth URL, token URL, and client ID")
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal custom oauth provider: %w", err)
	}

	if s.keyringEnabled {
		kerr := gokeyring.Set(serviceName, customOAuthProviderKeyringPrefix+accountID, string(payload))
		if kerr == nil {
			s.log.Debug().Str("account_id", accountID).Msg("custom OAuth provider stored in OS keyring")
			// Keyring is primary — clear any encrypted-DB copy.
			s.clearCustomOAuthProviderDB(accountID)
			return nil
		}
		s.log.Warn().Err(kerr).Str("account_id", accountID).Msg("Failed to store custom OAuth provider in OS keyring, falling back to encrypted database")
	}

	if err := s.ensureCustomProvidersTable(); err != nil {
		return fmt.Errorf("ensure oauth_custom_providers table: %w", err)
	}
	encrypted, err := s.encryptor.Encrypt(string(payload))
	if err != nil {
		return fmt.Errorf("encrypt custom oauth provider: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO oauth_custom_providers (account_id, encrypted, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(account_id) DO UPDATE SET
			encrypted  = excluded.encrypted,
			updated_at = excluded.updated_at
	`, accountID, encrypted); err != nil {
		return fmt.Errorf("store custom oauth provider: %w", err)
	}
	s.log.Debug().Str("account_id", accountID).Msg("custom OAuth provider stored in encrypted database")
	return nil
}

// GetCustomOAuthProvider retrieves the custom provider config for an account, or
// (zero, false, nil) when none is set. Errors are returned only for real failures.
func (s *Store) GetCustomOAuthProvider(accountID string) (CustomOAuthProvider, bool, error) {
	var zero CustomOAuthProvider
	if accountID == "" {
		return zero, false, nil
	}

	if s.keyringEnabled {
		payload, kerr := gokeyring.Get(serviceName, customOAuthProviderKeyringPrefix+accountID)
		if kerr == nil {
			var cfg CustomOAuthProvider
			if jerr := json.Unmarshal([]byte(payload), &cfg); jerr != nil {
				return zero, false, fmt.Errorf("parse custom oauth provider from keyring: %w", jerr)
			}
			return cfg, true, nil
		}
		if kerr != gokeyring.ErrNotFound {
			s.log.Warn().Err(kerr).Msg("Error reading custom OAuth provider from keyring, trying fallback")
		}
	}

	if err := s.ensureCustomProvidersTable(); err != nil {
		return zero, false, fmt.Errorf("ensure oauth_custom_providers table: %w", err)
	}
	var encrypted sql.NullString
	row := s.db.QueryRow(`SELECT encrypted FROM oauth_custom_providers WHERE account_id = ?`, accountID)
	switch err := row.Scan(&encrypted); {
	case err == sql.ErrNoRows:
		return zero, false, nil
	case err != nil:
		return zero, false, fmt.Errorf("query custom oauth provider: %w", err)
	}
	if !encrypted.Valid || encrypted.String == "" {
		return zero, false, nil
	}
	plaintext, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return zero, false, fmt.Errorf("decrypt custom oauth provider: %w", err)
	}
	var cfg CustomOAuthProvider
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		return zero, false, fmt.Errorf("parse custom oauth provider: %w", err)
	}
	return cfg, true, nil
}

// DeleteCustomOAuthProvider removes any stored custom provider config. Idempotent.
func (s *Store) DeleteCustomOAuthProvider(accountID string) error {
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, customOAuthProviderKeyringPrefix+accountID)
	}
	s.clearCustomOAuthProviderDB(accountID)
	return nil
}

func (s *Store) clearCustomOAuthProviderDB(accountID string) {
	_, _ = s.db.Exec(`DELETE FROM oauth_custom_providers WHERE account_id = ?`, accountID)
}
