package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/oauth2"
	"github.com/hkdb/aerion/internal/platform"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// LinkedAccountInfo represents an email account that can be linked to a contact source
type LinkedAccountInfo struct {
	AccountID       string `json:"accountId"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`        // "google" or "microsoft"
	IsLinked        bool   `json:"isLinked"`        // Already has contact source linked
	HasContactScope bool   `json:"hasContactScope"` // Has required contacts scope
}

// ============================================================================
// CardDAV Contact Source API - Exposed to frontend via Wails bindings
// ============================================================================

// DiscoverCardDAVAddressbooks discovers available addressbooks from a CardDAV server
func (a *App) DiscoverCardDAVAddressbooks(url, username, password string) ([]carddav.AddressbookInfo, error) {
	return carddav.DiscoverAddressbooks(url, username, password)
}

// TestCardDAVConnection tests connection to a CardDAV server
func (a *App) TestCardDAVConnection(url, username, password string) error {
	return carddav.TestConnection(url, username, password)
}

// GetContactSources returns all configured contact sources
func (a *App) GetContactSources() ([]*carddav.Source, error) {
	return a.carddavStore.ListSources()
}

// GetContactSource returns a single contact source by ID
func (a *App) GetContactSource(id string) (*carddav.Source, error) {
	return a.carddavStore.GetSource(id)
}

// AddContactSource creates a new contact source with addressbooks
func (a *App) AddContactSource(config carddav.SourceConfig) (*carddav.Source, error) {
	log := logging.WithComponent("app")

	// Create the source
	source, err := a.carddavStore.CreateSource(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	// Store password in credential store (use CardDAV-specific method)
	if config.Password != "" {
		if err := a.credStore.SetCardDAVPassword(source.ID, config.Password); err != nil {
			// Rollback source creation
			a.carddavStore.DeleteSource(source.ID)
			return nil, fmt.Errorf("failed to store password: %w", err)
		}
	}

	// Create addressbooks based on enabled paths
	for _, path := range config.EnabledAddressbooks {
		// Try to get the name from discovery
		name := path
		if parts := strings.Split(strings.Trim(path, "/"), "/"); len(parts) > 0 {
			name = parts[len(parts)-1]
		}

		_, err := a.carddavStore.CreateAddressbook(source.ID, path, name, true)
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("Failed to create addressbook")
		}
	}

	// Trigger initial sync
	go a.carddavSyncer.SyncSource(source.ID)

	log.Info().Str("id", source.ID).Str("name", source.Name).Msg("Contact source created")
	return source, nil
}

// UpdateContactSource updates an existing contact source
func (a *App) UpdateContactSource(id string, config carddav.SourceConfig) error {
	log := logging.WithComponent("app")

	// Update the source
	if err := a.carddavStore.UpdateSource(id, &config); err != nil {
		return fmt.Errorf("failed to update source: %w", err)
	}

	// Update password if provided (use CardDAV-specific method)
	if config.Password != "" {
		if err := a.credStore.SetCardDAVPassword(id, config.Password); err != nil {
			return fmt.Errorf("failed to update password: %w", err)
		}
	}

	// Differential addressbook update: only delete addressbooks whose paths
	// are no longer in EnabledAddressbooks; only create addressbooks for
	// paths not already present. Existing addressbooks keep their UUID,
	// sync_token, last_synced_at, and all the carddav_record_state rows
	// pointing at them — so a writable-toggle save or a name-only edit
	// becomes a no-op on the addressbook side.
	//
	// The previous behavior tore down ALL addressbooks on every call and
	// re-created them with new UUIDs. carddav_record_state.addressbook_id
	// has no FK cascade, so every record row was orphaned (UI went empty;
	// the next full sync rebuilt the cache from scratch and left the old
	// rows as dead bloat).
	if len(config.EnabledAddressbooks) > 0 {
		existing, listErr := a.carddavStore.ListAddressbooks(id)
		if listErr != nil {
			return fmt.Errorf("failed to list current addressbooks: %w", listErr)
		}

		existingByPath := make(map[string]*carddav.Addressbook, len(existing))
		for _, ab := range existing {
			existingByPath[ab.Path] = ab
		}
		incomingByPath := make(map[string]bool, len(config.EnabledAddressbooks))
		for _, path := range config.EnabledAddressbooks {
			incomingByPath[path] = true
		}

		// Delete addressbooks the user removed from their selection. Uses
		// DeleteAddressbookByID (tx-wrapped) so the carddav_record_state
		// rows + contact_records under that addressbook are cleaned up.
		for path, ab := range existingByPath {
			if incomingByPath[path] {
				continue
			}
			if err := a.carddavStore.DeleteAddressbookByID(ab.ID); err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to delete removed addressbook")
			}
		}

		// Create addressbooks for paths the user newly enabled. Existing
		// paths are skipped — preserving their UUID and downstream cache.
		for _, path := range config.EnabledAddressbooks {
			if _, exists := existingByPath[path]; exists {
				continue
			}
			name := path
			if parts := strings.Split(strings.Trim(path, "/"), "/"); len(parts) > 0 {
				name = parts[len(parts)-1]
			}
			if _, err := a.carddavStore.CreateAddressbook(id, path, name, true); err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to create new addressbook")
			}
		}
	}

	// Trigger resync
	go a.carddavSyncer.SyncSource(id)

	log.Info().Str("id", id).Msg("Contact source updated")
	return nil
}

// DeleteContactSource deletes a contact source and all its data
func (a *App) DeleteContactSource(id string) error {
	log := logging.WithComponent("app")

	// Get source first to check type
	source, _ := a.carddavStore.GetSource(id)

	// Delete from database (cascades to addressbooks and contacts)
	if err := a.carddavStore.DeleteSource(id); err != nil {
		return fmt.Errorf("failed to delete source: %w", err)
	}

	// Delete credentials based on source type
	if source != nil {
		switch source.Type {
		case carddav.SourceTypeCardDAV:
			a.credStore.DeleteCardDAVPassword(id)
		case carddav.SourceTypeGoogle, carddav.SourceTypeMicrosoft:
			// Only delete OAuth tokens for standalone sources (not linked to an account)
			if source.AccountID == nil || *source.AccountID == "" {
				a.credStore.DeleteContactSourceOAuthTokens(id)
			}
		}
	}

	log.Info().Str("id", id).Msg("Contact source deleted")
	return nil
}

// GetSourceAddressbooks returns all addressbooks for a source
func (a *App) GetSourceAddressbooks(sourceID string) ([]*carddav.Addressbook, error) {
	return a.carddavStore.ListAddressbooks(sourceID)
}

// SetAddressbookEnabled enables or disables an addressbook
func (a *App) SetAddressbookEnabled(addressbookID string, enabled bool) error {
	return a.carddavStore.SetAddressbookEnabled(addressbookID, enabled)
}

// SetContactSourceWritable flips the writable flag for a CardDAV source.
// Phase 2b.2.a UI surface — backs the "Enable write access" checkbox in the
// per-source settings dialog. CardDAV uses the source's existing basic-auth
// credentials, so this is a pure flag flip (no consent flow needed). OAuth-
// based sources (Google/Microsoft) get their toggle in 2b.3 alongside
// incremental consent.
func (a *App) SetContactSourceWritable(sourceID string, writable bool) error {
	return a.carddavStore.SetSourceWritable(sourceID, writable)
}

// SyncContactSource manually triggers a sync for a source
func (a *App) SyncContactSource(id string) error {
	return a.carddavSyncer.SyncSource(id)
}

// SyncAllContactSources manually triggers a sync for all sources
func (a *App) SyncAllContactSources() error {
	return a.carddavSyncer.SyncAllSources()
}

// ForceSyncContactSource clears the per-addressbook sync tokens for a
// CardDAV source so the next sync re-fetches every vCard from the
// server. Used to backfill multi-field data (phones, addresses, org,
// notes, etc.) for contacts originally synced under a legacy schema
// where the old parser only stored email + display name. Mirrors
// App.ForceSyncFolder for mail messages.
func (a *App) ForceSyncContactSource(sourceID string) error {
	abs, err := a.carddavStore.ListAddressbooks(sourceID)
	if err != nil {
		return fmt.Errorf("failed to list addressbooks: %w", err)
	}
	for _, ab := range abs {
		if err := a.carddavStore.UpdateAddressbookSyncToken(ab.ID, ""); err != nil {
			return fmt.Errorf("failed to clear sync token for addressbook %s: %w", ab.ID, err)
		}
	}
	return a.carddavSyncer.SyncSource(sourceID)
}

// GetContactSourceErrors returns all sources that have errors
func (a *App) GetContactSourceErrors() ([]*carddav.SourceError, error) {
	return a.carddavStore.GetSourcesWithErrors()
}

// GetContactSourceStats returns statistics for contact sources
func (a *App) GetContactSourceStats() (map[string]interface{}, error) {
	sources, err := a.carddavStore.ListSources()
	if err != nil {
		return nil, err
	}

	totalContacts, _ := a.carddavStore.CountContacts()
	sourcesWithErrors, _ := a.carddavStore.GetSourcesWithErrors()

	return map[string]interface{}{
		"total_sources":       len(sources),
		"total_contacts":      totalContacts,
		"sources_with_errors": len(sourcesWithErrors),
	}, nil
}

// ============================================================================
// OAuth Contact Source API - Google/Microsoft contact sync
// ============================================================================

// GetLinkedAccountsForContactSync returns email accounts that can be linked to contact sources
func (a *App) GetLinkedAccountsForContactSync() ([]LinkedAccountInfo, error) {
	log := logging.WithComponent("app")

	accounts, err := a.accountStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	// Get existing contact sources to check which accounts are already linked
	sources, err := a.carddavStore.ListSources()
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}

	// Build a map of linked account IDs
	linkedAccountIDs := make(map[string]bool)
	for _, source := range sources {
		if source.AccountID != nil && *source.AccountID != "" {
			linkedAccountIDs[*source.AccountID] = true
		}
	}

	var result []LinkedAccountInfo
	for _, acc := range accounts {
		// Only OAuth accounts (Google/Microsoft) can be linked for contacts
		if acc.AuthType != account.AuthOAuth2 {
			continue
		}

		// Get OAuth provider
		provider, err := a.credStore.GetOAuthProvider(acc.ID)
		if err != nil || provider == "" {
			continue
		}

		// Only Google supports linked contact sync
		// Microsoft can't share tokens between Outlook (email) and Graph API (contacts) due to audience restrictions
		if provider != "google" {
			continue
		}

		// Check if account has contact scope
		tokens, err := a.credStore.GetOAuthTokens(acc.ID)
		hasContactScope := false
		if err == nil && tokens != nil {
			for _, scope := range tokens.Scopes {
				if strings.Contains(scope, "contacts") {
					hasContactScope = true
					break
				}
			}
		}

		result = append(result, LinkedAccountInfo{
			AccountID:       acc.ID,
			Email:           acc.Email,
			Name:            acc.Name,
			Provider:        provider,
			IsLinked:        linkedAccountIDs[acc.ID],
			HasContactScope: hasContactScope,
		})
	}

	log.Debug().Int("count", len(result)).Msg("Found linkable accounts for contact sync")
	return result, nil
}

// LinkAccountContactSource creates a contact source linked to an existing email account
func (a *App) LinkAccountContactSource(accountID string, name string, syncInterval int) (*carddav.Source, error) {
	log := logging.WithComponent("app")

	// Verify account exists and is OAuth
	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	if acc.AuthType != account.AuthOAuth2 {
		return nil, fmt.Errorf("account is not an OAuth account")
	}

	// Get provider from account
	provider, err := a.credStore.GetOAuthProvider(accountID)
	if err != nil || provider == "" {
		return nil, fmt.Errorf("could not determine OAuth provider for account")
	}

	// Determine source type
	var sourceType carddav.SourceType
	switch provider {
	case "google":
		sourceType = carddav.SourceTypeGoogle
	case "microsoft":
		sourceType = carddav.SourceTypeMicrosoft
	default:
		return nil, fmt.Errorf("unsupported provider for contacts: %s", provider)
	}

	// Check if account is already linked
	existing, _ := a.carddavStore.GetSourceByAccountID(accountID)
	if existing != nil {
		return nil, fmt.Errorf("account already has a contact source linked")
	}

	// Create the source config
	config := carddav.SourceConfig{
		Name:         name,
		Type:         sourceType,
		AccountID:    accountID,
		Enabled:      true,
		SyncInterval: syncInterval,
	}

	// Create the source
	source, err := a.carddavStore.CreateSource(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	// Trigger initial sync
	go a.carddavSyncer.SyncSource(source.ID)

	log.Info().
		Str("sourceID", source.ID).
		Str("accountID", accountID).
		Str("provider", provider).
		Msg("Contact source linked to email account")

	return source, nil
}

// StartContactsOnlyOAuthFlow initiates OAuth flow for standalone contact source
func (a *App) StartContactsOnlyOAuthFlow(provider string) error {
	log := logging.WithComponent("app.contacts-oauth")

	// Validate provider
	if provider != "google" && provider != "microsoft" {
		return fmt.Errorf("unsupported provider for contacts: %s", provider)
	}

	// Get contacts-only provider config
	var providerConfig oauth2.ProviderConfig
	switch provider {
	case "google":
		providerConfig = oauth2.GoogleContactsOnlyProvider()
	case "microsoft":
		providerConfig = oauth2.MicrosoftContactsOnlyProvider()
	}

	// Check if provider is configured
	if providerConfig.ClientID == "" {
		return fmt.Errorf("OAuth provider %s is not configured", provider)
	}

	log.Info().Str("provider", provider).Msg("Starting contacts-only OAuth flow")

	// Start the OAuth flow using the contacts-only provider
	authURL, err := a.oauth2Manager.StartAuthFlowWithProvider(a.ctx, &providerConfig)
	if err != nil {
		wailsRuntime.EventsEmit(a.ctx, "contact-source-oauth:error", map[string]interface{}{
			"provider": provider,
			"error":    err.Error(),
		})
		return fmt.Errorf("failed to start OAuth flow: %w", err)
	}

	// Emit started event with the auth URL so the frontend can show a
	// "Copy link" fallback affordance for users whose browser fails to open.
	wailsRuntime.EventsEmit(a.ctx, "contact-source-oauth:started", map[string]interface{}{
		"provider": provider,
		"authURL":  authURL,
	})

	// Open browser with auth URL. Portal-first for Flatpak/Wayland correctness,
	// fall back to Wails' BrowserOpenURL on portal error.
	if perr := platform.PortalOpenURI(authURL); perr != nil {
		log.Debug().Err(perr).Msg("Portal OpenURI failed, falling back to BrowserOpenURL")
		wailsRuntime.BrowserOpenURL(a.ctx, authURL)
	}

	// Wait for callback in background
	go func() {
		defer recoverPanic("app.carddav", "CardDAV OAuth callback")
		tokens, email, err := a.oauth2Manager.WaitForCallback(a.ctx)
		if err != nil {
			log.Error().Err(err).Str("provider", provider).Msg("Contact source OAuth callback failed")
			wailsRuntime.EventsEmit(a.ctx, "contact-source-oauth:error", map[string]interface{}{
				"provider": provider,
				"error":    err.Error(),
			})
			return
		}

		// Store tokens temporarily for source creation
		a.pendingContactSourceOAuthTokens = tokens
		a.pendingContactSourceOAuthEmail = email
		a.pendingContactSourceOAuthProvider = provider

		log.Info().
			Str("provider", provider).
			Str("email", email).
			Msg("Contact source OAuth flow completed successfully")

		// Emit success event
		wailsRuntime.EventsEmit(a.ctx, "contact-source-oauth:success", map[string]interface{}{
			"provider":  provider,
			"email":     email,
			"expiresIn": tokens.ExpiresIn,
		})
	}()

	return nil
}

// CompleteContactSourceOAuthSetup creates a standalone contact source after OAuth
func (a *App) CompleteContactSourceOAuthSetup(name string, syncInterval int) (*carddav.Source, error) {
	log := logging.WithComponent("app.contacts-oauth")

	// Check that we have pending tokens
	if a.pendingContactSourceOAuthTokens == nil {
		return nil, fmt.Errorf("no pending OAuth tokens - please complete the sign-in process first")
	}

	provider := a.pendingContactSourceOAuthProvider
	email := a.pendingContactSourceOAuthEmail

	log.Info().
		Str("provider", provider).
		Str("email", email).
		Str("name", name).
		Msg("Completing contact source OAuth setup")

	// Determine source type
	var sourceType carddav.SourceType
	switch provider {
	case "google":
		sourceType = carddav.SourceTypeGoogle
	case "microsoft":
		sourceType = carddav.SourceTypeMicrosoft
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}

	// Get provider config for scopes
	var providerConfig oauth2.ProviderConfig
	switch provider {
	case "google":
		providerConfig = oauth2.GoogleContactsOnlyProvider()
	case "microsoft":
		providerConfig = oauth2.MicrosoftContactsOnlyProvider()
	}

	// Create source config
	config := carddav.SourceConfig{
		Name:         name,
		Type:         sourceType,
		Enabled:      true,
		SyncInterval: syncInterval,
	}

	// Create the source
	source, err := a.carddavStore.CreateSource(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	// Calculate token expiry
	expiresAt := time.Now().Add(time.Duration(a.pendingContactSourceOAuthTokens.ExpiresIn) * time.Second)

	// Save OAuth tokens for the source
	tokens := &credentials.OAuthTokens{
		Provider:     provider,
		AccessToken:  a.pendingContactSourceOAuthTokens.AccessToken,
		RefreshToken: a.pendingContactSourceOAuthTokens.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       providerConfig.Scopes,
	}

	if err := a.credStore.SetContactSourceOAuthTokens(source.ID, tokens); err != nil {
		// Rollback source creation
		a.carddavStore.DeleteSource(source.ID)
		return nil, fmt.Errorf("failed to save OAuth tokens: %w", err)
	}

	// Clear pending tokens
	a.pendingContactSourceOAuthTokens = nil
	a.pendingContactSourceOAuthEmail = ""
	a.pendingContactSourceOAuthProvider = ""

	// Trigger initial sync
	go a.carddavSyncer.SyncSource(source.ID)

	log.Info().
		Str("sourceID", source.ID).
		Str("provider", provider).
		Str("email", email).
		Msg("Standalone contact source created")

	return source, nil
}

// CancelContactSourceOAuthFlow cancels any in-progress contact source OAuth flow
func (a *App) CancelContactSourceOAuthFlow() {
	log := logging.WithComponent("app.contacts-oauth")
	log.Info().Msg("Cancelling contact source OAuth flow")

	a.oauth2Manager.CancelAuthFlow()

	// Clear any pending tokens
	a.pendingContactSourceOAuthTokens = nil
	a.pendingContactSourceOAuthEmail = ""
	a.pendingContactSourceOAuthProvider = ""

	wailsRuntime.EventsEmit(a.ctx, "contact-source-oauth:cancelled", nil)
}
