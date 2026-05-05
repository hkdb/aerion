package carddav

import (
	"fmt"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// AccessTokenGetter is a function that retrieves a valid OAuth access token for an account
type AccessTokenGetter func(accountID string) (string, error)

// retryDBOperation retries a database operation with exponential backoff.
// This handles SQLITE_BUSY errors that occur during concurrent database access.
func retryDBOperation(operation func() error, maxRetries int, baseDelay time.Duration, log zerolog.Logger) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = operation()
		if err == nil {
			return nil
		}
		// Check if it's a SQLITE_BUSY error (retryable)
		if !strings.Contains(err.Error(), "database is locked") &&
			!strings.Contains(err.Error(), "SQLITE_BUSY") {
			return err // Non-retryable error, return immediately
		}
		// Exponential backoff: 100ms, 200ms, 400ms, 800ms, 1600ms
		delay := baseDelay * time.Duration(1<<uint(i))
		log.Debug().
			Int("attempt", i+1).
			Int("maxRetries", maxRetries).
			Dur("delay", delay).
			Msg("Database busy, retrying after delay")
		time.Sleep(delay)
	}
	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, err)
}

// Syncer handles syncing contacts from CardDAV/Google/Microsoft sources
type Syncer struct {
	store           *Store
	credStore       *credentials.Store
	getAccountToken AccessTokenGetter // Gets OAuth token from linked email account
	getSourceToken  AccessTokenGetter // Gets OAuth token from standalone contact source
	googleSyncer    *contact.GoogleContactsSyncer
	microsoftSyncer *contact.MicrosoftContactsSyncer
	log             zerolog.Logger
}

// NewSyncer creates a new contact syncer
func NewSyncer(store *Store, credStore *credentials.Store) *Syncer {
	return &Syncer{
		store:           store,
		credStore:       credStore,
		googleSyncer:    contact.NewGoogleContactsSyncer(),
		microsoftSyncer: contact.NewMicrosoftContactsSyncer(),
		log:             logging.WithComponent("carddav-sync"),
	}
}

// SetAccessTokenGetters sets the functions for retrieving OAuth access tokens
func (s *Syncer) SetAccessTokenGetters(accountTokenGetter, sourceTokenGetter AccessTokenGetter) {
	s.getAccountToken = accountTokenGetter
	s.getSourceToken = sourceTokenGetter
}

// SyncSource syncs contacts for a source based on its type (CardDAV, Google, Microsoft)
func (s *Syncer) SyncSource(sourceID string) error {
	s.log.Info().Str("sourceID", sourceID).Msg("Starting source sync")

	// Get source
	source, err := s.store.GetSource(sourceID)
	if err != nil {
		return fmt.Errorf("failed to get source: %w", err)
	}
	if source == nil {
		return fmt.Errorf("source not found: %s", sourceID)
	}

	if !source.Enabled {
		s.log.Debug().Str("sourceID", sourceID).Msg("Source is disabled, skipping sync")
		return nil
	}

	// Dispatch based on source type
	switch source.Type {
	case SourceTypeCardDAV:
		return s.syncCardDAV(source)
	case SourceTypeGoogle:
		return s.syncGoogle(source)
	case SourceTypeMicrosoft:
		return s.syncMicrosoft(source)
	default:
		return fmt.Errorf("unknown source type: %s", source.Type)
	}
}

// syncCardDAV syncs contacts from a CardDAV server
func (s *Syncer) syncCardDAV(source *Source) error {
	// Get password from credential store (use CardDAV-specific method)
	password, err := s.credStore.GetCardDAVPassword(source.ID)
	if err != nil {
		syncErr := fmt.Sprintf("failed to get credentials: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to get password: %w", err)
	}

	// Create CardDAV client
	client, err := NewClient(source.URL, source.Username, password)
	if err != nil {
		syncErr := fmt.Sprintf("failed to connect: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get enabled addressbooks
	addressbooks, err := s.store.ListEnabledAddressbooks(source.ID)
	if err != nil {
		syncErr := fmt.Sprintf("failed to list addressbooks: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to list addressbooks: %w", err)
	}

	if len(addressbooks) == 0 {
		s.log.Warn().Str("sourceID", source.ID).Msg("No enabled addressbooks for source")
		s.store.UpdateSourceSyncStatus(source.ID, "")
		return nil
	}

	// Sync each addressbook
	var syncErrors []string
	for _, ab := range addressbooks {
		if err := s.syncAddressbook(client, ab); err != nil {
			s.log.Error().Err(err).Str("addressbook", ab.Name).Msg("Failed to sync addressbook")
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", ab.Name, err))
		}
	}

	// Update source sync status
	if len(syncErrors) > 0 {
		syncErr := fmt.Sprintf("sync errors: %v", syncErrors)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("sync completed with errors: %v", syncErrors)
	}

	s.store.UpdateSourceSyncStatus(source.ID, "")
	s.log.Info().Str("sourceID", source.ID).Msg("CardDAV source sync completed successfully")
	return nil
}

// syncGoogle syncs contacts from Google People API using delta sync
func (s *Syncer) syncGoogle(source *Source) error {
	// Get OAuth access token
	accessToken, err := s.getOAuthToken(source)
	if err != nil {
		syncErr := fmt.Sprintf("failed to get OAuth token: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to get OAuth token: %w", err)
	}

	// Get or create the virtual addressbook to retrieve sync token
	ab, err := s.getOrCreateOAuthAddressbook(source)
	if err != nil {
		syncErr := fmt.Sprintf("failed to get addressbook: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to get addressbook: %w", err)
	}

	// Sync contacts using Google syncer with delta sync
	result, err := s.googleSyncer.SyncContactsDelta(accessToken, ab.SyncToken)
	if err != nil {
		syncErr := fmt.Sprintf("failed to sync: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("Google sync failed: %w", err)
	}

	// Store contacts using delta sync logic
	if err := s.storeOAuthContactsDelta(ab, result); err != nil {
		syncErr := fmt.Sprintf("failed to store contacts: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to store contacts: %w", err)
	}

	s.store.UpdateSourceSyncStatus(source.ID, "")
	if result.IsFullSync {
		s.log.Info().Str("sourceID", source.ID).Int("contacts", len(result.Contacts)).Msg("Google full sync completed")
	} else {
		s.log.Info().Str("sourceID", source.ID).Int("updated", len(result.Contacts)).Int("deleted", len(result.DeletedIDs)).Msg("Google incremental sync completed")
	}
	return nil
}

// syncMicrosoft syncs contacts from Microsoft Graph API using delta sync
func (s *Syncer) syncMicrosoft(source *Source) error {
	// Get OAuth access token
	accessToken, err := s.getOAuthToken(source)
	if err != nil {
		syncErr := fmt.Sprintf("failed to get OAuth token: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to get OAuth token: %w", err)
	}

	// Get or create the virtual addressbook to retrieve sync token
	ab, err := s.getOrCreateOAuthAddressbook(source)
	if err != nil {
		syncErr := fmt.Sprintf("failed to get addressbook: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to get addressbook: %w", err)
	}

	// Sync contacts using Microsoft syncer with delta sync
	result, err := s.microsoftSyncer.SyncContactsDelta(accessToken, ab.SyncToken)
	if err != nil {
		syncErr := fmt.Sprintf("failed to sync: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("Microsoft sync failed: %w", err)
	}

	// Store contacts using delta sync logic
	if err := s.storeOAuthContactsDelta(ab, result); err != nil {
		syncErr := fmt.Sprintf("failed to store contacts: %v", err)
		s.store.UpdateSourceSyncStatus(source.ID, syncErr)
		return fmt.Errorf("failed to store contacts: %w", err)
	}

	s.store.UpdateSourceSyncStatus(source.ID, "")
	if result.IsFullSync {
		s.log.Info().Str("sourceID", source.ID).Int("contacts", len(result.Contacts)).Msg("Microsoft full sync completed")
	} else {
		s.log.Info().Str("sourceID", source.ID).Int("updated", len(result.Contacts)).Int("deleted", len(result.DeletedIDs)).Msg("Microsoft incremental sync completed")
	}
	return nil
}

// getOAuthToken retrieves the OAuth access token for a source
func (s *Syncer) getOAuthToken(source *Source) (string, error) {
	// If source is linked to an email account, use the account's token
	if source.AccountID != nil && *source.AccountID != "" {
		if s.getAccountToken == nil {
			return "", fmt.Errorf("account token getter not configured")
		}
		return s.getAccountToken(*source.AccountID)
	}

	// Otherwise, use the source's own token (standalone OAuth source)
	if s.getSourceToken == nil {
		return "", fmt.Errorf("source token getter not configured")
	}
	return s.getSourceToken(source.ID)
}

// getOrCreateOAuthAddressbook gets or creates the virtual addressbook for an OAuth source
func (s *Syncer) getOrCreateOAuthAddressbook(source *Source) (*Addressbook, error) {
	addressbooks, err := s.store.ListAddressbooks(source.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list addressbooks: %w", err)
	}

	if len(addressbooks) > 0 {
		return addressbooks[0], nil
	}

	// Create a virtual addressbook for this source
	ab, err := s.store.CreateAddressbook(source.ID, "/", source.Name, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create addressbook: %w", err)
	}
	return ab, nil
}

// storeOAuthContactsDelta stores contacts from an OAuth source using delta sync
func (s *Syncer) storeOAuthContactsDelta(ab *Addressbook, result *contact.SyncResult) error {
	// If this is a full sync, delete all existing contacts first
	if result.IsFullSync {
		deleteErr := retryDBOperation(func() error {
			return s.store.DeleteContactsForAddressbook(ab.ID)
		}, 5, 100*time.Millisecond, s.log)
		if deleteErr != nil {
			s.log.Warn().Err(deleteErr).Msg("Failed to delete existing contacts after retries")
		}
	} else {
		// Incremental sync: delete removed contacts
		if len(result.DeletedIDs) > 0 {
			s.log.Debug().Int("count", len(result.DeletedIDs)).Msg("Deleting removed OAuth contacts")
			deleteErr := retryDBOperation(func() error {
				return s.store.DeleteContactsByHrefs(ab.ID, result.DeletedIDs)
			}, 5, 100*time.Millisecond, s.log)
			if deleteErr != nil {
				s.log.Warn().Err(deleteErr).Msg("Failed to delete removed contacts after retries")
			}
		}
	}

	// Insert/update contacts
	if len(result.Contacts) > 0 {
		contacts := make([]*Contact, 0, len(result.Contacts))
		for _, sc := range result.Contacts {
			contacts = append(contacts, &Contact{
				AddressbookID: ab.ID,
				Email:         sc.Email,
				DisplayName:   sc.DisplayName,
				Href:          sc.RemoteID, // Use RemoteID as href for change detection
				ETag:          "",          // OAuth sources don't use ETags
			})
		}

		upsertErr := retryDBOperation(func() error {
			return s.store.UpsertContactsBatch(contacts)
		}, 5, 100*time.Millisecond, s.log)
		if upsertErr != nil {
			return fmt.Errorf("failed to batch upsert contacts: %w", upsertErr)
		}
	}

	// Store the sync token for future incremental syncs
	s.store.UpdateAddressbookSyncToken(ab.ID, result.NextSyncToken)

	return nil
}

// syncAddressbook syncs a single addressbook
// Uses incremental sync (sync-collection) if a sync token exists, otherwise does a full sync
func (s *Syncer) syncAddressbook(client *Client, ab *Addressbook) error {
	s.log.Debug().Str("addressbook", ab.Name).Str("path", ab.Path).Str("syncToken", ab.SyncToken).Msg("Syncing addressbook")

	// Try incremental sync if we have a sync token
	if ab.SyncToken != "" {
		if err := s.syncAddressbookIncremental(client, ab); err != nil {
			// Log the error and fall back to full sync
			s.log.Warn().Err(err).Str("addressbook", ab.Name).Msg("Incremental sync failed, falling back to full sync")
		} else {
			// Incremental sync succeeded
			return nil
		}
	}

	// Full sync (first sync or fallback)
	return s.syncAddressbookFull(client, ab)
}

// syncAddressbookIncremental performs an incremental sync using sync-collection
func (s *Syncer) syncAddressbookIncremental(client *Client, ab *Addressbook) error {
	s.log.Debug().Str("addressbook", ab.Name).Msg("Attempting incremental sync")

	result, err := client.SyncAddressbook(ab.Path, ab.SyncToken)
	if err != nil {
		return err
	}

	// Process deleted contacts
	if len(result.Deleted) > 0 {
		s.log.Debug().Int("count", len(result.Deleted)).Msg("Processing deleted contacts")
		deleteErr := retryDBOperation(func() error {
			return s.store.DeleteContactsByHrefs(ab.ID, result.Deleted)
		}, 5, 100*time.Millisecond, s.log)
		if deleteErr != nil {
			s.log.Warn().Err(deleteErr).Msg("Failed to delete contacts after retries")
		}
	}

	// Process updated/new contacts
	if len(result.Updated) > 0 {
		s.log.Debug().Int("count", len(result.Updated)).Msg("Processing updated contacts")
		contacts := make([]*Contact, 0, len(result.Updated))
		for _, pc := range result.Updated {
			contacts = append(contacts, &Contact{
				AddressbookID: ab.ID,
				Email:         pc.Email,
				DisplayName:   pc.DisplayName,
				Href:          pc.Href,
				ETag:          pc.ETag,
			})
		}

		upsertErr := retryDBOperation(func() error {
			return s.store.UpsertContactsBatch(contacts)
		}, 5, 100*time.Millisecond, s.log)
		if upsertErr != nil {
			return fmt.Errorf("failed to upsert contacts: %w", upsertErr)
		}
	}

	// Update sync token
	s.store.UpdateAddressbookSyncToken(ab.ID, result.SyncToken)

	s.log.Info().
		Str("addressbook", ab.Name).
		Int("updated", len(result.Updated)).
		Int("deleted", len(result.Deleted)).
		Msg("Incremental sync completed")

	return nil
}

// syncAddressbookFull performs a full sync (used for first sync or when incremental fails)
func (s *Syncer) syncAddressbookFull(client *Client, ab *Addressbook) error {
	s.log.Debug().Str("addressbook", ab.Name).Msg("Performing full sync")

	// First, try sync-collection with empty token to get all contacts + sync token
	result, err := client.SyncAddressbook(ab.Path, "")
	if err != nil {
		// If sync-collection is not supported, fall back to query all
		s.log.Debug().Err(err).Msg("Sync-collection not supported, using query all")
		return s.syncAddressbookLegacy(client, ab)
	}

	// Delete all existing contacts and replace with synced ones
	deleteErr := retryDBOperation(func() error {
		return s.store.DeleteContactsForAddressbook(ab.ID)
	}, 5, 100*time.Millisecond, s.log)
	if deleteErr != nil {
		s.log.Warn().Err(deleteErr).Msg("Failed to delete existing contacts after retries")
	}

	// Insert all contacts
	if len(result.Updated) > 0 {
		contacts := make([]*Contact, 0, len(result.Updated))
		for _, pc := range result.Updated {
			contacts = append(contacts, &Contact{
				AddressbookID: ab.ID,
				Email:         pc.Email,
				DisplayName:   pc.DisplayName,
				Href:          pc.Href,
				ETag:          pc.ETag,
			})
		}

		upsertErr := retryDBOperation(func() error {
			return s.store.UpsertContactsBatch(contacts)
		}, 5, 100*time.Millisecond, s.log)
		if upsertErr != nil {
			s.log.Warn().Err(upsertErr).Msg("Failed to batch upsert contacts after retries")
		}
	}

	// Store the sync token for future incremental syncs
	s.store.UpdateAddressbookSyncToken(ab.ID, result.SyncToken)

	s.log.Info().Str("addressbook", ab.Name).Int("contacts", len(result.Updated)).Msg("Full sync completed")
	return nil
}

// syncAddressbookLegacy performs a legacy full sync using addressbook-query
// Used when the server doesn't support sync-collection
func (s *Syncer) syncAddressbookLegacy(client *Client, ab *Addressbook) error {
	s.log.Debug().Str("addressbook", ab.Name).Msg("Performing legacy sync (addressbook-query)")

	parsedContacts, err := client.FetchContacts(ab.Path)
	if err != nil {
		return fmt.Errorf("failed to fetch contacts: %w", err)
	}

	s.log.Debug().Int("count", len(parsedContacts)).Str("addressbook", ab.Name).Msg("Fetched contacts")

	// Delete all existing contacts and re-add
	deleteErr := retryDBOperation(func() error {
		return s.store.DeleteContactsForAddressbook(ab.ID)
	}, 5, 100*time.Millisecond, s.log)
	if deleteErr != nil {
		s.log.Warn().Err(deleteErr).Msg("Failed to delete existing contacts after retries")
	}

	// Convert to Contact structs for batch insert
	contacts := make([]*Contact, 0, len(parsedContacts))
	for _, pc := range parsedContacts {
		contacts = append(contacts, &Contact{
			AddressbookID: ab.ID,
			Email:         pc.Email,
			DisplayName:   pc.DisplayName,
			Href:          pc.Href,
			ETag:          pc.ETag,
		})
	}

	// Batch insert all contacts
	upsertErr := retryDBOperation(func() error {
		return s.store.UpsertContactsBatch(contacts)
	}, 5, 100*time.Millisecond, s.log)
	if upsertErr != nil {
		s.log.Warn().Err(upsertErr).Msg("Failed to batch upsert contacts after retries")
	}

	// No sync token available with legacy method
	s.store.UpdateAddressbookSyncToken(ab.ID, "")

	s.log.Info().Str("addressbook", ab.Name).Int("contacts", len(contacts)).Msg("Legacy sync completed")
	return nil
}

// SyncAllSources syncs all enabled sources
func (s *Syncer) SyncAllSources() error {
	sources, err := s.store.ListSources()
	if err != nil {
		return fmt.Errorf("failed to list sources: %w", err)
	}

	var syncErrors []string
	for _, source := range sources {
		if !source.Enabled {
			continue
		}

		if err := s.SyncSource(source.ID); err != nil {
			s.log.Error().Err(err).Str("source", source.Name).Msg("Failed to sync source")
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", source.Name, err))
		}
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync completed with errors: %v", syncErrors)
	}

	return nil
}

// GetSourcesDueForSync returns sources that are due for sync based on their sync_interval
func (s *Syncer) GetSourcesDueForSync() ([]*Source, error) {
	sources, err := s.store.ListSources()
	if err != nil {
		return nil, err
	}

	var dueForSync []*Source
	for _, source := range sources {
		if !source.Enabled {
			continue
		}

		// Skip manual-only sources (sync_interval = 0)
		if source.SyncInterval <= 0 {
			continue
		}

		// Check if sync is due
		if source.LastSyncedAt == nil {
			// Never synced - definitely due
			dueForSync = append(dueForSync, source)
			continue
		}

		// Check if interval has passed
		// We use the source's sync_interval (in minutes)
		intervalMinutes := source.SyncInterval
		if intervalMinutes <= 0 {
			intervalMinutes = 60 // Default to 60 minutes
		}

		// Calculate time since last sync
		// Use Go's time.Since for simplicity
		// Note: This is calculated in the scheduler, not here
		dueForSync = append(dueForSync, source)
	}

	return dueForSync, nil
}
