package backend

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/contact"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/logging"
)

// google_api.go holds the Phase 2b.3 Google People API write path — the
// extension's side of the create/update/delete/list-addressbooks flow.
// google_write.go is the low-level HTTP client; google_convert.go is the
// Person ↔ contact.Record mapping. This file glues those to api.go's
// source-type dispatch and to the local DB.
//
// Why split: api.go is already large, and Track C will mirror this file as
// microsoft_api.go. Keeping per-provider write logic in dedicated files makes
// it obvious which API surface each function targets.

// googleWriteScope is the People API scope we request for write paths. Phase
// 2b.3's manifest lists ONLY readonly scopes under first_party_uses_core_for_scopes,
// so this scope routes through the `google-contacts` per-extension OAuth slot
// and triggers ErrAdditionalConsentRequired on first call — caught by the
// frontend wrapper, which prompts the consent dialog and retries.
const googleWriteScope = "https://www.googleapis.com/auth/contacts"

// googleReadScope is the readonly scope used by listGoogleAddressbooks. It's
// already covered by the existing READ-side sync's grant, so this call goes
// through the broker without triggering consent.
const googleReadScope = "https://www.googleapis.com/auth/contacts.readonly"

// googleCallTimeout caps the HTTP step for interactive writes. Long enough to
// tolerate one 429 retry-after sleep (typically a few seconds) but short
// enough that the UI doesn't hang forever on network failure.
const googleCallTimeout = 45 * time.Second

// createGoogleContact creates a new contact in the user's Google contacts via
// the People API and persists a mirror row locally. Filled in for Phase 2b.3
// Track B (was a shell in Track A).
//
// Flow:
//  1. Validate source + look up auth client.
//  2. Build googlePerson from input email+name (other fields land via Edit).
//  3. POST people:createContact.
//  4. If the chosen addressbook is a contactGroup, ModifyGroupMembership(add).
//  5. Resolve the physical addressbook id for this source (created by the
//     READ-side syncer at link time) — used as source_ref so subsequent
//     reads find this contact.
//  6. Persist contact_records + carddav_record_state in one transaction.
//  7. SetETag on the per-extension store so the next update can use it.
func (a *API) createGoogleContact(input coreapi.ContactCreateInput, email string, source *carddav.Source) (string, error) {
	if a.core == nil {
		return "", fmt.Errorf("contacts.createGoogleContact: core not wired")
	}
	if a.db == nil {
		return "", fmt.Errorf("contacts.createGoogleContact: db not wired")
	}
	if a.extStore == nil {
		return "", fmt.Errorf("contacts.createGoogleContact: extension store not wired")
	}
	if source == nil || source.AccountID == nil || *source.AccountID == "" {
		return "", fmt.Errorf("contacts.createGoogleContact: google source has no linked account")
	}
	if !source.Writable {
		return "", fmt.Errorf("contacts.createGoogleContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: googleWriteScope, Reason: "Create contacts in your Google account"},
	})
	if err != nil {
		// ErrAdditionalConsentRequired flows up — bridge translates to
		// ConsentRequiredError JSON for the frontend wrapper.
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("google-contacts-write")
	writer := NewGoogleContactsWriter(httpClient)

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = email
	}
	rec := &contact.Record{
		Fn: name,
		Emails: []contact.RecordEmail{{
			Email:     email,
			IsPrimary: true,
		}},
	}
	person := recordToGooglePerson(rec, log)

	created, err := writer.CreateContact(ctx, person)
	if err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: %w", err)
	}
	if created.ResourceName == "" {
		return "", fmt.Errorf("contacts.createGoogleContact: server returned no resourceName")
	}

	groupID := parseAddressbookGroupID(input.AddressbookID)
	if groupID != "" {
		if merr := writer.ModifyGroupMembership(ctx, groupID, []string{created.ResourceName}, nil); merr != nil {
			// Non-fatal — the contact exists, just not in the chosen group.
			log.Warn().Err(merr).Str("group", groupID).Str("resourceName", created.ResourceName).Msg("Failed to add new contact to group; contact saved without group")
		}
	}

	addressbookID, err := a.firstAddressbookForSource(source.ID)
	if err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: resolve addressbook for source %s: %w", source.ID, err)
	}

	recordID := uuid.New().String()
	// Stitch the server-accepted state onto rec, then persist.
	persisted := googlePersonToRecord(created)
	if persisted == nil {
		persisted = rec
	}
	persisted.ID = recordID
	persisted.Source = "carddav"
	persisted.SourceRef = addressbookID

	tx, err := a.db.Begin()
	if err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := contact.UpsertRecordTx(tx, persisted); err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: upsert local record: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
		VALUES (?, ?, ?, ?, ?)
	`, recordID, addressbookID, created.ResourceName, "", time.Now()); err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: insert record_state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("contacts.createGoogleContact: commit: %w", err)
	}

	if etag := etagFromPerson(created); etag != "" {
		if err := a.extStore.SetETag(recordID, etag); err != nil {
			// Non-fatal — next update will GET-to-refresh.
			log.Warn().Err(err).Str("record_id", recordID).Msg("Failed to persist initial Google etag")
		}
	}

	log.Info().Str("record_id", recordID).Str("resourceName", created.ResourceName).Msg("Google contact created")
	return recordID, nil
}

// updateGoogleContact PATCHes the existing contact under rec.ID. Loads the
// stored ETag from extStore — if empty (first write after a sync), does a GET
// to refresh the etag and retries the PATCH once. 412/failedPrecondition
// becomes *coreapi.ErrConflict, which Bridge.emitConflict translates into the
// `contacts:conflict` Wails event the UI already handles.
//
// Photos are handled separately after the main PATCH. Photo-step failure is
// non-fatal (the contact itself is saved; just the avatar didn't change).
func (a *API) updateGoogleContact(rec *contact.Record) error {
	if a.core == nil {
		return fmt.Errorf("contacts.updateGoogleContact: core not wired")
	}
	if a.db == nil {
		return fmt.Errorf("contacts.updateGoogleContact: db not wired")
	}
	if a.extStore == nil {
		return fmt.Errorf("contacts.updateGoogleContact: extension store not wired")
	}

	source, resourceName, err := a.googleSourceAndHrefForRecord(rec)
	if err != nil {
		return err
	}
	if !source.Writable {
		return fmt.Errorf("contacts.updateGoogleContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: googleWriteScope, Reason: "Update contacts in your Google account"},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("google-contacts-write")
	writer := NewGoogleContactsWriter(httpClient)

	etag, err := a.extStore.GetETag(rec.ID)
	if err != nil {
		return fmt.Errorf("contacts.updateGoogleContact: load etag: %w", err)
	}
	if etag == "" {
		current, gerr := writer.GetContact(ctx, resourceName)
		if gerr != nil {
			return fmt.Errorf("contacts.updateGoogleContact: refresh etag: %w", gerr)
		}
		etag = etagFromPerson(current)
		if etag == "" {
			return fmt.Errorf("contacts.updateGoogleContact: server returned no etag for %s", resourceName)
		}
	}

	person := recordToGooglePerson(rec, log)
	mask := fieldMaskForRecord(rec)

	updated, err := writer.UpdateContact(ctx, resourceName, etag, person, mask)
	if err != nil {
		var etagErr *ErrGoogleEtagMismatch
		if errors.As(err, &etagErr) {
			return &coreapi.ErrConflict{ContactID: rec.ID, Message: "contact was modified elsewhere"}
		}
		return fmt.Errorf("contacts.updateGoogleContact: %w", err)
	}

	// Mirror the server's accepted state locally. UpsertRecordTx replaces
	// sub-tables wholesale, which matches Google's PATCH-replaces semantics.
	persisted := googlePersonToRecord(updated)
	if persisted == nil {
		persisted = rec
	}
	persisted.ID = rec.ID
	persisted.Source = "carddav"
	persisted.SourceRef = rec.SourceRef

	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("contacts.updateGoogleContact: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := contact.UpsertRecordTx(tx, persisted); err != nil {
		return fmt.Errorf("contacts.updateGoogleContact: upsert local record: %w", err)
	}
	if _, err := tx.Exec(`UPDATE carddav_record_state SET synced_at = ? WHERE record_id = ?`, time.Now(), rec.ID); err != nil {
		return fmt.Errorf("contacts.updateGoogleContact: touch record_state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("contacts.updateGoogleContact: commit: %w", err)
	}

	if newEtag := etagFromPerson(updated); newEtag != "" {
		if err := a.extStore.SetETag(rec.ID, newEtag); err != nil {
			log.Warn().Err(err).Str("record_id", rec.ID).Msg("Failed to refresh stored Google etag after update")
		}
	}

	// Photo: only push if the record carries inline photo bytes. Out-of-band
	// PHOTO URLs aren't pushed to Google (Google rejects URL-only photos).
	if rec.PhotoData != "" {
		photoBytes, derr := base64.StdEncoding.DecodeString(rec.PhotoData)
		if derr != nil {
			log.Warn().Err(derr).Msg("Failed to decode photo bytes; skipping photo update")
		} else if _, perr := writer.UpdatePhoto(ctx, resourceName, photoBytes); perr != nil {
			log.Warn().Err(perr).Str("resourceName", resourceName).Msg("Failed to update Google contact photo; contact saved without photo update")
		}
	}

	log.Info().Str("record_id", rec.ID).Str("resourceName", resourceName).Msg("Google contact updated")
	return nil
}

// deleteGoogleContact removes the contact from Google and cascades the local
// rows via the contact_records FK. extStore.DeleteETag drops the version
// stamp.
func (a *API) deleteGoogleContact(rec *contact.Record) error {
	if a.core == nil {
		return fmt.Errorf("contacts.deleteGoogleContact: core not wired")
	}
	if a.db == nil {
		return fmt.Errorf("contacts.deleteGoogleContact: db not wired")
	}

	source, resourceName, err := a.googleSourceAndHrefForRecord(rec)
	if err != nil {
		return err
	}
	if !source.Writable {
		return fmt.Errorf("contacts.deleteGoogleContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: googleWriteScope, Reason: "Delete contacts from your Google account"},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("google-contacts-write")
	writer := NewGoogleContactsWriter(httpClient)

	if err := writer.DeleteContact(ctx, resourceName); err != nil {
		return fmt.Errorf("contacts.deleteGoogleContact: %w", err)
	}

	if _, err := a.db.Exec(`DELETE FROM contact_records WHERE id = ?`, rec.ID); err != nil {
		return fmt.Errorf("contacts.deleteGoogleContact: cascade delete record %s: %w", rec.ID, err)
	}
	if a.extStore != nil {
		if err := a.extStore.DeleteETag(rec.ID); err != nil {
			log.Warn().Err(err).Str("record_id", rec.ID).Msg("Failed to delete stored Google etag")
		}
	}

	log.Info().Str("record_id", rec.ID).Str("resourceName", resourceName).Msg("Google contact deleted")
	return nil
}

// listGoogleAddressbooks surfaces the user's contactGroups as pseudo-
// addressbooks for the Add Contact dialog's picker. Returns a synthetic
// "My Contacts" entry + each USER_CONTACT_GROUP. SYSTEM_CONTACT_GROUP entries
// (myContacts, all, chatBuddies, starred, friends, family, coworkers) are
// filtered — Google's create dialog doesn't surface them either; their
// semantics are "auto-derived" from other state.
//
// Read-scope only — covered by the existing first_party_uses_core_for_scopes
// manifest entry, so this call doesn't trigger consent even on a fresh
// read-only source.
func (a *API) listGoogleAddressbooks(source *carddav.Source) ([]coreapi.Addressbook, error) {
	if a.core == nil || source == nil || source.AccountID == nil || *source.AccountID == "" {
		return nil, nil
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: googleReadScope, Reason: "List contact groups"},
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	writer := NewGoogleContactsWriter(httpClient)
	groups, err := writer.ListContactGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("contacts.listGoogleAddressbooks: %w", err)
	}

	out := []coreapi.Addressbook{
		{ID: "google-mycontacts:" + source.ID, SourceID: source.ID, Name: "My Contacts"},
	}
	for _, g := range groups {
		if g.GroupType != "USER_CONTACT_GROUP" {
			continue
		}
		name := g.FormattedName
		if name == "" {
			name = g.Name
		}
		out = append(out, coreapi.Addressbook{
			ID:       "google-group:" + g.ResourceName,
			SourceID: source.ID,
			Name:     name,
			Path:     g.ResourceName,
		})
	}
	return out, nil
}

// firstAddressbookForSource returns the physical addressbook id for the
// source. Google contact-source links create exactly one addressbook row at
// link time (`storeOAuthContactsDelta` writes to it), so this is the
// destination for the new contact_records row.
//
// Returns an error when no addressbook is found — typically means the
// source was created but never synced (no record_state mirror to write to).
func (a *API) firstAddressbookForSource(sourceID string) (string, error) {
	abs, err := a.carddavStore.ListAddressbooks(sourceID)
	if err != nil {
		return "", err
	}
	for _, ab := range abs {
		if ab != nil {
			return ab.ID, nil
		}
	}
	return "", fmt.Errorf("source %s has no addressbook; run a sync first", sourceID)
}

// googleSourceAndHrefForRecord loads the carddav.Source AND the Google
// resourceName (stored as carddav_record_state.href for OAuth-synced
// records) for an existing record. Used by Update + Delete to validate the
// source is writable AND get the API target.
func (a *API) googleSourceAndHrefForRecord(rec *contact.Record) (*carddav.Source, string, error) {
	if rec == nil || rec.SourceRef == "" {
		return nil, "", fmt.Errorf("record has no addressbook reference")
	}
	source, err := a.carddavStore.GetSourceForAddressbook(rec.SourceRef)
	if err != nil {
		return nil, "", fmt.Errorf("lookup source for addressbook %s: %w", rec.SourceRef, err)
	}
	if source == nil {
		return nil, "", fmt.Errorf("no source owns addressbook %s", rec.SourceRef)
	}
	if source.AccountID == nil || *source.AccountID == "" {
		return nil, "", fmt.Errorf("source %s has no linked account", source.ID)
	}

	var href string
	err = a.db.QueryRow(`SELECT href FROM carddav_record_state WHERE record_id = ?`, rec.ID).Scan(&href)
	if err != nil {
		return nil, "", fmt.Errorf("lookup record_state for %s: %w", rec.ID, err)
	}
	if href == "" {
		return nil, "", fmt.Errorf("record %s has no remote resourceName", rec.ID)
	}
	return source, href, nil
}
