package credentials

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hkdb/aerion/internal/oauth2"
	gokeyring "github.com/zalando/go-keyring"
)

// OAuthTokens represents OAuth2 tokens and metadata for an account
type OAuthTokens struct {
	Provider     string    `json:"provider"`     // "google", "microsoft"
	AccessToken  string    `json:"accessToken"`  // Stored in keyring (sensitive)
	RefreshToken string    `json:"refreshToken"` // Stored in keyring (sensitive)
	ExpiresAt    time.Time `json:"expiresAt"`    // Stored in DB
	Scopes       []string  `json:"scopes"`       // Stored in DB
}


// IsExpired returns true if the access token has expired
func (t *OAuthTokens) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// IsExpiringSoon returns true if the access token will expire within the given duration
func (t *OAuthTokens) IsExpiringSoon(within time.Duration) bool {
	return time.Now().Add(within).After(t.ExpiresAt)
}

// SetOAuthTokens stores OAuth tokens for an account
// Sensitive tokens go to keyring (with DB fallback), metadata goes to DB
func (s *Store) SetOAuthTokens(accountID string, tokens *OAuthTokens) error {
	if tokens == nil {
		return fmt.Errorf("tokens cannot be nil")
	}

	// Store sensitive tokens in keyring (or encrypted DB fallback)
	if err := s.setOAuthAccessToken(accountID, tokens.AccessToken); err != nil {
		return fmt.Errorf("failed to store access token: %w", err)
	}

	if err := s.setOAuthRefreshToken(accountID, tokens.RefreshToken); err != nil {
		return fmt.Errorf("failed to store refresh token: %w", err)
	}

	// Store metadata in database
	scopesJSON, err := json.Marshal(tokens.Scopes)
	if err != nil {
		return fmt.Errorf("failed to marshal scopes: %w", err)
	}

	// Derive client_config_id from provider for legacy callers. New code paths
	// should use SetOAuthTokensForClientConfig for explicit selection.
	clientConfigID := oauth2.ClientConfigIDForProvider(tokens.Provider)
	if clientConfigID == "" {
		return fmt.Errorf("cannot derive client_config_id for provider %q", tokens.Provider)
	}

	_, err = s.db.Exec(`
		INSERT INTO oauth_tokens (account_id, client_config_id, provider, expires_at, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(account_id, client_config_id) DO UPDATE SET
			provider = excluded.provider,
			expires_at = excluded.expires_at,
			scopes = excluded.scopes,
			updated_at = CURRENT_TIMESTAMP
	`, accountID, clientConfigID, tokens.Provider, tokens.ExpiresAt, string(scopesJSON))

	if err != nil {
		return fmt.Errorf("failed to store OAuth metadata: %w", err)
	}

	s.log.Debug().
		Str("account_id", accountID).
		Str("provider", tokens.Provider).
		Time("expires_at", tokens.ExpiresAt).
		Msg("OAuth tokens stored")

	return nil
}

// GetOAuthTokens retrieves the mail OAuth tokens for an account.
//
// This is the legacy single-config accessor that predates the per-(account,
// client_config) token storage. Today there can be multiple oauth_tokens rows
// per account (one per slot — google-mail, google-contacts, google-calendar,
// etc.), so we filter to the canonical mail slot here. Callers that need a
// specific extension slot should use GetOAuthTokensForClientConfig.
//
// Without this filter the Scan would pick whichever row SQLite returned first
// and the provider column would hold an extension slot ID
// (e.g. "google-calendar"), which then breaks downstream provider routing in
// the Auth Broker.
func (s *Store) GetOAuthTokens(accountID string) (*OAuthTokens, error) {
	// Get metadata from database
	var provider string
	var expiresAt sql.NullTime
	var scopesJSON sql.NullString

	err := s.db.QueryRow(`
		SELECT provider, expires_at, scopes
		FROM oauth_tokens
		WHERE account_id = ?
		  AND client_config_id IN ('google-mail', 'microsoft-mail')
	`, accountID).Scan(&provider, &expiresAt, &scopesJSON)

	if err == sql.ErrNoRows {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query OAuth metadata: %w", err)
	}

	// Get sensitive tokens from keyring (or encrypted DB fallback)
	accessToken, err := s.getOAuthAccessToken(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	refreshToken, err := s.getOAuthRefreshToken(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get refresh token: %w", err)
	}

	// Parse scopes
	var scopes []string
	if scopesJSON.Valid && scopesJSON.String != "" {
		if err := json.Unmarshal([]byte(scopesJSON.String), &scopes); err != nil {
			s.log.Warn().Err(err).Msg("Failed to parse OAuth scopes, using empty list")
			scopes = []string{}
		}
	}

	tokens := &OAuthTokens{
		Provider:     provider,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scopes:       scopes,
	}

	if expiresAt.Valid {
		tokens.ExpiresAt = expiresAt.Time
	}

	return tokens, nil
}

// DeleteOAuthTokens removes all OAuth data for an account
func (s *Store) DeleteOAuthTokens(accountID string) error {
	// Delete from keyring
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, accountID+":access_token")
		_ = gokeyring.Delete(serviceName, accountID+":refresh_token")
	}

	// Clear encrypted fallback storage
	_, _ = s.db.Exec(`
		UPDATE accounts
		SET encrypted_access_token = NULL, encrypted_refresh_token = NULL
		WHERE id = ?
	`, accountID)

	// Delete metadata
	_, err := s.db.Exec("DELETE FROM oauth_tokens WHERE account_id = ?", accountID)
	if err != nil {
		return fmt.Errorf("failed to delete OAuth metadata: %w", err)
	}

	s.log.Debug().Str("account_id", accountID).Msg("OAuth tokens deleted")
	return nil
}

// UpdateOAuthAccessToken updates just the access token and expiry (after refresh)
// for the account's mail tokens. Per-extension slots have their own update path
// in UpdateOAuthAccessTokenForClientConfig (oauth_clientconfig.go).
func (s *Store) UpdateOAuthAccessToken(accountID, accessToken string, expiresAt time.Time) error {
	// Store new access token
	if err := s.setOAuthAccessToken(accountID, accessToken); err != nil {
		return fmt.Errorf("failed to store access token: %w", err)
	}

	// Update expiry in database — restrict to the mail row so we don't
	// clobber expiries for extension slots that share account_id.
	_, err := s.db.Exec(`
		UPDATE oauth_tokens
		SET expires_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE account_id = ?
		  AND client_config_id IN ('google-mail', 'microsoft-mail')
	`, expiresAt, accountID)

	if err != nil {
		return fmt.Errorf("failed to update OAuth expiry: %w", err)
	}

	s.log.Debug().
		Str("account_id", accountID).
		Time("expires_at", expiresAt).
		Msg("OAuth access token updated")

	return nil
}

// GetOAuthProvider returns the OAuth provider for an account, or empty string
// if not OAuth. Reads the mail row specifically — extension-slot rows carry
// per-slot provider names (e.g. "google-calendar") that would otherwise
// confuse provider-name comparisons in callers.
func (s *Store) GetOAuthProvider(accountID string) (string, error) {
	var provider string
	err := s.db.QueryRow(
		`SELECT provider FROM oauth_tokens
		 WHERE account_id = ?
		   AND client_config_id IN ('google-mail', 'microsoft-mail')`,
		accountID,
	).Scan(&provider)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to query OAuth provider: %w", err)
	}

	return provider, nil
}

// HasOAuthTokens returns true if the account has OAuth tokens stored
func (s *Store) HasOAuthTokens(accountID string) bool {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM oauth_tokens WHERE account_id = ?",
		accountID,
	).Scan(&count)

	return err == nil && count > 0
}

// setOAuthAccessToken stores the access token in keyring or encrypted DB
func (s *Store) setOAuthAccessToken(accountID, token string) error {
	if token == "" {
		return nil
	}

	// Try OS keyring first
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, accountID+":access_token", token)
		if err == nil {
			// Clear fallback storage
			_, _ = s.db.Exec("UPDATE accounts SET encrypted_access_token = NULL WHERE id = ?", accountID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store access token in keyring, using fallback")
	}

	// Fallback to encrypted database
	encrypted, err := s.encryptor.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt access token: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE accounts SET encrypted_access_token = ? WHERE id = ?",
		encrypted, accountID,
	)
	return err
}

// getOAuthAccessToken retrieves the access token from keyring or encrypted DB
func (s *Store) getOAuthAccessToken(accountID string) (string, error) {
	// Try OS keyring first
	if s.keyringEnabled {
		token, err := gokeyring.Get(serviceName, accountID+":access_token")
		if err == nil {
			return token, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading access token from keyring, trying fallback")
		}
	}

	// Try fallback encrypted database
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_access_token FROM accounts WHERE id = ?",
		accountID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows || !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query access token: %w", err)
	}

	return s.encryptor.Decrypt(encrypted.String)
}

// setOAuthRefreshToken stores the refresh token in keyring or encrypted DB
func (s *Store) setOAuthRefreshToken(accountID, token string) error {
	if token == "" {
		return nil
	}

	// Try OS keyring first
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, accountID+":refresh_token", token)
		if err == nil {
			// Clear fallback storage
			_, _ = s.db.Exec("UPDATE accounts SET encrypted_refresh_token = NULL WHERE id = ?", accountID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store refresh token in keyring, using fallback")
	}

	// Fallback to encrypted database
	encrypted, err := s.encryptor.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt refresh token: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE accounts SET encrypted_refresh_token = ? WHERE id = ?",
		encrypted, accountID,
	)
	return err
}

// getOAuthRefreshToken retrieves the refresh token from keyring or encrypted DB
func (s *Store) getOAuthRefreshToken(accountID string) (string, error) {
	// Try OS keyring first
	if s.keyringEnabled {
		token, err := gokeyring.Get(serviceName, accountID+":refresh_token")
		if err == nil {
			return token, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading refresh token from keyring, trying fallback")
		}
	}

	// Try fallback encrypted database
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_refresh_token FROM accounts WHERE id = ?",
		accountID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows || !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query refresh token: %w", err)
	}

	return s.encryptor.Decrypt(encrypted.String)
}

// =============================================================================
// Contact Source OAuth Methods (for standalone Google/Microsoft contact sources)
// =============================================================================

// SetContactSourceOAuthTokens stores OAuth tokens for a standalone contact source
// Sensitive tokens go to keyring (with DB fallback), metadata goes to contact_source_oauth table
func (s *Store) SetContactSourceOAuthTokens(sourceID string, tokens *OAuthTokens) error {
	if tokens == nil {
		return fmt.Errorf("tokens cannot be nil")
	}

	// Store sensitive tokens in keyring (or encrypted DB fallback)
	if err := s.setContactSourceAccessToken(sourceID, tokens.AccessToken); err != nil {
		return fmt.Errorf("failed to store access token: %w", err)
	}

	if err := s.setContactSourceRefreshToken(sourceID, tokens.RefreshToken); err != nil {
		return fmt.Errorf("failed to store refresh token: %w", err)
	}

	// Store metadata in contact_source_oauth table
	scopesJSON, err := json.Marshal(tokens.Scopes)
	if err != nil {
		return fmt.Errorf("failed to marshal scopes: %w", err)
	}

	// Derive client_config_id from provider. Contact sources keep source_id as
	// their PK (a source has at most one set of tokens), so this column is
	// informational/routing-only — used by the Auth Broker when an extension
	// later wants to discover which OAuth client backs this source.
	clientConfigID := oauth2.ClientConfigIDForProvider(tokens.Provider)
	if clientConfigID == "" {
		return fmt.Errorf("cannot derive client_config_id for provider %q", tokens.Provider)
	}

	_, err = s.db.Exec(`
		INSERT INTO contact_source_oauth (source_id, client_config_id, provider, expires_at, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id) DO UPDATE SET
			client_config_id = excluded.client_config_id,
			provider = excluded.provider,
			expires_at = excluded.expires_at,
			scopes = excluded.scopes,
			updated_at = CURRENT_TIMESTAMP
	`, sourceID, clientConfigID, tokens.Provider, tokens.ExpiresAt, string(scopesJSON))

	if err != nil {
		return fmt.Errorf("failed to store contact source OAuth metadata: %w", err)
	}

	s.log.Debug().
		Str("source_id", sourceID).
		Str("provider", tokens.Provider).
		Time("expires_at", tokens.ExpiresAt).
		Msg("Contact source OAuth tokens stored")

	return nil
}

// GetContactSourceOAuthTokens retrieves OAuth tokens for a standalone contact source
func (s *Store) GetContactSourceOAuthTokens(sourceID string) (*OAuthTokens, error) {
	// Get metadata from contact_source_oauth table
	var provider string
	var expiresAt sql.NullTime
	var scopesJSON sql.NullString

	err := s.db.QueryRow(`
		SELECT provider, expires_at, scopes
		FROM contact_source_oauth
		WHERE source_id = ?
	`, sourceID).Scan(&provider, &expiresAt, &scopesJSON)

	if err == sql.ErrNoRows {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query contact source OAuth metadata: %w", err)
	}

	// Get sensitive tokens from keyring (or encrypted DB fallback)
	accessToken, err := s.getContactSourceAccessToken(sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	refreshToken, err := s.getContactSourceRefreshToken(sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get refresh token: %w", err)
	}

	// Parse scopes
	var scopes []string
	if scopesJSON.Valid && scopesJSON.String != "" {
		if err := json.Unmarshal([]byte(scopesJSON.String), &scopes); err != nil {
			s.log.Warn().Err(err).Msg("Failed to parse contact source OAuth scopes, using empty list")
			scopes = []string{}
		}
	}

	tokens := &OAuthTokens{
		Provider:     provider,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scopes:       scopes,
	}

	if expiresAt.Valid {
		tokens.ExpiresAt = expiresAt.Time
	}

	return tokens, nil
}

// DeleteContactSourceOAuthTokens removes all OAuth data for a standalone contact source
func (s *Store) DeleteContactSourceOAuthTokens(sourceID string) error {
	// Delete from keyring
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, "contact_source:"+sourceID+":access_token")
		_ = gokeyring.Delete(serviceName, "contact_source:"+sourceID+":refresh_token")
	}

	// Clear encrypted fallback storage
	_, _ = s.db.Exec(`
		UPDATE contact_sources
		SET encrypted_access_token = NULL, encrypted_refresh_token = NULL
		WHERE id = ?
	`, sourceID)

	// Delete metadata
	_, err := s.db.Exec("DELETE FROM contact_source_oauth WHERE source_id = ?", sourceID)
	if err != nil {
		return fmt.Errorf("failed to delete contact source OAuth metadata: %w", err)
	}

	s.log.Debug().Str("source_id", sourceID).Msg("Contact source OAuth tokens deleted")
	return nil
}

// UpdateContactSourceOAuthAccessToken updates just the access token and expiry (after refresh)
func (s *Store) UpdateContactSourceOAuthAccessToken(sourceID, accessToken string, expiresAt time.Time) error {
	// Store new access token
	if err := s.setContactSourceAccessToken(sourceID, accessToken); err != nil {
		return fmt.Errorf("failed to store access token: %w", err)
	}

	// Update expiry in database
	_, err := s.db.Exec(`
		UPDATE contact_source_oauth
		SET expires_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE source_id = ?
	`, expiresAt, sourceID)

	if err != nil {
		return fmt.Errorf("failed to update contact source OAuth expiry: %w", err)
	}

	s.log.Debug().
		Str("source_id", sourceID).
		Time("expires_at", expiresAt).
		Msg("Contact source OAuth access token updated")

	return nil
}

// HasContactSourceOAuthTokens returns true if the contact source has OAuth tokens stored
func (s *Store) HasContactSourceOAuthTokens(sourceID string) bool {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM contact_source_oauth WHERE source_id = ?",
		sourceID,
	).Scan(&count)

	return err == nil && count > 0
}

// setContactSourceAccessToken stores the access token in keyring or encrypted DB
func (s *Store) setContactSourceAccessToken(sourceID, token string) error {
	if token == "" {
		return nil
	}

	// Try OS keyring first
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, "contact_source:"+sourceID+":access_token", token)
		if err == nil {
			// Clear fallback storage
			_, _ = s.db.Exec("UPDATE contact_sources SET encrypted_access_token = NULL WHERE id = ?", sourceID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store contact source access token in keyring, using fallback")
	}

	// Fallback to encrypted database
	encrypted, err := s.encryptor.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt access token: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE contact_sources SET encrypted_access_token = ? WHERE id = ?",
		encrypted, sourceID,
	)
	return err
}

// getContactSourceAccessToken retrieves the access token from keyring or encrypted DB
func (s *Store) getContactSourceAccessToken(sourceID string) (string, error) {
	// Try OS keyring first
	if s.keyringEnabled {
		token, err := gokeyring.Get(serviceName, "contact_source:"+sourceID+":access_token")
		if err == nil {
			return token, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading contact source access token from keyring, trying fallback")
		}
	}

	// Try fallback encrypted database
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_access_token FROM contact_sources WHERE id = ?",
		sourceID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows || !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query contact source access token: %w", err)
	}

	return s.encryptor.Decrypt(encrypted.String)
}

// setContactSourceRefreshToken stores the refresh token in keyring or encrypted DB
func (s *Store) setContactSourceRefreshToken(sourceID, token string) error {
	if token == "" {
		return nil
	}

	// Try OS keyring first
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, "contact_source:"+sourceID+":refresh_token", token)
		if err == nil {
			// Clear fallback storage
			_, _ = s.db.Exec("UPDATE contact_sources SET encrypted_refresh_token = NULL WHERE id = ?", sourceID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store contact source refresh token in keyring, using fallback")
	}

	// Fallback to encrypted database
	encrypted, err := s.encryptor.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt refresh token: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE contact_sources SET encrypted_refresh_token = ? WHERE id = ?",
		encrypted, sourceID,
	)
	return err
}

// getContactSourceRefreshToken retrieves the refresh token from keyring or encrypted DB
func (s *Store) getContactSourceRefreshToken(sourceID string) (string, error) {
	// Try OS keyring first
	if s.keyringEnabled {
		token, err := gokeyring.Get(serviceName, "contact_source:"+sourceID+":refresh_token")
		if err == nil {
			return token, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading contact source refresh token from keyring, trying fallback")
		}
	}

	// Try fallback encrypted database
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_refresh_token FROM contact_sources WHERE id = ?",
		sourceID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows || !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query contact source refresh token: %w", err)
	}

	return s.encryptor.Decrypt(encrypted.String)
}
