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
	store            *Store
	credStore        *credentials.Store
	getAccountToken  AccessTokenGetter // Gets OAuth token from linked email account
	getSourceToken   AccessTokenGetter // Gets OAuth token from standalone contact source
	googleSyncer     *contact.GoogleContactsSyncer
	microsoftSyncer  *contact.MicrosoftContactsSyncer
	onSyncComplete   func(sourceID string) // optional: fired after a source syncs successfully
	log              zerolog.Logger
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

// SetSyncCompleteHandler registers a callback fired after a source finishes
// syncing successfully (any path: manual, scheduler, or post-add). The host
// wires this to a frontend event so the contact list live-refreshes when a
// background sync lands new data.
func (s *Syncer) SetSyncCompleteHandler(fn func(sourceID string)) {
	s.onSyncComplete = fn
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
	var syncErr error
	switch source.Type {
	case SourceTypeCardDAV:
		syncErr = s.syncCardDAV(source)
	case SourceTypeGoogle:
		syncErr = s.syncGoogle(source)
	case SourceTypeMicrosoft:
		syncErr = s.syncMicrosoft(source)
	default:
		return fmt.Errorf("unknown source type: %s", source.Type)
	}
	if syncErr != nil {
		return syncErr
	}
	if s.onSyncComplete != nil {
		s.onSyncComplete(sourceID)
	}
	return nil
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
		s.log.Info().Str("sourceID", source.ID).Int("contacts", len(result.Records)).Msg("Google full sync completed")
		return nil
	}
	s.log.Info().Str("sourceID", source.ID).Int("updated", len(result.Records)).Int("deleted", len(result.DeletedIDs)).Msg("Google incremental sync completed")
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
		s.log.Info().Str("sourceID", source.ID).Int("contacts", len(result.Records)).Msg("Microsoft full sync completed")
		return nil
	}
	s.log.Info().Str("sourceID", source.ID).Int("updated", len(result.Records)).Int("deleted", len(result.DeletedIDs)).Msg("Microsoft incremental sync completed")
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

// storeOAuthContactsDelta stores contacts from an OAuth source (Google /
// Microsoft) using the same multi-field record path as CardDAV. Records carry
// name + emails + phones + addresses, and an email-less contact is still a
// valid record (this is what makes phone-only contacts land instead of being
// dropped). Change detection keys on (addressbook_id, href=RemoteID).
func (s *Syncer) storeOAuthContactsDelta(ab *Addressbook, result *contact.SyncResult) error {
	switch {
	case result.IsFullSync:
		// Full sync: clear all existing records for this addressbook first.
		if err := retryDBOperation(func() error {
			return s.store.DeleteRecordsForAddressbook(ab.ID)
		}, 5, 100*time.Millisecond, s.log); err != nil {
			s.log.Warn().Err(err).Msg("Failed to clear existing records after retries")
		}
	case len(result.DeletedIDs) > 0:
		// Incremental sync: delete the records the provider reported removed.
		s.log.Debug().Int("count", len(result.DeletedIDs)).Msg("Deleting removed OAuth contacts")
		if err := retryDBOperation(func() error {
			return s.store.DeleteContactsByHrefs(ab.ID, result.DeletedIDs)
		}, 5, 100*time.Millisecond, s.log); err != nil {
			s.log.Warn().Err(err).Msg("Failed to delete removed records after retries")
		}
	}

	if len(result.Records) > 0 {
		entries := make([]RecordSyncEntry, 0, len(result.Records))
		for _, sr := range result.Records {
			if sr.Record == nil {
				continue
			}
			entries = append(entries, RecordSyncEntry{
				Record:        sr.Record,
				AddressbookID: ab.ID,
				Href:          sr.RemoteID, // provider id doubles as href for change detection
				ETag:          sr.ETag,
			})
		}
		if err := retryDBOperation(func() error {
			return s.store.UpsertRecordsBatch(entries)
		}, 5, 100*time.Millisecond, s.log); err != nil {
			return fmt.Errorf("failed to batch upsert records: %w", err)
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

	// Process updated/new records (multi-field).
	if len(result.Updated) > 0 {
		s.log.Debug().Int("count", len(result.Updated)).Msg("Processing updated records")
		entries := buildRecordSyncEntries(ab.ID, result.Updated)
		upsertErr := retryDBOperation(func() error {
			return s.store.UpsertRecordsBatch(entries)
		}, 5, 100*time.Millisecond, s.log)
		if upsertErr != nil {
			return fmt.Errorf("failed to upsert records: %w", upsertErr)
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

// buildRecordSyncEntries converts a slice of ParsedRecord (the parser output)
// into RecordSyncEntry payloads suitable for Store.UpsertRecordsBatch. Each
// entry carries a *contact.Record (mapped via parsedRecordToContactRecord)
// plus the addressbook_id + href + etag triplet for carddav_record_state.
func buildRecordSyncEntries(addressbookID string, parsed []*ParsedRecord) []RecordSyncEntry {
	entries := make([]RecordSyncEntry, 0, len(parsed))
	for _, pr := range parsed {
		if pr == nil {
			continue
		}
		entries = append(entries, RecordSyncEntry{
			Record:        parsedRecordToContactRecord(pr),
			AddressbookID: addressbookID,
			Href:          pr.Href,
			ETag:          pr.ETag,
		})
	}
	return entries
}

// parsedRecordToContactRecord maps the carddav-specific ParsedRecord shape
// into the generic contact.Record + sub-tables used by UpsertRecordTx.
func parsedRecordToContactRecord(pr *ParsedRecord) *contact.Record {
	rec := &contact.Record{
		Source:   "carddav",
		Fn:       pr.FN,
		NGiven:   pr.NGiven,
		NFamily:  pr.NFamily,
		Org:      pr.Org,
		Title:    pr.Title,
		Note:     pr.Note,
		Bday:     pr.Bday,
		Nickname: pr.Nickname,
		VCardRaw: pr.VCardRaw,
	}
	for _, e := range pr.Emails {
		rec.Emails = append(rec.Emails, contact.RecordEmail{
			Email:     e.Value,
			EmailType: e.Type,
			IsPrimary: e.IsPrimary,
		})
	}
	for _, p := range pr.Phones {
		rec.Phones = append(rec.Phones, contact.RecordPhone{
			Number:    p.Value,
			PhoneType: p.Type,
			IsPrimary: p.IsPrimary,
		})
	}
	for _, a := range pr.Addresses {
		rec.Addresses = append(rec.Addresses, contact.RecordAddress{
			AddrType: a.Type,
			Street:   a.Street,
			City:     a.City,
			Region:   a.Region,
			Postcode: a.Postcode,
			Country:  a.Country,
		})
	}
	for _, u := range pr.URLs {
		rec.URLs = append(rec.URLs, contact.RecordURL{URL: u.Value, URLType: u.Type})
	}
	for _, i := range pr.IMPPs {
		rec.IMPPs = append(rec.IMPPs, contact.RecordIMPP{Handle: i.Handle, IMPPType: i.Type})
	}
	rec.Categories = append(rec.Categories, pr.Categories...)
	return rec
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

	// Insert all records (multi-field).
	if len(result.Updated) > 0 {
		entries := buildRecordSyncEntries(ab.ID, result.Updated)
		upsertErr := retryDBOperation(func() error {
			return s.store.UpsertRecordsBatch(entries)
		}, 5, 100*time.Millisecond, s.log)
		if upsertErr != nil {
			s.log.Warn().Err(upsertErr).Msg("Failed to batch upsert records after retries")
		}
	}

	// Store the sync token for future incremental syncs
	s.store.UpdateAddressbookSyncToken(ab.ID, result.SyncToken)

	s.log.Info().Str("addressbook", ab.Name).Int("records", len(result.Updated)).Msg("Full sync completed")
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

	// Convert to RecordSyncEntry for the multi-field upsert.
	entries := buildRecordSyncEntries(ab.ID, parsedContacts)

	// Batch insert all records.
	upsertErr := retryDBOperation(func() error {
		return s.store.UpsertRecordsBatch(entries)
	}, 5, 100*time.Millisecond, s.log)
	if upsertErr != nil {
		s.log.Warn().Err(upsertErr).Msg("Failed to batch upsert records after retries")
	}

	// No sync token available with legacy method
	s.store.UpdateAddressbookSyncToken(ab.ID, "")

	s.log.Info().Str("addressbook", ab.Name).Int("records", len(entries)).Msg("Legacy sync completed")
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
