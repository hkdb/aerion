package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goImap "github.com/emersion/go-imap/v2"
	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/draft"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/pgp"
	"github.com/hkdb/aerion/internal/smime"
	"github.com/hkdb/aerion/internal/smtp"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// draftBodyPayload is used to serialize body fields for encrypted draft storage
type draftBodyPayload struct {
	BodyHTML    string            `json:"bodyHtml"`
	BodyText   string            `json:"bodyText"`
	Attachments []smtp.Attachment `json:"attachments,omitempty"`
}

// DraftResult represents the result of saving a draft
type DraftResult struct {
	Draft *draft.Draft `json:"draft"`
}

// encryptResult holds the result of encrypting a draft body for storage
type encryptResult struct {
	bodyHTML         string
	bodyText         string
	encrypted        bool
	encryptedBody    []byte
	pgpEncrypted     bool
	pgpEncryptedBody []byte
	attachmentsData  []byte
}

// syncStatusEmitter is a callback for emitting draft sync status changes to a Wails context
type syncStatusEmitter func(status draft.SyncStatus, imapUID uint32, syncError string)

// ============================================================================
// draftOps — shared draft logic used by both App and ComposerApp
// ============================================================================

// draftOps contains shared draft operation logic used by both App and ComposerApp.
// This prevents divergence between in-window and detached composer draft handling.
type draftOps struct {
	accountStore   *account.Store
	folderStore    *folder.Store
	messageStore   *message.Store
	draftStore     *draft.Store
	imapPool       *imap.Pool
	smimeSigner    *smime.Signer
	smimeEncryptor *smime.Encryptor
	smimeDecryptor *smime.Decryptor
	pgpSigner      *pgp.Signer
	pgpEncryptor   *pgp.Encryptor
	pgpDecryptor   *pgp.Decryptor
}

// getSpecialFolder looks up a special folder for an account, checking manual
// mappings first and falling back to auto-detected folder type.
func (ops *draftOps) getSpecialFolder(accountID string, folderType folder.Type) (*folder.Folder, error) {
	acc, err := ops.accountStore.Get(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}

	mappedPath := acc.GetFolderMapping(string(folderType))
	if mappedPath != "" {
		f, err := ops.folderStore.GetByPath(accountID, mappedPath)
		if err != nil {
			return nil, err
		}
		if f != nil {
			return f, nil
		}
	}

	return ops.folderStore.GetByType(accountID, folderType)
}

// resolveAttachmentContent resolves ContentBase64 to Content for all attachments,
// normalizing the representation for storage and processing.
func resolveAttachmentContent(attachments []smtp.Attachment) ([]smtp.Attachment, error) {
	resolved := make([]smtp.Attachment, len(attachments))
	for i, att := range attachments {
		content, err := att.ResolveContent()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve content for %s: %w", att.Filename, err)
		}
		resolved[i] = att
		resolved[i].Content = content
		resolved[i].ContentBase64 = "" // Clear to avoid storing both
	}
	return resolved, nil
}

// encryptDraftBody encrypts the draft body to self if encryption is enabled.
// Handles S/MIME and PGP (mutually exclusive). Falls back to unencrypted on failure.
func (ops *draftOps) encryptDraftBody(accountID, fromEmail string, msg smtp.ComposeMessage) (*encryptResult, error) {
	log := logging.WithComponent("draft")

	// Resolve ContentBase64 → Content for all attachments before processing
	if len(msg.Attachments) > 0 {
		resolved, err := resolveAttachmentContent(msg.Attachments)
		if err != nil {
			return nil, err
		}
		msg.Attachments = resolved
	}

	result := &encryptResult{
		bodyHTML: msg.HTMLBody,
		bodyText: msg.TextBody,
	}

	switch {
	case msg.EncryptMessage:
		// S/MIME encrypt-to-self
		payload := draftBodyPayload{BodyHTML: msg.HTMLBody, BodyText: msg.TextBody, Attachments: msg.Attachments}
		jsonBytes, jsonErr := json.Marshal(payload)
		if jsonErr != nil {
			return nil, fmt.Errorf("failed to serialize draft body: %w", jsonErr)
		}

		enc, encErr := ops.smimeEncryptor.EncryptBytes(accountID, fromEmail, jsonBytes)
		if encErr != nil {
			log.Warn().Err(encErr).Msg("Failed to encrypt draft body, saving unencrypted")
			break
		}
		result.encrypted = true
		result.encryptedBody = enc
		result.bodyHTML = ""
		result.bodyText = ""

	case msg.PGPEncryptMessage:
		// PGP encrypt-to-self
		payload := draftBodyPayload{BodyHTML: msg.HTMLBody, BodyText: msg.TextBody, Attachments: msg.Attachments}
		jsonBytes, jsonErr := json.Marshal(payload)
		if jsonErr != nil {
			return nil, fmt.Errorf("failed to serialize draft body: %w", jsonErr)
		}

		enc, encErr := ops.pgpEncryptor.EncryptBytes(accountID, fromEmail, jsonBytes)
		if encErr != nil {
			log.Warn().Err(encErr).Msg("Failed to PGP encrypt draft body, saving unencrypted")
			break
		}
		result.pgpEncrypted = true
		result.pgpEncryptedBody = enc
		result.bodyHTML = ""
		result.bodyText = ""
	}

	// For non-encrypted drafts, store attachments separately
	if !result.encrypted && !result.pgpEncrypted && len(msg.Attachments) > 0 {
		attJSON, attErr := json.Marshal(msg.Attachments)
		if attErr != nil {
			log.Warn().Err(attErr).Msg("Failed to serialize draft attachments")
		}
		if attErr == nil {
			result.attachmentsData = attJSON
		}
	}

	return result, nil
}

// saveDraftToDB creates or updates a draft in the local database.
// If localDraft is non-nil, updates it; otherwise creates a new draft.
func (ops *draftOps) saveDraftToDB(accountID string, localDraft *draft.Draft, msg smtp.ComposeMessage, enc *encryptResult) (*draft.Draft, error) {
	log := logging.WithComponent("draft")

	if localDraft != nil {
		// Update existing draft
		localDraft.ToList = addressListToJSON(msg.To)
		localDraft.CcList = addressListToJSON(msg.Cc)
		localDraft.BccList = addressListToJSON(msg.Bcc)
		localDraft.Subject = msg.Subject
		localDraft.BodyHTML = enc.bodyHTML
		localDraft.BodyText = enc.bodyText
		localDraft.InReplyToID = msg.InReplyTo
		localDraft.SignMessage = msg.SignMessage
		localDraft.Encrypted = enc.encrypted
		localDraft.EncryptedBody = enc.encryptedBody
		localDraft.PGPSignMessage = msg.PGPSignMessage
		localDraft.PGPEncrypted = enc.pgpEncrypted
		localDraft.PGPEncryptedBody = enc.pgpEncryptedBody
		localDraft.AttachmentsData = enc.attachmentsData
		localDraft.SyncStatus = draft.SyncStatusPending

		if err := ops.draftStore.Update(localDraft); err != nil {
			return nil, fmt.Errorf("failed to update draft: %w", err)
		}
		log.Debug().Str("draftID", localDraft.ID).Bool("encrypted", enc.encrypted).Bool("pgpEncrypted", enc.pgpEncrypted).Msg("Updated existing draft")
		return localDraft, nil
	}

	// Create new draft
	localDraft = &draft.Draft{
		AccountID:        accountID,
		ToList:           addressListToJSON(msg.To),
		CcList:           addressListToJSON(msg.Cc),
		BccList:          addressListToJSON(msg.Bcc),
		Subject:          msg.Subject,
		BodyHTML:         enc.bodyHTML,
		BodyText:         enc.bodyText,
		InReplyToID:      msg.InReplyTo,
		SignMessage:      msg.SignMessage,
		Encrypted:        enc.encrypted,
		EncryptedBody:    enc.encryptedBody,
		PGPSignMessage:   msg.PGPSignMessage,
		PGPEncrypted:     enc.pgpEncrypted,
		PGPEncryptedBody: enc.pgpEncryptedBody,
		AttachmentsData:  enc.attachmentsData,
		SyncStatus:       draft.SyncStatusPending,
	}

	if err := ops.draftStore.Create(localDraft); err != nil {
		return nil, fmt.Errorf("failed to create draft: %w", err)
	}
	log.Debug().Str("draftID", localDraft.ID).Bool("encrypted", enc.encrypted).Bool("pgpEncrypted", enc.pgpEncrypted).Msg("Created new draft")
	return localDraft, nil
}

// deleteDraftCore deletes a draft from IMAP (if synced), cleans up the message
// row, and removes the draft from the local database. Returns the drafts folder
// (non-nil if the draft was synced) so callers can emit events and trigger syncs.
func (ops *draftOps) deleteDraftCore(ctx context.Context, d *draft.Draft) (*folder.Folder, error) {
	log := logging.WithComponent("draft")

	// Re-read from DB to get latest sync state (IMAPUID may have been updated
	// by a background sync goroutine since the caller obtained this draft object)
	if fresh, err := ops.draftStore.Get(d.ID); err == nil && fresh != nil {
		d = fresh
	}

	var draftsFolder *folder.Folder
	if d.IsSynced() {
		draftsFolder, _ = ops.getSpecialFolder(d.AccountID, folder.TypeDrafts)
		if draftsFolder != nil {
			poolConn, err := ops.imapPool.GetConnection(ctx, d.AccountID)
			if err == nil {
				defer ops.imapPool.Release(poolConn)
				conn := poolConn.Client()
				if _, err := conn.SelectMailbox(ctx, draftsFolder.Path); err == nil {
					if err := conn.DeleteMessageByUID(goImap.UID(d.IMAPUID)); err != nil {
						log.Warn().Err(err).Uint32("uid", d.IMAPUID).Msg("Failed to delete draft from IMAP")
					}
				}
			}

			// Clean up the message row that syncDraftToIMAP's SyncFolder may have created.
			// Done directly because the post-delete SyncFolder may be debounced (500ms)
			// if a recent draft sync just ran.
			if err := ops.messageStore.DeleteByUID(draftsFolder.ID, d.IMAPUID); err != nil {
				log.Warn().Err(err).Uint32("uid", d.IMAPUID).Str("folderID", draftsFolder.ID).Msg("Failed to clean up draft message row")
			}
		}
	}

	// Delete from local database
	if err := ops.draftStore.Delete(d.ID); err != nil {
		return draftsFolder, fmt.Errorf("failed to delete draft: %w", err)
	}

	return draftsFolder, nil
}

// syncToIMAP syncs a draft to the IMAP server. The emitStatus callback lets each
// caller emit events to its own Wails context. Returns the drafts folder on success
// (nil on failure) so callers can perform post-append work (SyncFolder or IPC notify).
func (ops *draftOps) syncToIMAP(ctx context.Context, localDraft *draft.Draft, msg smtp.ComposeMessage, emitStatus syncStatusEmitter) *folder.Folder {
	log := logging.WithComponent("draft")

	// Find the Drafts folder for this account
	draftsFolder, err := ops.getSpecialFolder(localDraft.AccountID, folder.TypeDrafts)
	if err != nil || draftsFolder == nil {
		log.Warn().Err(err).Str("account_id", localDraft.AccountID).Msg("No drafts folder found, skipping IMAP sync")
		ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", "no drafts folder found")
		emitStatus(draft.SyncStatusFailed, 0, "no drafts folder found")
		return nil
	}

	// Get IMAP connection from pool
	poolConn, err := ops.imapPool.GetConnection(ctx, localDraft.AccountID)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get IMAP connection, will retry later")
		ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", err.Error())
		emitStatus(draft.SyncStatusFailed, 0, err.Error())
		return nil
	}
	defer ops.imapPool.Release(poolConn)

	conn := poolConn.Client()

	// Delete old IMAP draft if it exists
	if localDraft.IMAPUID > 0 && localDraft.FolderID != "" {
		if _, err := conn.SelectMailbox(ctx, draftsFolder.Path); err == nil {
			if err := conn.DeleteMessageByUID(goImap.UID(localDraft.IMAPUID)); err != nil {
				log.Warn().Err(err).Uint32("uid", localDraft.IMAPUID).Msg("Failed to delete old draft from IMAP")
			}
		}
	}

	// Build RFC822 message
	rawMsg, err := msg.ToRFC822()
	if err != nil {
		log.Error().Err(err).Msg("Failed to build RFC822 message")
		ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", err.Error())
		emitStatus(draft.SyncStatusFailed, 0, err.Error())
		return nil
	}

	// The sender's email determines which cert/key to use
	fromEmail := msg.From.Address

	// Sign then encrypt draft for IMAP sync (mirrors send flow)
	// S/MIME signing
	if localDraft.SignMessage {
		signedMsg, signErr := ops.smimeSigner.SignMessage(localDraft.AccountID, fromEmail, rawMsg)
		if signErr != nil {
			log.Warn().Err(signErr).Msg("Failed to sign draft for IMAP sync, continuing unsigned")
		}
		if signErr == nil {
			rawMsg = signedMsg
			log.Debug().Str("draftID", localDraft.ID).Msg("Draft S/MIME signed for IMAP sync")
		}
	}
	// S/MIME encryption
	if localDraft.Encrypted {
		encryptedMsg, encErr := ops.smimeEncryptor.EncryptMessageToSelf(localDraft.AccountID, fromEmail, rawMsg)
		if encErr != nil {
			log.Error().Err(encErr).Msg("Failed to encrypt draft for IMAP sync")
			ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", encErr.Error())
			emitStatus(draft.SyncStatusFailed, 0, encErr.Error())
			return nil
		}
		rawMsg = encryptedMsg
		log.Debug().Str("draftID", localDraft.ID).Msg("Draft S/MIME encrypted for IMAP sync")
	}
	// PGP signing (mutually exclusive with S/MIME)
	if !localDraft.SignMessage && localDraft.PGPSignMessage {
		signedMsg, signErr := ops.pgpSigner.SignMessage(localDraft.AccountID, fromEmail, rawMsg)
		if signErr != nil {
			log.Warn().Err(signErr).Msg("Failed to PGP sign draft for IMAP sync, continuing unsigned")
		}
		if signErr == nil {
			rawMsg = signedMsg
			log.Debug().Str("draftID", localDraft.ID).Msg("Draft PGP signed for IMAP sync")
		}
	}
	// PGP encryption (mutually exclusive with S/MIME)
	if !localDraft.Encrypted && localDraft.PGPEncrypted {
		encryptedMsg, encErr := ops.pgpEncryptor.EncryptMessageToSelf(localDraft.AccountID, fromEmail, rawMsg)
		if encErr != nil {
			log.Error().Err(encErr).Msg("Failed to PGP encrypt draft for IMAP sync")
			ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", encErr.Error())
			emitStatus(draft.SyncStatusFailed, 0, encErr.Error())
			return nil
		}
		rawMsg = encryptedMsg
		log.Debug().Str("draftID", localDraft.ID).Msg("Draft PGP encrypted for IMAP sync")
	}

	// Re-check if draft still exists (may have been deleted by concurrent DeleteDraft)
	if d, _ := ops.draftStore.Get(localDraft.ID); d == nil {
		log.Debug().Str("draftID", localDraft.ID).Msg("Draft deleted during sync, skipping IMAP append")
		return nil
	}

	// Check if cancelled before the irreversible IMAP append
	if ctx.Err() != nil {
		log.Debug().Str("draftID", localDraft.ID).Msg("Draft sync cancelled before IMAP append")
		return nil
	}

	// Append to IMAP Drafts folder with \Draft and \Seen flags
	flags := []goImap.Flag{goImap.FlagDraft, goImap.FlagSeen}
	uid, err := conn.AppendMessage(draftsFolder.Path, flags, time.Now(), rawMsg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to append draft to IMAP")
		ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusFailed, 0, "", err.Error())
		emitStatus(draft.SyncStatusFailed, 0, err.Error())
		return nil
	}

	// Update local draft with sync status
	if err := ops.draftStore.UpdateSyncStatus(localDraft.ID, draft.SyncStatusSynced, uint32(uid), draftsFolder.ID, ""); err != nil {
		log.Warn().Err(err).Msg("Failed to update draft sync status")
	}
	emitStatus(draft.SyncStatusSynced, uint32(uid), "")

	log.Info().
		Str("id", localDraft.ID).
		Uint32("imap_uid", uint32(uid)).
		Msg("Draft synced to IMAP")

	return draftsFolder
}

// toComposeMessage converts a draft to a ComposeMessage, decrypting the body
// (S/MIME or PGP) if the draft is encrypted.
func (ops *draftOps) toComposeMessage(d *draft.Draft) *smtp.ComposeMessage {
	bodyHTML := d.BodyHTML
	bodyText := d.BodyText
	encryptMessage := false
	pgpEncryptMessage := false
	var attachments []smtp.Attachment

	// Determine the identity email for decryption
	identityEmail := ops.getIdentityEmail(d)

	// S/MIME encrypted draft
	if d.Encrypted && len(d.EncryptedBody) > 0 {
		decrypted, decErr := ops.smimeDecryptor.DecryptBytes(d.AccountID, identityEmail, d.EncryptedBody)
		if decErr != nil {
			log := logging.WithComponent("draft")
			log.Error().Err(decErr).Str("draftID", d.ID).Msg("Failed to decrypt S/MIME draft body")
		}
		if decErr == nil {
			var payload draftBodyPayload
			unmarshalErr := json.Unmarshal(decrypted, &payload)
			if unmarshalErr != nil {
				log := logging.WithComponent("draft")
				log.Error().Err(unmarshalErr).Str("draftID", d.ID).Msg("Failed to unmarshal decrypted S/MIME draft body")
			}
			if unmarshalErr == nil {
				bodyHTML = payload.BodyHTML
				bodyText = payload.BodyText
				attachments = payload.Attachments
				encryptMessage = true
			}
		}
	}

	// PGP encrypted draft (mutually exclusive with S/MIME)
	if !d.Encrypted && d.PGPEncrypted && len(d.PGPEncryptedBody) > 0 {
		decrypted, decErr := ops.pgpDecryptor.DecryptBytes(d.AccountID, identityEmail, d.PGPEncryptedBody)
		if decErr != nil {
			log := logging.WithComponent("draft")
			log.Error().Err(decErr).Str("draftID", d.ID).Msg("Failed to decrypt PGP draft body")
		}
		if decErr == nil {
			var payload draftBodyPayload
			unmarshalErr := json.Unmarshal(decrypted, &payload)
			if unmarshalErr != nil {
				log := logging.WithComponent("draft")
				log.Error().Err(unmarshalErr).Str("draftID", d.ID).Msg("Failed to unmarshal decrypted PGP draft body")
			}
			if unmarshalErr == nil {
				bodyHTML = payload.BodyHTML
				bodyText = payload.BodyText
				attachments = payload.Attachments
				pgpEncryptMessage = true
			}
		}
	}

	// For non-encrypted drafts, restore attachments from separate column
	if !d.Encrypted && !d.PGPEncrypted && len(d.AttachmentsData) > 0 {
		if err := json.Unmarshal(d.AttachmentsData, &attachments); err != nil {
			log := logging.WithComponent("draft")
			log.Warn().Err(err).Str("draftID", d.ID).Msg("Failed to unmarshal draft attachments")
		}
	}

	return &smtp.ComposeMessage{
		To:                parseAddressList(d.ToList),
		Cc:                parseAddressList(d.CcList),
		Bcc:               parseAddressList(d.BccList),
		Subject:           d.Subject,
		HTMLBody:          bodyHTML,
		TextBody:          bodyText,
		Attachments:       attachments,
		InReplyTo:         d.InReplyToID,
		SignMessage:       d.SignMessage,
		EncryptMessage:    encryptMessage,
		PGPSignMessage:    d.PGPSignMessage,
		PGPEncryptMessage: pgpEncryptMessage,
	}
}

// getIdentityEmail returns the email address for the draft's identity.
// Falls back to the account email if the identity cannot be resolved.
func (ops *draftOps) getIdentityEmail(d *draft.Draft) string {
	if d.IdentityID != "" {
		identities, err := ops.accountStore.GetIdentities(d.AccountID)
		if err == nil {
			for _, id := range identities {
				if id.ID == d.IdentityID {
					return id.Email
				}
			}
		}
	}
	// Fall back to account email
	acc, err := ops.accountStore.Get(d.AccountID)
	if err == nil && acc != nil {
		return acc.Email
	}
	return ""
}

// ============================================================================
// Draft API - Exposed to frontend via Wails bindings
// ============================================================================

// cancelDraftSync cancels any in-flight syncDraftToIMAP goroutine for the given draft
// and waits for it to finish. This prevents the race where DeleteDraft runs while
// a background goroutine is still uploading the draft to IMAP.
func (a *App) cancelDraftSync(draftID string) {
	a.syncMu.Lock()
	cancel, hasCancel := a.draftSyncContexts[draftID]
	done, hasDone := a.draftSyncDone[draftID]
	a.syncMu.Unlock()

	if !hasCancel {
		return
	}
	cancel()
	if !hasDone {
		return
	}
	<-done
}

// SaveDraft saves or updates a draft email to the local database and syncs to IMAP.
// If existingDraftID is provided and exists, updates that draft; otherwise creates a new one.
func (a *App) SaveDraft(accountID string, msg smtp.ComposeMessage, existingDraftID string) (*DraftResult, error) {
	log := logging.WithComponent("app")

	log.Debug().
		Str("accountID", accountID).
		Str("existingDraftID", existingDraftID).
		Str("subject", msg.Subject).
		Msg("SaveDraft called")

	var localDraft *draft.Draft

	// Try to load existing draft if ID provided
	if existingDraftID != "" {
		existing, err := a.draftStore.Get(existingDraftID)
		if err != nil {
			log.Warn().Err(err).Str("draftID", existingDraftID).Msg("Failed to load existing draft")
		}
		if err == nil && existing != nil {
			localDraft = existing
			log.Debug().Str("draftID", existingDraftID).Msg("Loaded existing draft for update")
		}
	}

	enc, err := a.draftOps.encryptDraftBody(accountID, msg.From.Address, msg)
	if err != nil {
		return nil, err
	}

	localDraft, err = a.draftOps.saveDraftToDB(accountID, localDraft, msg, enc)
	if err != nil {
		return nil, err
	}

	// Cancel any previous in-flight sync for this draft before starting a new one
	a.cancelDraftSync(localDraft.ID)

	// Sync to IMAP in background with cancellation support
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	a.syncMu.Lock()
	a.draftSyncContexts[localDraft.ID] = cancel
	a.draftSyncDone[localDraft.ID] = done
	a.syncMu.Unlock()

	go func() {
		defer recoverPanic("app.draft", "sync draft to IMAP")
		defer close(done)
		defer func() {
			a.syncMu.Lock()
			if cur, exists := a.draftSyncDone[localDraft.ID]; exists && cur == done {
				delete(a.draftSyncContexts, localDraft.ID)
				delete(a.draftSyncDone, localDraft.ID)
			}
			a.syncMu.Unlock()
		}()
		a.syncDraftToIMAP(ctx, localDraft, msg)
	}()

	log.Info().Str("draftID", localDraft.ID).Bool("encrypted", enc.encrypted).Msg("Draft saved locally, syncing to IMAP")
	return &DraftResult{Draft: localDraft}, nil
}

// syncDraftToIMAP syncs a draft to the IMAP server
func (a *App) syncDraftToIMAP(ctx context.Context, localDraft *draft.Draft, msg smtp.ComposeMessage) {
	log := logging.WithComponent("app")

	emitStatus := func(status draft.SyncStatus, imapUID uint32, syncError string) {
		wailsRuntime.EventsEmit(a.ctx, "draft:syncStatusChanged", map[string]interface{}{
			"draftId":    localDraft.ID,
			"syncStatus": status,
			"imapUid":    imapUID,
			"error":      syncError,
		})
	}

	draftsFolder := a.draftOps.syncToIMAP(ctx, localDraft, msg, emitStatus)
	if draftsFolder == nil {
		return
	}

	// Sync the Drafts folder so the main window's message list shows the updated draft
	// Do this after IMAP upload completes to ensure the draft is available
	if ctx.Err() != nil {
		log.Debug().Str("draftID", localDraft.ID).Msg("Draft sync cancelled, skipping folder sync")
		return
	}
	if err := a.SyncFolder(localDraft.AccountID, draftsFolder.ID); err != nil {
		log.Warn().Err(err).Str("folderID", draftsFolder.ID).Msg("Failed to sync Drafts folder after draft save")
		return
	}
	log.Debug().Str("folderID", draftsFolder.ID).Msg("Synced Drafts folder after draft save")
}

// SyncPendingDrafts syncs any pending drafts for an account
func (a *App) SyncPendingDrafts(accountID string) error {
	log := logging.WithComponent("app")

	pending, err := a.draftStore.ListPendingSync(accountID)
	if err != nil {
		return fmt.Errorf("failed to list pending drafts: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	log.Info().Int("count", len(pending)).Str("accountID", accountID).Msg("Syncing pending drafts")

	for _, d := range pending {
		msg := a.draftToComposeMessage(d)
		a.syncDraftToIMAP(a.ctx, d, *msg)
	}

	return nil
}

// syncAllPendingDrafts syncs pending drafts for all accounts
func (a *App) syncAllPendingDrafts() {
	defer recoverPanic("app.draft", "sync pending drafts")
	log := logging.WithComponent("app")

	accounts, err := a.accountStore.List()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list accounts for draft sync")
		return
	}

	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}
		if err := a.SyncPendingDrafts(acc.ID); err != nil {
			log.Warn().Err(err).Str("accountID", acc.ID).Msg("Failed to sync pending drafts")
		}
	}
}

// draftToComposeMessage converts a draft to a ComposeMessage.
// If the draft is encrypted (S/MIME or PGP), decrypts the body first.
func (a *App) draftToComposeMessage(d *draft.Draft) *smtp.ComposeMessage {
	return a.draftOps.toComposeMessage(d)
}

// getDraftIdentityEmail returns the email address for the draft's identity.
// Falls back to the account email if the identity cannot be resolved.
func (a *App) getDraftIdentityEmail(d *draft.Draft) string {
	return a.draftOps.getIdentityEmail(d)
}

// DeleteDraft deletes a draft from local DB and IMAP
func (a *App) DeleteDraft(draftID string) error {
	log := logging.WithComponent("app")

	// Cancel any in-flight IMAP sync goroutine and wait for it to finish.
	// This ensures the goroutine can't upload the draft after we delete it.
	a.cancelDraftSync(draftID)

	// Get the draft to find IMAP UID (re-read after cancel to get latest state)
	d, err := a.draftStore.Get(draftID)
	if err != nil {
		return fmt.Errorf("failed to get draft: %w", err)
	}
	if d == nil {
		return nil // Already deleted
	}

	draftsFolder, err := a.draftOps.deleteDraftCore(a.ctx, d)
	if err != nil {
		return err
	}

	// Notify frontend to refresh the message list for this folder
	if draftsFolder != nil {
		wailsRuntime.EventsEmit(a.ctx, "messages:updated", map[string]interface{}{
			"accountId": d.AccountID,
			"folderId":  draftsFolder.ID,
		})
	}

	// Emit event
	wailsRuntime.EventsEmit(a.ctx, "draft:deleted", map[string]interface{}{
		"draftId": draftID,
	})

	// Sync the Drafts folder so the message list and sidebar counts update
	if draftsFolder != nil {
		accountID := d.AccountID
		folderID := draftsFolder.ID
		go func() {
			defer recoverPanic("app.draft", "sync drafts folder after delete")
			if err := a.SyncFolder(accountID, folderID); err != nil {
				log.Warn().Err(err).Str("folderID", folderID).Msg("Failed to sync Drafts folder after draft delete")
			}
		}()
	}

	log.Info().Str("draftID", draftID).Msg("Draft deleted")
	return nil
}

// GetDraft returns a draft by ID as a ComposeMessage (for editing in composer)
// The ID can be either a draft ID or a message ID (from the Drafts folder)
func (a *App) GetDraft(id string) (*smtp.ComposeMessage, error) {
	log := logging.WithComponent("app")

	// First, try to get it as a draft ID
	d, err := a.draftStore.Get(id)
	if err != nil {
		return nil, err
	}
	if d != nil {
		log.Debug().Str("draftID", id).Msg("Found draft by draft ID")
		return a.draftToComposeMessage(d), nil
	}

	// Not found as draft ID - try as message ID
	// Get the message to find its IMAP UID and folder
	msg, err := a.messageStore.Get(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, nil
	}

	// Look up draft by IMAP UID and folder
	d, err = a.draftStore.GetByIMAPUID(msg.FolderID, msg.UID)
	if err != nil {
		return nil, err
	}
	if d != nil {
		log.Debug().Str("messageID", id).Str("draftID", d.ID).Msg("Found draft by message IMAP UID")
		return a.draftToComposeMessage(d), nil
	}

	// No draft found - this might be a draft that was created outside Aerion
	// (e.g., from webmail). Build a ComposeMessage from the message itself.
	log.Debug().Str("messageID", id).Msg("No local draft found, building from message")
	return a.messageToComposeMessage(msg), nil
}

// messageToComposeMessage converts a message (from Drafts folder) to a ComposeMessage
func (a *App) messageToComposeMessage(msg *message.Message) *smtp.ComposeMessage {
	return &smtp.ComposeMessage{
		To:        parseAddressList(msg.ToList),
		Cc:        parseAddressList(msg.CcList),
		Bcc:       parseAddressList(msg.BccList),
		Subject:   msg.Subject,
		HTMLBody:  msg.BodyHTML,
		TextBody:  msg.BodyText,
		InReplyTo: msg.InReplyTo,
	}
}

// ListDrafts returns all drafts for an account
func (a *App) ListDrafts(accountID string) ([]*draft.Draft, error) {
	return a.draftStore.ListByAccount(accountID)
}
