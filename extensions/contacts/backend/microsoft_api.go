package backend

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/contact"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/logging"
)

// microsoft_api.go: Phase 2b.3 Track C. Fills the createMicrosoftContact /
// updateMicrosoftContact / deleteMicrosoftContact / listMicrosoftAddressbooks
// shells that Track A scaffolded. Mirrors google_api.go's shape — the per-
// provider files are intentionally parallel so a future maintainer can diff
// the two paths and spot the provider-specific deltas.
//
// Microsoft differs from Google in a few places that show up here:
//   - No etag enforcement → no GET-to-refresh-before-PATCH ceremony.
//   - No conflict path (Graph contacts are last-writer-wins for our use).
//   - Folders are real containers (not memberships), so create routes to
//     /me/contactFolders/{id}/contacts when a specific folder is picked.
//   - Read scope already covered by mail's first_party_uses_core_for_scopes
//     manifest entry, so listMicrosoftAddressbooks doesn't trigger consent.

const microsoftWriteScope = "https://graph.microsoft.com/Contacts.ReadWrite"

// microsoftReadScope is the readonly scope used by listMicrosoftAddressbooks.
// Already covered by the existing READ-side sync's grant via the manifest's
// first_party_uses_core_for_scopes, so this call goes through the broker
// without triggering consent.
const microsoftReadScope = "https://graph.microsoft.com/Contacts.Read"

// createMicrosoftContact creates a new contact in the user's Microsoft 365 /
// Outlook account via Graph + persists a mirror row locally. Filled in for
// Phase 2b.3 Track C (was a shell in Track A).
//
// Flow:
//  1. Validate source + look up auth client.
//  2. Build msContact from input email + name.
//  3. POST /me/contacts (default folder) or /me/contactFolders/{id}/contacts
//     based on input.AddressbookID.
//  4. Resolve the physical addressbook id for the source (single row created
//     at link time by the READ-side syncer) — used as source_ref so subsequent
//     reads find this contact.
//  5. Persist contact_records + carddav_record_state in one transaction.
//  6. Store the @odata.etag on the per-extension store (informational; not
//     enforced on subsequent updates).
func (a *API) createMicrosoftContact(input coreapi.ContactCreateInput, email string, source *carddav.Source) (string, error) {
	if a.core == nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: core not wired")
	}
	if a.db == nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: db not wired")
	}
	if a.extStore == nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: extension store not wired")
	}
	if source == nil || source.AccountID == nil || *source.AccountID == "" {
		return "", fmt.Errorf("contacts.createMicrosoftContact: microsoft source has no linked account")
	}
	if !source.Writable {
		return "", fmt.Errorf("contacts.createMicrosoftContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: microsoftWriteScope, Reason: "Create contacts in your Microsoft account"},
	})
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("microsoft-contacts-write")
	writer := NewMicrosoftContactsWriter(httpClient)

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
	contactReq := recordToMicrosoftContact(rec, log)
	folderID := parseAddressbookFolderID(input.AddressbookID)

	created, err := writer.CreateContact(ctx, folderID, contactReq)
	if err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("contacts.createMicrosoftContact: server returned no contact id")
	}

	addressbookID, err := a.firstAddressbookForSource(source.ID)
	if err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: resolve addressbook for source %s: %w", source.ID, err)
	}

	recordID := uuid.New().String()
	persisted := microsoftContactToRecord(created)
	if persisted == nil {
		persisted = rec
	}
	persisted.ID = recordID
	persisted.Source = "carddav"
	persisted.SourceRef = addressbookID

	tx, err := a.db.Begin()
	if err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := contact.UpsertRecordTx(tx, persisted); err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: upsert local record: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
		VALUES (?, ?, ?, ?, ?)
	`, recordID, addressbookID, created.ID, "", time.Now()); err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: insert record_state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("contacts.createMicrosoftContact: commit: %w", err)
	}

	// Etag stored for telemetry/future-use only; Graph doesn't enforce on
	// PATCH, so we don't gate updates on it.
	if created.ETag != "" {
		if err := a.extStore.SetETag(recordID, created.ETag); err != nil {
			log.Warn().Err(err).Str("record_id", recordID).Msg("Failed to persist initial Microsoft etag")
		}
	}

	log.Info().Str("record_id", recordID).Str("contact_id", created.ID).Msg("Microsoft contact created")
	return recordID, nil
}

// updateMicrosoftContact PATCHes the existing contact under rec.ID. Sends the
// full intended record state — Graph treats missing scalar fields as
// "unchanged" and replaces multi-value arrays wholesale, which matches
// Aerion's "patch is full state" semantics from applyContactPatchToRecord.
//
// No etag gate (Graph contacts don't enforce). Photo handling is a separate
// PATCH /photo/$value after the main update; photo-step failure is non-fatal.
func (a *API) updateMicrosoftContact(rec *contact.Record) error {
	if a.core == nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: core not wired")
	}
	if a.db == nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: db not wired")
	}
	if a.extStore == nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: extension store not wired")
	}

	source, contactID, err := a.microsoftSourceAndHrefForRecord(rec)
	if err != nil {
		return err
	}
	if !source.Writable {
		return fmt.Errorf("contacts.updateMicrosoftContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: microsoftWriteScope, Reason: "Update contacts in your Microsoft account"},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("microsoft-contacts-write")
	writer := NewMicrosoftContactsWriter(httpClient)
	contactReq := recordToMicrosoftContact(rec, log)

	updated, err := writer.UpdateContact(ctx, contactID, contactReq)
	if err != nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: %w", err)
	}

	persisted := microsoftContactToRecord(updated)
	if persisted == nil {
		persisted = rec
	}
	persisted.ID = rec.ID
	persisted.Source = "carddav"
	persisted.SourceRef = rec.SourceRef

	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := contact.UpsertRecordTx(tx, persisted); err != nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: upsert local record: %w", err)
	}
	if _, err := tx.Exec(`UPDATE carddav_record_state SET synced_at = ? WHERE record_id = ?`, time.Now(), rec.ID); err != nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: touch record_state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("contacts.updateMicrosoftContact: commit: %w", err)
	}

	if updated.ETag != "" {
		if err := a.extStore.SetETag(rec.ID, updated.ETag); err != nil {
			log.Warn().Err(err).Str("record_id", rec.ID).Msg("Failed to refresh stored Microsoft etag after update")
		}
	}

	if rec.PhotoData != "" {
		photoBytes, derr := base64.StdEncoding.DecodeString(rec.PhotoData)
		if derr != nil {
			log.Warn().Err(derr).Msg("Failed to decode photo bytes; skipping photo update")
		}
		if derr == nil {
			if perr := writer.UpdatePhoto(ctx, contactID, photoBytes); perr != nil {
				log.Warn().Err(perr).Str("contact_id", contactID).Msg("Failed to update Microsoft contact photo; contact saved without photo update")
			}
		}
	}

	log.Info().Str("record_id", rec.ID).Str("contact_id", contactID).Msg("Microsoft contact updated")
	return nil
}

// deleteMicrosoftContact removes the contact from Graph and cascades the
// local rows via the contact_records FK. extStore.DeleteETag drops the
// stored version stamp.
func (a *API) deleteMicrosoftContact(rec *contact.Record) error {
	if a.core == nil {
		return fmt.Errorf("contacts.deleteMicrosoftContact: core not wired")
	}
	if a.db == nil {
		return fmt.Errorf("contacts.deleteMicrosoftContact: db not wired")
	}

	source, contactID, err := a.microsoftSourceAndHrefForRecord(rec)
	if err != nil {
		return err
	}
	if !source.Writable {
		return fmt.Errorf("contacts.deleteMicrosoftContact: source is not writable; enable write access")
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: microsoftWriteScope, Reason: "Delete contacts from your Microsoft account"},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	log := logging.WithComponent("microsoft-contacts-write")
	writer := NewMicrosoftContactsWriter(httpClient)

	if err := writer.DeleteContact(ctx, contactID); err != nil {
		return fmt.Errorf("contacts.deleteMicrosoftContact: %w", err)
	}

	if _, err := a.db.Exec(`DELETE FROM contact_records WHERE id = ?`, rec.ID); err != nil {
		return fmt.Errorf("contacts.deleteMicrosoftContact: cascade delete record %s: %w", rec.ID, err)
	}
	if a.extStore != nil {
		if err := a.extStore.DeleteETag(rec.ID); err != nil {
			log.Warn().Err(err).Str("record_id", rec.ID).Msg("Failed to delete stored Microsoft etag")
		}
	}

	log.Info().Str("record_id", rec.ID).Str("contact_id", contactID).Msg("Microsoft contact deleted")
	return nil
}

// listMicrosoftAddressbooks surfaces the user's contactFolders as pseudo-
// addressbooks for the Add Contact dialog's picker. Returns a synthetic
// "Default Contacts" entry (the unfoldered /me/contacts endpoint) + each
// user-created contactFolder.
//
// Read-scope only — covered by the existing first_party_uses_core_for_scopes
// manifest entry, so this call doesn't trigger consent even on a fresh
// read-only source.
func (a *API) listMicrosoftAddressbooks(source *carddav.Source) ([]coreapi.Addressbook, error) {
	if a.core == nil || source == nil || source.AccountID == nil || *source.AccountID == "" {
		return nil, nil
	}

	httpClient, err := a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{
		{Resource: microsoftReadScope, Reason: "List contact folders"},
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), googleCallTimeout)
	defer cancel()

	writer := NewMicrosoftContactsWriter(httpClient)
	folders, err := writer.ListContactFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("contacts.listMicrosoftAddressbooks: %w", err)
	}

	out := []coreapi.Addressbook{
		{ID: "ms-default:" + source.ID, SourceID: source.ID, Name: "Default Contacts"},
	}
	for _, f := range folders {
		if f.ID == "" {
			continue
		}
		name := f.DisplayName
		if name == "" {
			name = "Folder"
		}
		out = append(out, coreapi.Addressbook{
			ID:       "ms-folder:" + f.ID,
			SourceID: source.ID,
			Name:     name,
			Path:     f.ID,
		})
	}
	return out, nil
}

// microsoftSourceAndHrefForRecord loads the carddav.Source AND the Graph
// contact id (stored as carddav_record_state.href for OAuth-synced records)
// for an existing record. Used by Update + Delete to validate the source is
// writable AND get the API target. Mirrors googleSourceAndHrefForRecord.
func (a *API) microsoftSourceAndHrefForRecord(rec *contact.Record) (*carddav.Source, string, error) {
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
		return nil, "", fmt.Errorf("record %s has no remote contact id", rec.ID)
	}
	return source, href, nil
}
