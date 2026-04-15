package app

import (
	"errors"
	"fmt"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/certificate"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
)

// ============================================================================
// Account API - Exposed to frontend via Wails bindings
// ============================================================================

// GetAccounts returns all configured accounts
func (a *App) GetAccounts() ([]*account.Account, error) {
	return a.accountStore.List()
}

// GetAccount returns a single account by ID
func (a *App) GetAccount(id string) (*account.Account, error) {
	return a.accountStore.Get(id)
}

// AddAccount creates a new email account
func (a *App) AddAccount(config account.AccountConfig) (*account.Account, error) {
	log := logging.WithComponent("app")

	// Create account in database
	acc, err := a.accountStore.Create(&config)
	if err != nil {
		log.Error().Err(err).Str("email", config.Email).Msg("Failed to create account")
		return nil, err
	}

	// Store password in credential store
	if config.Password != "" {
		if err := a.credStore.SetPassword(acc.ID, config.Password); err != nil {
			log.Error().Err(err).Str("account_id", acc.ID).Msg("Failed to store password")
			// Delete the account since we can't store credentials
			a.accountStore.Delete(acc.ID)
			return nil, fmt.Errorf("failed to store password: %w", err)
		}
	}

	// Scale database connection pool for new account
	a.updateDBConnectionPool()

	// Start IDLE for the new account
	if a.idleManager != nil && acc.Enabled {
		a.idleManager.StartAccount(acc.ID, acc.Name)
	}

	log.Info().Str("account_id", acc.ID).Str("email", acc.Email).Msg("Account created")
	return acc, nil
}

// AddMicrosoftSharedMailbox creates a shared mailbox account linked to a primary Microsoft OAuth account.
// The shared mailbox reuses the primary account's OAuth tokens but authenticates IMAP/SMTP
// with the shared mailbox email as the XOAUTH2 username.
func (a *App) AddMicrosoftSharedMailbox(primaryAccountID, sharedEmail, displayName string) (*account.Account, error) {
	log := logging.WithComponent("app")

	if sharedEmail == "" {
		return nil, fmt.Errorf("shared mailbox email is required")
	}
	if displayName == "" {
		displayName = sharedEmail
	}

	primary, err := a.accountStore.Get(primaryAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get primary account: %w", err)
	}
	if primary == nil {
		return nil, fmt.Errorf("primary account not found")
	}
	if primary.AuthType != account.AuthOAuth2 {
		return nil, fmt.Errorf("shared mailboxes require an OAuth account")
	}

	// Get the primary account's OAuth tokens
	tokens, err := a.credStore.GetOAuthTokens(primaryAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth tokens from primary account: %w", err)
	}

	// Validate IMAP access with shared mailbox email as XOAUTH2 username
	clientConfig := imap.DefaultConfig()
	clientConfig.Host = primary.IMAPHost
	clientConfig.Port = primary.IMAPPort
	clientConfig.Security = imap.SecurityType(primary.IMAPSecurity)
	clientConfig.Username = sharedEmail
	clientConfig.AuthType = imap.AuthTypeOAuth2
	clientConfig.AccessToken = tokens.AccessToken

	clientConfig.TLSConfig = certificate.BuildTLSConfig(primary.IMAPHost, a.certStore)

	client := imap.NewClient(clientConfig)
	if connectErr := client.Connect(); connectErr != nil {
		return nil, fmt.Errorf("failed to connect to IMAP: %w", connectErr)
	}
	if loginErr := client.Login(); loginErr != nil {
		client.Close()
		return nil, fmt.Errorf("shared mailbox authentication failed — verify you have access to %s: %w", sharedEmail, loginErr)
	}
	client.Close()

	// Create the shared mailbox account
	config := account.AccountConfig{
		Name:                  displayName,
		DisplayName:           displayName,
		Email:                 sharedEmail,
		SharedMailboxParentID: primaryAccountID,
		IMAPHost:              primary.IMAPHost,
		IMAPPort:              primary.IMAPPort,
		IMAPSecurity:          primary.IMAPSecurity,
		SMTPHost:              primary.SMTPHost,
		SMTPPort:              primary.SMTPPort,
		SMTPSecurity:          primary.SMTPSecurity,
		AuthType:              account.AuthOAuth2,
		Username:              sharedEmail,
		Color:                 primary.Color,
		SyncPeriodDays:        primary.SyncPeriodDays,
		SyncInterval:          primary.SyncInterval,
	}

	acc, err := a.accountStore.Create(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create shared mailbox account: %w", err)
	}

	// Copy OAuth tokens to the new account
	if tokenErr := a.credStore.SetOAuthTokens(acc.ID, tokens); tokenErr != nil {
		// Clean up the account if token storage fails
		a.accountStore.Delete(acc.ID)
		return nil, fmt.Errorf("failed to store OAuth tokens for shared mailbox: %w", tokenErr)
	}

	a.updateDBConnectionPool()

	if a.idleManager != nil && acc.Enabled {
		a.idleManager.StartAccount(acc.ID, acc.Name)
	}

	log.Info().
		Str("accountID", acc.ID).
		Str("sharedEmail", sharedEmail).
		Str("parentID", primaryAccountID).
		Msg("Microsoft shared mailbox created")

	return acc, nil
}

// GetMicrosoftSharedMailboxes returns all shared mailbox accounts linked to a parent account.
func (a *App) GetMicrosoftSharedMailboxes(parentAccountID string) ([]*account.Account, error) {
	return a.accountStore.ListBySharedMailboxParent(parentAccountID)
}

// UpdateAccount updates an existing account
func (a *App) UpdateAccount(id string, config account.AccountConfig) (*account.Account, error) {
	log := logging.WithComponent("app")

	// Get existing account to check for sync period changes
	existingAcc, err := a.accountStore.Get(id)
	if err != nil {
		log.Error().Err(err).Str("account_id", id).Msg("Failed to get existing account")
		return nil, fmt.Errorf("failed to get existing account: %w", err)
	}
	if existingAcc == nil {
		return nil, fmt.Errorf("account not found: %s", id)
	}

	// Validate folder mappings if any are set
	folderPaths := map[string]string{
		"sent":    config.SentFolderPath,
		"drafts":  config.DraftsFolderPath,
		"trash":   config.TrashFolderPath,
		"spam":    config.SpamFolderPath,
		"archive": config.ArchiveFolderPath,
		"all":     config.AllMailFolderPath,
		"starred": config.StarredFolderPath,
	}

	for folderType, path := range folderPaths {
		if path != "" {
			f, err := a.folderStore.GetByPath(id, path)
			if err != nil {
				return nil, fmt.Errorf("error checking %s folder: %w", folderType, err)
			}
			if f == nil {
				return nil, fmt.Errorf("%s folder not found: %s", folderType, path)
			}
		}
	}

	// Check if sync period changed
	syncPeriodChanged := existingAcc.SyncPeriodDays != config.SyncPeriodDays

	acc, err := a.accountStore.Update(id, &config)
	if err != nil {
		log.Error().Err(err).Str("account_id", id).Msg("Failed to update account")
		return nil, err
	}

	// Update password in credential store if provided
	if config.Password != "" {
		if err := a.credStore.SetPassword(id, config.Password); err != nil {
			log.Error().Err(err).Str("account_id", id).Msg("Failed to update password")
			return nil, fmt.Errorf("failed to update password: %w", err)
		}
	}

	// If sync period changed, cancel any running sync and trigger a new one
	if syncPeriodChanged && a.syncScheduler != nil {
		log.Info().
			Str("account_id", id).
			Int("old_sync_period", existingAcc.SyncPeriodDays).
			Int("new_sync_period", config.SyncPeriodDays).
			Msg("Sync period changed, cancelling current sync and triggering new sync")

		a.syncScheduler.CancelSync(id)
		// Small delay to allow cancellation to complete
		go func() {
			defer recoverPanic("app", "sync after account update")
			// time.Sleep(500 * time.Millisecond)
			a.syncScheduler.TriggerSync(id)
		}()
	}

	log.Info().Str("account_id", id).Msg("Account updated")
	return acc, nil
}

// RemoveAccount deletes an account and all its data
func (a *App) RemoveAccount(id string) error {
	log := logging.WithComponent("app")

	// Cascade: delete any shared mailboxes linked to this account
	sharedMailboxes, _ := a.accountStore.ListBySharedMailboxParent(id)
	for _, sm := range sharedMailboxes {
		log.Info().Str("sharedID", sm.ID).Str("parentID", id).Msg("Cascade deleting shared mailbox")
		a.RemoveAccount(sm.ID)
	}

	// Stop IDLE for this account
	if a.idleManager != nil {
		a.idleManager.StopAccount(id)
	}

	// Close any IMAP connections for this account
	a.imapPool.CloseAccount(id)

	// Delete from database (cascades to folders, messages, etc.)
	if err := a.accountStore.Delete(id); err != nil {
		log.Error().Err(err).Str("account_id", id).Msg("Failed to delete account")
		return err
	}

	// Delete credentials from credential store
	if err := a.credStore.DeleteAllCredentials(id); err != nil {
		log.Warn().Err(err).Str("account_id", id).Msg("Failed to delete credentials")
	}

	// Scale database connection pool after removing account
	a.updateDBConnectionPool()

	log.Info().Str("account_id", id).Msg("Account removed")
	return nil
}

// SetAccountEnabled enables or disables an account
func (a *App) SetAccountEnabled(id string, enabled bool) error {
	err := a.accountStore.SetEnabled(id, enabled)
	if err != nil {
		return err
	}

	// Update IDLE manager
	if a.idleManager != nil {
		if enabled {
			// Start IDLE for the account
			acc, err := a.accountStore.Get(id)
			if err == nil && acc != nil {
				a.idleManager.StartAccount(acc.ID, acc.Name)
			}
		} else {
			// Stop IDLE for the account
			a.idleManager.StopAccount(id)
		}
	}

	return nil
}

// ReorderAccounts updates the order of accounts
func (a *App) ReorderAccounts(ids []string) error {
	return a.accountStore.Reorder(ids)
}

// AccountIdentityGroup groups an account with its identities for the cross-account From dropdown
type AccountIdentityGroup struct {
	Account    *account.Account    `json:"account"`
	Identities []*account.Identity `json:"identities"`
}

// GetAllAccountIdentities returns all accounts with their identities in one call.
// Used by the inline composer to populate the cross-account From dropdown.
func (a *App) GetAllAccountIdentities() ([]AccountIdentityGroup, error) {
	accounts, err := a.accountStore.List()
	if err != nil {
		return nil, err
	}
	var groups []AccountIdentityGroup
	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}
		identities, err := a.accountStore.GetIdentities(acc.ID)
		if err != nil {
			return nil, err
		}
		groups = append(groups, AccountIdentityGroup{
			Account:    acc,
			Identities: identities,
		})
	}
	return groups, nil
}

// GetIdentities returns all identities for an account
func (a *App) GetIdentities(accountID string) ([]*account.Identity, error) {
	return a.accountStore.GetIdentities(accountID)
}

// GetIdentity returns a single identity by ID
func (a *App) GetIdentity(identityID string) (*account.Identity, error) {
	return a.accountStore.GetIdentity(identityID)
}

// CreateIdentity creates a new email identity for an account
func (a *App) CreateIdentity(accountID string, config account.IdentityConfig) (*account.Identity, error) {
	return a.accountStore.CreateIdentity(accountID, &config)
}

// UpdateIdentity updates an existing identity
func (a *App) UpdateIdentity(identityID string, config account.IdentityConfig) (*account.Identity, error) {
	return a.accountStore.UpdateIdentity(identityID, &config)
}

// DeleteIdentity deletes an identity (cannot delete the default identity)
func (a *App) DeleteIdentity(identityID string) error {
	return a.accountStore.DeleteIdentity(identityID)
}

// SetDefaultIdentity sets an identity as the default for sending
func (a *App) SetDefaultIdentity(accountID, identityID string) error {
	return a.accountStore.SetDefaultIdentity(accountID, identityID)
}

// ============================================================================
// Connection Testing
// ============================================================================

// ConnectionTestResult holds the result of a connection test
type ConnectionTestResult struct {
	Success             bool                      `json:"success"`
	Error               string                    `json:"error,omitempty"`
	CertificateRequired bool                      `json:"certificateRequired"`
	Certificate         *certificate.CertificateInfo `json:"certificate,omitempty"`
}

// TestConnection tests the IMAP/SMTP connection for an account config
// For OAuth2 accounts, this only tests connectivity (no login) since the user
// hasn't authenticated yet during account creation.
func (a *App) TestConnection(config account.AccountConfig) ConnectionTestResult {
	log := logging.WithComponent("app")

	// Validate config first
	if err := config.Validate(); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}

	// For OAuth2 accounts, skip login test during account creation
	if config.AuthType == account.AuthOAuth2 {
		log.Info().
			Str("host", config.IMAPHost).
			Str("authType", string(config.AuthType)).
			Msg("Skipping connection test for OAuth2 account (will test after authorization)")
		return ConnectionTestResult{Success: true}
	}

	// Create a temporary IMAP client to test connection
	clientConfig := imap.DefaultConfig()
	clientConfig.Host = config.IMAPHost
	clientConfig.Port = config.IMAPPort
	clientConfig.Security = imap.SecurityType(config.IMAPSecurity)
	clientConfig.Username = config.Username
	clientConfig.Password = config.Password
	clientConfig.AuthType = imap.AuthTypePassword
	clientConfig.TLSConfig = certificate.BuildTLSConfig(config.IMAPHost, a.certStore)

	client := imap.NewClient(clientConfig)

	if err := client.Connect(); err != nil {
		var certErr *certificate.Error
		if errors.As(err, &certErr) {
			return ConnectionTestResult{
				CertificateRequired: true,
				Certificate:         certErr.Info,
			}
		}
		log.Error().Err(err).Msg("Connection test failed")
		return ConnectionTestResult{Error: fmt.Sprintf("failed to connect: %v", err)}
	}
	defer client.Close()

	if err := client.Login(); err != nil {
		log.Error().Err(err).Msg("Login test failed")
		return ConnectionTestResult{Error: fmt.Sprintf("failed to login: %v", err)}
	}

	log.Info().Str("host", config.IMAPHost).Msg("Connection test successful")
	return ConnectionTestResult{Success: true}
}
