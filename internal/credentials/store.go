// Package credentials provides secure credential storage with fallback support
package credentials

import (
	"database/sql"
	"fmt"

	"github.com/hkdb/aerion/internal/crypto"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
	gokeyring "github.com/zalando/go-keyring"
)

const serviceName = "aerion"

// Store provides credential storage with OS keyring and encrypted DB fallback
type Store struct {
	db             *sql.DB
	encryptor      *crypto.Encryptor
	keyringEnabled bool
	log            zerolog.Logger
}

// NewStore creates a new credential store
// It tries to use the OS keyring, falling back to encrypted database storage
func NewStore(db *sql.DB, dataDir string) (*Store, error) {
	log := logging.WithComponent("credentials")

	// Create encryptor for fallback storage
	encryptor, err := crypto.NewEncryptor(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Test if keyring is available
	keyringEnabled := testKeyring()
	if keyringEnabled {
		log.Info().Msg("OS keyring available, using as primary credential storage")
	} else {
		log.Warn().Msg("OS keyring not available, using encrypted database storage")
	}

	return &Store{
		db:             db,
		encryptor:      encryptor,
		keyringEnabled: keyringEnabled,
		log:            log,
	}, nil
}

// testKeyring checks if the OS keyring is available and functional
func testKeyring() bool {
	testKey := "aerion-test-keyring-check"
	testValue := "test"

	// Try to set a test value
	err := gokeyring.Set(serviceName, testKey, testValue)
	if err != nil {
		return false
	}

	// Clean up test value
	_ = gokeyring.Delete(serviceName, testKey)

	return true
}

// SetPassword stores a password for an account
func (s *Store) SetPassword(accountID, password string) error {
	if password == "" {
		return nil
	}

	// Try OS keyring first if available
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, accountID, password)
		if err == nil {
			s.log.Debug().Str("account_id", accountID).Msg("Password stored in OS keyring")
			// Clear any fallback storage
			s.clearDBPassword(accountID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store in OS keyring, using fallback")
	}

	// Fallback to encrypted database storage
	encrypted, err := s.encryptor.Encrypt(password)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE accounts SET encrypted_password = ? WHERE id = ?",
		encrypted, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to store encrypted password: %w", err)
	}

	s.log.Debug().Str("account_id", accountID).Msg("Password stored in encrypted database")
	return nil
}

// GetPassword retrieves a password for an account
func (s *Store) GetPassword(accountID string) (string, error) {
	// Try OS keyring first if available
	if s.keyringEnabled {
		password, err := gokeyring.Get(serviceName, accountID)
		if err == nil {
			return password, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading from OS keyring, trying fallback")
		}
	}

	// Try fallback encrypted database storage
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_password FROM accounts WHERE id = ?",
		accountID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query password: %w", err)
	}

	if !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}

	// Decrypt
	password, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt password: %w", err)
	}

	return password, nil
}

// DeletePassword removes a password for an account
func (s *Store) DeletePassword(accountID string) error {
	// Delete from OS keyring
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, accountID)
	}

	// Delete from database
	s.clearDBPassword(accountID)

	return nil
}

// clearDBPassword clears the encrypted password from the database
func (s *Store) clearDBPassword(accountID string) {
	_, _ = s.db.Exec("UPDATE accounts SET encrypted_password = NULL WHERE id = ?", accountID)
}

// DeleteAllCredentials removes all credentials for an account
func (s *Store) DeleteAllCredentials(accountID string) error {
	_ = s.DeletePassword(accountID)
	_ = s.DeleteSMTPPassword(accountID)
	_ = s.DeleteOAuthTokens(accountID)
	return nil
}

// smtpPasswordKeyringKey returns the keyring slot used for the
// SMTP-specific password (only relevant when Account.SMTPUsername is set).
// Keeps the IMAP credential under accountID alone, so legacy entries are
// untouched by this addition.
func smtpPasswordKeyringKey(accountID string) string {
	return accountID + ":smtp"
}

// SetSMTPPassword stores the SMTP-specific password for an account. Used
// only when the account has a non-empty SMTPUsername (separate creds).
// Empty input is a no-op so the UI can submit a blank field on Update to
// mean "keep what's already there."
func (s *Store) SetSMTPPassword(accountID, password string) error {
	if password == "" {
		return nil
	}

	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, smtpPasswordKeyringKey(accountID), password)
		if err == nil {
			s.log.Debug().Str("account_id", accountID).Msg("SMTP password stored in OS keyring")
			s.clearDBSMTPPassword(accountID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store SMTP password in OS keyring, using fallback")
	}

	encrypted, err := s.encryptor.Encrypt(password)
	if err != nil {
		return fmt.Errorf("failed to encrypt SMTP password: %w", err)
	}
	if _, err := s.db.Exec(
		"UPDATE accounts SET encrypted_smtp_password = ? WHERE id = ?",
		encrypted, accountID,
	); err != nil {
		return fmt.Errorf("failed to store encrypted SMTP password: %w", err)
	}
	s.log.Debug().Str("account_id", accountID).Msg("SMTP password stored in encrypted database")
	return nil
}

// GetSMTPPassword retrieves the SMTP-specific password for an account.
// Mirrors GetPassword's keyring-first + DB-fallback shape, but uses the
// separate "<accountID>:smtp" keyring slot and the
// encrypted_smtp_password column.
func (s *Store) GetSMTPPassword(accountID string) (string, error) {
	if s.keyringEnabled {
		password, err := gokeyring.Get(serviceName, smtpPasswordKeyringKey(accountID))
		if err == nil {
			return password, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading SMTP password from OS keyring, trying fallback")
		}
	}

	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_smtp_password FROM accounts WHERE id = ?",
		accountID,
	).Scan(&encrypted)
	if err == sql.ErrNoRows {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query SMTP password: %w", err)
	}
	if !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}
	password, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt SMTP password: %w", err)
	}
	return password, nil
}

// DeleteSMTPPassword removes the SMTP-specific password for an account.
// Idempotent.
func (s *Store) DeleteSMTPPassword(accountID string) error {
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, smtpPasswordKeyringKey(accountID))
	}
	s.clearDBSMTPPassword(accountID)
	return nil
}

// clearDBSMTPPassword clears the encrypted SMTP password from the database.
func (s *Store) clearDBSMTPPassword(accountID string) {
	_, _ = s.db.Exec("UPDATE accounts SET encrypted_smtp_password = NULL WHERE id = ?", accountID)
}

// IsKeyringEnabled returns whether the OS keyring is being used
func (s *Store) IsKeyringEnabled() bool {
	return s.keyringEnabled
}

// SetSMIMEPrivateKey stores an S/MIME private key for a certificate
func (s *Store) SetSMIMEPrivateKey(certID string, privateKeyPEM []byte) error {
	if len(privateKeyPEM) == 0 {
		return nil
	}

	keyringKey := "smime:" + certID + ":private_key"

	// Try OS keyring first if available
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, keyringKey, string(privateKeyPEM))
		if err == nil {
			s.log.Debug().Str("cert_id", certID).Msg("S/MIME private key stored in OS keyring")
			s.clearSMIMEDBPrivateKey(certID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store S/MIME key in OS keyring, using fallback")
	}

	// Fallback to encrypted database storage
	encrypted, err := s.encryptor.Encrypt(string(privateKeyPEM))
	if err != nil {
		return fmt.Errorf("failed to encrypt S/MIME private key: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE smime_certificates SET encrypted_private_key = ? WHERE id = ?",
		encrypted, certID,
	)
	if err != nil {
		return fmt.Errorf("failed to store encrypted S/MIME private key: %w", err)
	}

	s.log.Debug().Str("cert_id", certID).Msg("S/MIME private key stored in encrypted database")
	return nil
}

// GetSMIMEPrivateKey retrieves an S/MIME private key for a certificate
func (s *Store) GetSMIMEPrivateKey(certID string) ([]byte, error) {
	keyringKey := "smime:" + certID + ":private_key"

	// Try OS keyring first if available
	if s.keyringEnabled {
		key, err := gokeyring.Get(serviceName, keyringKey)
		if err == nil {
			return []byte(key), nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading S/MIME key from OS keyring, trying fallback")
		}
	}

	// Try fallback encrypted database storage
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_private_key FROM smime_certificates WHERE id = ?",
		certID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query S/MIME private key: %w", err)
	}

	if !encrypted.Valid || encrypted.String == "" {
		return nil, ErrCredentialNotFound
	}

	key, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt S/MIME private key: %w", err)
	}

	return []byte(key), nil
}

// DeleteSMIMEPrivateKey removes an S/MIME private key for a certificate
func (s *Store) DeleteSMIMEPrivateKey(certID string) error {
	keyringKey := "smime:" + certID + ":private_key"

	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, keyringKey)
	}

	s.clearSMIMEDBPrivateKey(certID)
	return nil
}

// clearSMIMEDBPrivateKey clears the encrypted private key from the database
func (s *Store) clearSMIMEDBPrivateKey(certID string) {
	_, _ = s.db.Exec("UPDATE smime_certificates SET encrypted_private_key = NULL WHERE id = ?", certID)
}

// SetPGPPrivateKey stores a PGP private key for a keypair
func (s *Store) SetPGPPrivateKey(keyID string, armoredKey []byte) error {
	if len(armoredKey) == 0 {
		return nil
	}

	keyringKey := "pgp:" + keyID + ":private_key"

	// Try OS keyring first if available
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, keyringKey, string(armoredKey))
		if err == nil {
			s.log.Debug().Str("key_id", keyID).Msg("PGP private key stored in OS keyring")
			s.clearPGPDBPrivateKey(keyID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store PGP key in OS keyring, using fallback")
	}

	// Fallback to encrypted database storage
	encrypted, err := s.encryptor.Encrypt(string(armoredKey))
	if err != nil {
		return fmt.Errorf("failed to encrypt PGP private key: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE pgp_keys SET encrypted_private_key = ? WHERE id = ?",
		encrypted, keyID,
	)
	if err != nil {
		return fmt.Errorf("failed to store encrypted PGP private key: %w", err)
	}

	s.log.Debug().Str("key_id", keyID).Msg("PGP private key stored in encrypted database")
	return nil
}

// GetPGPPrivateKey retrieves a PGP private key for a keypair
func (s *Store) GetPGPPrivateKey(keyID string) ([]byte, error) {
	keyringKey := "pgp:" + keyID + ":private_key"

	// Try OS keyring first if available
	if s.keyringEnabled {
		key, err := gokeyring.Get(serviceName, keyringKey)
		if err == nil {
			return []byte(key), nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading PGP key from OS keyring, trying fallback")
		}
	}

	// Try fallback encrypted database storage
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_private_key FROM pgp_keys WHERE id = ?",
		keyID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query PGP private key: %w", err)
	}

	if !encrypted.Valid || encrypted.String == "" {
		return nil, ErrCredentialNotFound
	}

	key, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt PGP private key: %w", err)
	}

	return []byte(key), nil
}

// DeletePGPPrivateKey removes a PGP private key for a keypair
func (s *Store) DeletePGPPrivateKey(keyID string) error {
	keyringKey := "pgp:" + keyID + ":private_key"

	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, keyringKey)
	}

	s.clearPGPDBPrivateKey(keyID)
	return nil
}

// clearPGPDBPrivateKey clears the encrypted private key from the database
func (s *Store) clearPGPDBPrivateKey(keyID string) {
	_, _ = s.db.Exec("UPDATE pgp_keys SET encrypted_private_key = NULL WHERE id = ?", keyID)
}

// --- Extension-secret storage (host-internal helpers used by app/coreimpl.go
// to back the coreapi.Storage.Secrets surface that extensions consume) ----
//
// Extensions never import this package — they call core.Storage().Secrets(...)
// which delegates to these methods inside the host. The four methods are
// extension-generic: any extension that opts into the convenience tier via
// coreapi.Secrets ends up here.
//
// Storage model: keyring is primary (key shape `ext:<extension>:<key>`).
// When the keyring is unavailable OR rejects the write, the value is
// encrypted with the store's AES encryptor and persisted to the
// `extension_secrets` core table.
//
// The `extension_secrets` table tracks ALL extension secret keys regardless
// of where their value actually lives, so DeleteAll can enumerate the
// keyring entries it needs to remove. Storage location is encoded in the
// `encrypted_value` column: empty string → "lives in keyring", non-empty →
// "AES ciphertext (base64) right here".

// trySetExtensionSecretInKeyring is an internal helper that attempts a
// keyring write. Returns true on success, false on disabled/failed (logs a
// warning on real failures).
func (s *Store) trySetExtensionSecretInKeyring(extension, key, value string) bool {
	if !s.keyringEnabled {
		return false
	}
	keyringKey := "ext:" + extension + ":" + key
	if err := gokeyring.Set(serviceName, keyringKey, value); err != nil {
		s.log.Warn().Err(err).Str("extension", extension).Str("key", key).
			Msg("Failed to store extension secret in OS keyring, falling back to encrypted DB")
		return false
	}
	return true
}

// SetExtensionSecret stores a per-extension secret. Keyring is tried first;
// on keyring failure the value is encrypted and stored in extension_secrets.
// Either way the table gets a row tracking the (extension, key) pair so
// DeleteAll can find it later. An empty `value` is treated as Delete.
func (s *Store) SetExtensionSecret(extension, key, value string) error {
	if extension == "" || key == "" {
		return fmt.Errorf("credentials: extension and key required")
	}
	if value == "" {
		return s.DeleteExtensionSecret(extension, key)
	}

	storedInKeyring := s.trySetExtensionSecretInKeyring(extension, key, value)

	var encryptedValue string
	if !storedInKeyring {
		ct, err := s.encryptor.Encrypt(value)
		if err != nil {
			return fmt.Errorf("encrypt extension secret: %w", err)
		}
		encryptedValue = ct
	}

	_, err := s.db.Exec(`
		INSERT INTO extension_secrets (extension, key, encrypted_value, created_at)
		VALUES (?, ?, ?, strftime('%s', 'now'))
		ON CONFLICT(extension, key) DO UPDATE SET
		    encrypted_value = excluded.encrypted_value,
		    created_at = excluded.created_at`,
		extension, key, encryptedValue,
	)
	if err != nil {
		// Roll the keyring entry back if we wrote one, so on-disk state stays
		// consistent.
		if storedInKeyring {
			_ = gokeyring.Delete(serviceName, "ext:"+extension+":"+key)
		}
		return fmt.Errorf("persist extension secret: %w", err)
	}
	return nil
}

// GetExtensionSecret retrieves a per-extension secret. Returns ("", nil) when
// no entry exists — callers distinguish "not found" from errors by checking
// the returned string.
func (s *Store) GetExtensionSecret(extension, key string) (string, error) {
	if extension == "" || key == "" {
		return "", fmt.Errorf("credentials: extension and key required")
	}

	var encryptedValue string
	err := s.db.QueryRow(
		`SELECT encrypted_value FROM extension_secrets WHERE extension = ? AND key = ?`,
		extension, key,
	).Scan(&encryptedValue)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read extension secret: %w", err)
	}

	// Non-empty ciphertext column → value lives in the table.
	if encryptedValue != "" {
		plaintext, derr := s.encryptor.Decrypt(encryptedValue)
		if derr != nil {
			return "", fmt.Errorf("decrypt extension secret: %w", derr)
		}
		return plaintext, nil
	}

	// Empty ciphertext → value is in the keyring.
	if !s.keyringEnabled {
		// Row says "keyring" but keyring is no longer available. Treat as
		// missing; the caller will prompt the user to re-enter.
		return "", nil
	}
	value, kerr := gokeyring.Get(serviceName, "ext:"+extension+":"+key)
	if kerr == nil {
		return value, nil
	}
	if kerr == gokeyring.ErrNotFound {
		return "", nil
	}
	return "", fmt.Errorf("read extension secret from keyring: %w", kerr)
}

// DeleteExtensionSecret removes a per-extension secret. Idempotent —
// deleting a non-existent secret is not an error. Clears both the keyring
// entry (if applicable) and the table row.
func (s *Store) DeleteExtensionSecret(extension, key string) error {
	if extension == "" || key == "" {
		return fmt.Errorf("credentials: extension and key required")
	}
	if s.keyringEnabled {
		err := gokeyring.Delete(serviceName, "ext:"+extension+":"+key)
		if err != nil && err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Str("extension", extension).Str("key", key).
				Msg("Failed to delete extension secret from keyring")
		}
	}
	_, err := s.db.Exec(
		`DELETE FROM extension_secrets WHERE extension = ? AND key = ?`,
		extension, key,
	)
	if err != nil {
		return fmt.Errorf("delete extension secret row: %w", err)
	}
	return nil
}

// DeleteAllExtensionSecrets removes every secret stored under the given
// extension. Used by extension-uninstall flows. Best-effort on keyring
// errors — the table is always cleared.
func (s *Store) DeleteAllExtensionSecrets(extension string) error {
	if extension == "" {
		return fmt.Errorf("credentials: extension required")
	}
	rows, err := s.db.Query(
		`SELECT key FROM extension_secrets WHERE extension = ?`,
		extension,
	)
	if err != nil {
		return fmt.Errorf("list extension secret keys: %w", err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return fmt.Errorf("scan extension secret key: %w", err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate extension secret keys: %w", err)
	}

	if s.keyringEnabled {
		for _, k := range keys {
			derr := gokeyring.Delete(serviceName, "ext:"+extension+":"+k)
			if derr != nil && derr != gokeyring.ErrNotFound {
				s.log.Warn().Err(derr).Str("extension", extension).Str("key", k).
					Msg("Failed to delete extension secret from keyring")
			}
		}
	}
	_, err = s.db.Exec(`DELETE FROM extension_secrets WHERE extension = ?`, extension)
	if err != nil {
		return fmt.Errorf("delete extension secret rows: %w", err)
	}
	return nil
}

// SetCardDAVPassword stores a password for a CardDAV contact source
func (s *Store) SetCardDAVPassword(sourceID, password string) error {
	if password == "" {
		return nil
	}

	// Try OS keyring first if available
	if s.keyringEnabled {
		err := gokeyring.Set(serviceName, "carddav:"+sourceID, password)
		if err == nil {
			s.log.Debug().Str("source_id", sourceID).Msg("CardDAV password stored in OS keyring")
			// Clear any fallback storage
			s.clearCardDAVDBPassword(sourceID)
			return nil
		}
		s.log.Warn().Err(err).Msg("Failed to store CardDAV password in OS keyring, using fallback")
	}

	// Fallback to encrypted database storage
	encrypted, err := s.encryptor.Encrypt(password)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	_, err = s.db.Exec(
		"UPDATE contact_sources SET encrypted_password = ? WHERE id = ?",
		encrypted, sourceID,
	)
	if err != nil {
		return fmt.Errorf("failed to store encrypted password: %w", err)
	}

	s.log.Debug().Str("source_id", sourceID).Msg("CardDAV password stored in encrypted database")
	return nil
}

// GetCardDAVPassword retrieves a password for a CardDAV contact source
func (s *Store) GetCardDAVPassword(sourceID string) (string, error) {
	// Try OS keyring first if available
	if s.keyringEnabled {
		password, err := gokeyring.Get(serviceName, "carddav:"+sourceID)
		if err == nil {
			return password, nil
		}
		if err != gokeyring.ErrNotFound {
			s.log.Warn().Err(err).Msg("Error reading CardDAV password from OS keyring, trying fallback")
		}
	}

	// Try fallback encrypted database storage
	var encrypted sql.NullString
	err := s.db.QueryRow(
		"SELECT encrypted_password FROM contact_sources WHERE id = ?",
		sourceID,
	).Scan(&encrypted)

	if err == sql.ErrNoRows {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to query password: %w", err)
	}

	if !encrypted.Valid || encrypted.String == "" {
		return "", ErrCredentialNotFound
	}

	// Decrypt
	password, err := s.encryptor.Decrypt(encrypted.String)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt password: %w", err)
	}

	return password, nil
}

// DeleteCardDAVPassword removes a password for a CardDAV contact source
func (s *Store) DeleteCardDAVPassword(sourceID string) error {
	// Delete from OS keyring
	if s.keyringEnabled {
		_ = gokeyring.Delete(serviceName, "carddav:"+sourceID)
	}

	// Delete from database
	s.clearCardDAVDBPassword(sourceID)

	return nil
}

// clearCardDAVDBPassword clears the encrypted password from the contact_sources table
func (s *Store) clearCardDAVDBPassword(sourceID string) {
	_, _ = s.db.Exec("UPDATE contact_sources SET encrypted_password = NULL WHERE id = ?", sourceID)
}
