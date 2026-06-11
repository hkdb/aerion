package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/message"
)

// SyncMessages synchronizes messages for a folder with incremental sync support.
// syncPeriodDays determines how far back to sync (0 = all messages).
// Messages are fetched in two phases: headers first (fast), then bodies (background).
//
// NOTE: From the app package, prefer App.SyncFolder() over calling this directly.
// SyncFolder wraps SyncMessages with debouncing, cancellation of concurrent syncs
// on the same folder, and proper event emission (sync:progress, folder:synced).
// Calling SyncMessages directly from app/ risks race conditions when multiple
// operations trigger syncs on the same folder concurrently.
func (e *Engine) SyncMessages(ctx context.Context, accountID, folderID string, syncPeriodDays int) error {
	// Check context at start
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Get folder from store
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}
	if f == nil {
		return fmt.Errorf("folder not found: %s", folderID)
	}

	e.log.Debug().
		Str("account", accountID).
		Str("folder", f.Path).
		Int("syncPeriodDays", syncPeriodDays).
		Msg("Syncing messages (incremental)")

	// Get a connection from the pool
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	// Use closure so defer releases the current conn (which may be reassigned during
	// connection recovery in the header batch loop). A bare defer Release(conn) would
	// capture the original pointer and leak the replacement connection.
	defer func() { e.pool.Release(conn) }()

	// Select the mailbox
	mailbox, err := conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		return fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Get mailbox status for accurate unseen count (SELECT doesn't return this)
	mailboxStatus, err := conn.Client().GetMailboxStatus(ctx, f.Path)
	if err != nil {
		e.log.Warn().Err(err).Str("folder", f.Path).Msg("Failed to get mailbox status for unseen count")
		// Continue with sync, will fall back to local count
	}

	// CONDSTORE baseline for this cycle's flag-sync decision. Captured BEFORE
	// any mutation of f, so a UIDValidity reset below clears it explicitly.
	// See condstore.go (shouldUseCondStore / nextModSeq).
	prevModSeq := f.HighestModSeq
	uidValidityChanged := false

	// Check for UIDValidity change (mailbox recreated)
	if f.UIDValidity != 0 && f.UIDValidity != mailbox.UIDValidity {
		e.log.Warn().
			Str("folder", f.Path).
			Uint32("old", f.UIDValidity).
			Uint32("new", mailbox.UIDValidity).
			Msg("UIDValidity changed, full resync required")

		// Delete all local messages and resync
		if err := e.messageStore.DeleteByFolder(folderID); err != nil {
			return fmt.Errorf("failed to delete messages: %w", err)
		}
		f.UIDValidity = mailbox.UIDValidity
		// The old modseq refers to a different universe of UIDs after a
		// mailbox recreation; treat this cycle as first-sync.
		uidValidityChanged = true
		prevModSeq = 0
	}

	// Calculate sync date cutoff
	var sinceDate time.Time
	if syncPeriodDays > 0 {
		sinceDate = time.Now().AddDate(0, 0, -syncPeriodDays)
		e.log.Debug().
			Time("sinceDate", sinceDate).
			Int("syncPeriodDays", syncPeriodDays).
			Msg("Using date-based sync filter")

		// Delete local messages older than sync period
		deleted, err := e.messageStore.DeleteOlderThan(accountID, sinceDate)
		if err != nil {
			e.log.Warn().Err(err).Msg("Failed to delete old messages")
		} else if deleted > 0 {
			e.log.Info().Int("deleted", deleted).Msg("Deleted messages older than sync period")
		}
	}

	// Get local message UIDs
	localUIDs, err := e.messageStore.GetAllUIDs(folderID)
	if err != nil {
		return fmt.Errorf("failed to get local UIDs: %w", err)
	}
	localUIDSet := make(map[uint32]bool)
	for _, uid := range localUIDs {
		localUIDSet[uid] = true
	}

	// Check context before fetching UIDs
	if ctx.Err() != nil {
		e.log.Debug().Msg("Header sync cancelled before fetching UIDs")
		return ctx.Err()
	}

	// Emit "messages" phase - fetching message list from server (UID SEARCH)
	e.emitProgress(accountID, folderID, 0, 0, "messages")

	// Fetch UIDs from server (filtered by date if syncPeriodDays > 0)
	var remoteUIDs []uint32
	if syncPeriodDays > 0 {
		remoteUIDs, err = e.fetchUIDsSince(ctx, conn.Client().RawClient(), sinceDate)
	} else {
		remoteUIDs, err = e.fetchAllUIDs(ctx, conn.Client().RawClient())
	}
	if err != nil {
		e.log.Error().Err(err).Str("folder", f.Path).Msg("Failed to fetch UIDs from server - aborting sync to prevent data loss")
		return fmt.Errorf("failed to fetch UIDs: %w", err)
	}

	e.log.Debug().
		Str("folder", f.Path).
		Int("localCount", len(localUIDs)).
		Int("remoteCount", len(remoteUIDs)).
		Msg("UID comparison")

	// SAFEGUARD: If remote returns empty but we have local messages, something is wrong
	// This could be a network issue, server error, or connection problem
	// Do NOT delete local messages in this case (unless we're using date filtering)
	if len(remoteUIDs) == 0 && len(localUIDs) > 0 && syncPeriodDays == 0 {
		e.log.Warn().
			Str("folder", f.Path).
			Int("localCount", len(localUIDs)).
			Msg("Server returned 0 messages but we have local messages - skipping deletion to prevent data loss")
		// Still try to update folder metadata but don't delete anything
		now := time.Now()
		f.LastSync = &now
		if err := e.folderStore.Update(f); err != nil {
			e.log.Warn().Err(err).Msg("Failed to update folder sync state")
		}
		return nil
	}

	remoteUIDSet := make(map[uint32]bool)
	for _, uid := range remoteUIDs {
		remoteUIDSet[uid] = true
	}

	// Find new UIDs (on server but not local)
	var newUIDs []uint32
	for uid := range remoteUIDSet {
		if !localUIDSet[uid] {
			newUIDs = append(newUIDs, uid)
		}
	}

	// Find deleted UIDs (local but not on server within sync period)
	var deletedUIDs []uint32
	for uid := range localUIDSet {
		if !remoteUIDSet[uid] {
			deletedUIDs = append(deletedUIDs, uid)
		}
	}

	// SAFEGUARD: Warn if we're about to delete a large percentage of messages
	// This could indicate a problem with the sync rather than actual deletions
	if len(localUIDs) > 10 && len(deletedUIDs) > len(localUIDs)/2 && syncPeriodDays == 0 {
		e.log.Warn().
			Str("folder", f.Path).
			Int("localCount", len(localUIDs)).
			Int("deletedCount", len(deletedUIDs)).
			Msg("About to delete more than 50% of local messages - this may indicate a sync issue")
	}

	// Check if this is a Gmail account — messages hidden by Trash/Spam label
	// still exist but are invisible in other IMAP mailbox views.
	isGmail := false
	if acc, accErr := e.accountStore.Get(accountID); accErr == nil && acc != nil {
		isGmail = acc.IMAPHost == "imap.gmail.com"
	}

	// Delete removed messages
	for _, uid := range deletedUIDs {
		// For Gmail: before deleting, check if the message exists in Trash or Spam.
		// Gmail hides messages from all other IMAP views when Trash/Spam label is
		// added, so the UID disappears from this folder's server listing even though
		// the message isn't truly deleted. Skip local deletion to preserve it.
		if isGmail {
			msg, msgErr := e.messageStore.GetByUID(folderID, uid)
			if msgErr == nil && msg != nil && msg.MessageID != "" {
				inTrash, _ := e.messageStore.ExistsInFolder(msg.MessageID, string(folder.TypeTrash), accountID)
				inSpam, _ := e.messageStore.ExistsInFolder(msg.MessageID, string(folder.TypeSpam), accountID)
				if inTrash || inSpam {
					e.log.Debug().Uint32("uid", uid).Str("messageID", msg.MessageID).
						Msg("Gmail: skipping local delete — message hidden by Trash/Spam label")
					continue
				}
			}
		}

		if err := e.messageStore.DeleteByUID(folderID, uid); err != nil {
			e.log.Warn().Err(err).Uint32("uid", uid).Msg("Failed to delete message")
		}
	}

	// Sync flags for existing messages (messages that exist both locally and on server)
	var existingUIDs []uint32
	for uid := range localUIDSet {
		if remoteUIDSet[uid] {
			existingUIDs = append(existingUIDs, uid)
		}
	}

	// flagSyncOK gates whether the persisted HighestModSeq is allowed to
	// advance below. Stays true on success; flipped to false if the only
	// available flag-sync path failed. See nextModSeq in condstore.go.
	flagSyncOK := true
	if len(existingUIDs) > 0 {
		flagSyncOK = e.runFlagSync(
			ctx,
			conn.Client().RawClient(),
			folderID,
			existingUIDs,
			uidValidityChanged,
			prevModSeq,
			mailbox.HighestModSeq,
			conn.Client().SupportsCondStore(),
		)
	}

	// Fetch new messages with incremental approach (headers first)
	if len(newUIDs) > 0 {
		// Sort UIDs descending (newest first)
		sort.Slice(newUIDs, func(i, j int) bool {
			return newUIDs[i] > newUIDs[j]
		})

		e.log.Info().
			Int("count", len(newUIDs)).
			Msg("Fetching new messages (headers first)")

		// Track connection recovery attempts for header sync
		headerConnectionFailures := 0

		// Fetch headers in batches
		for i := 0; i < len(newUIDs); i += headerBatchSize {
			// Check context at start of each batch
			if ctx.Err() != nil {
				e.log.Debug().Msg("Header sync cancelled")
				return ctx.Err()
			}

			end := i + headerBatchSize
			if end > len(newUIDs) {
				end = len(newUIDs)
			}
			batch := newUIDs[i:end]

			// Emit progress
			e.emitProgress(accountID, folderID, i, len(newUIDs), "headers")

			// Fetch headers for this batch with retry on connection error
			batchRetries := 0
			for {
				err := e.fetchMessageHeaders(ctx, conn.Client().RawClient(), accountID, folderID, batch)
				if err == nil {
					break // Success
				}

				// Check if this was a cancellation
				if ctx.Err() != nil {
					e.log.Debug().Msg("Header sync cancelled during batch fetch")
					return ctx.Err()
				}

				// Check if this is a connection error
				if imapPkg.IsConnectionError(err) {
					headerConnectionFailures++
					batchRetries++

					// Check if we've exhausted connection recovery attempts
					if headerConnectionFailures > maxConnectionRetries {
						e.log.Error().
							Int("connectionFailures", headerConnectionFailures).
							Msg("Header sync aborted - connection recovery failed")
						return fmt.Errorf("header sync connection recovery failed after %d attempts", headerConnectionFailures)
					}

					e.log.Debug().
						Err(err).
						Int("attempt", headerConnectionFailures).
						Int("batchRetry", batchRetries).
						Msg("Connection error during header fetch, attempting recovery")

					// Discard dead connection and get a new one
					e.pool.Discard(conn)

					conn, err = e.pool.GetConnection(ctx, accountID)
					if err != nil {
						return fmt.Errorf("failed to get new connection during header sync: %w", err)
					}

					// Re-select mailbox on new connection
					_, err = conn.Client().SelectMailbox(ctx, f.Path)
					if err != nil {
						e.pool.Release(conn)
						return fmt.Errorf("failed to select mailbox on new connection: %w", err)
					}

					e.log.Debug().Msg("Connection recovered for header sync")
					// Retry this batch with the new connection
					continue
				}

				// Non-connection error - log and continue to next batch
				e.log.Warn().Err(err).Int("batch", i/headerBatchSize).Msg("Failed to fetch header batch")
				break
			}
		}

		// Emit final progress for headers
		e.emitProgress(accountID, folderID, len(newUIDs), len(newUIDs), "headers")
	} else {
		// No new messages - emit 1/1 so frontend shows 100% complete, not 0%
		// (0/0 would result in 0% which looks like it's stuck)
		e.emitProgress(accountID, folderID, 1, 1, "headers")
	}

	// Update sync state
	now := time.Now()
	f.UIDValidity = mailbox.UIDValidity
	f.UIDNext = mailbox.UIDNext
	// Pin modseq when flag sync didn't fully succeed — advancing the
	// baseline after a partial failure silently skips whatever the failed
	// cycle missed. See nextModSeq in condstore.go.
	f.HighestModSeq = nextModSeq(flagSyncOK, mailbox.HighestModSeq, prevModSeq)
	f.TotalCount = int(mailbox.Messages)
	f.LastSync = &now

	// Use IMAP server's authoritative unread count if available
	if mailboxStatus != nil {
		f.UnreadCount = int(mailboxStatus.Unseen)
	} else {
		// Fall back to counting local messages if status call failed
		unreadCount, err := e.messageStore.CountUnreadByFolder(folderID)
		if err == nil {
			f.UnreadCount = unreadCount
		}
	}

	if err := e.folderStore.Update(f); err != nil {
		e.log.Warn().Err(err).Msg("Failed to update folder sync state")
	}

	e.log.Info().
		Str("folder", f.Path).
		Int("new", len(newUIDs)).
		Int("deleted", len(deletedUIDs)).
		Msg("Message sync complete (headers)")

	return nil
}

// syncMessageFlags fetches and updates flags for existing messages from the IMAP server.
// This ensures local message flags stay in sync with server changes (e.g., webmail).
func (e *Engine) syncMessageFlags(ctx context.Context, client *imapclient.Client, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}

	// Fetch flags in batches to avoid overwhelming the server
	const flagBatchSize = 500
	for i := 0; i < len(uids); i += flagBatchSize {
		// Check for cancellation
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := i + flagBatchSize
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[i:end]

		// Convert to imap.UIDSet
		uidSet := imap.UIDSet{}
		for _, uid := range batch {
			uidSet.AddNum(imap.UID(uid))
		}

		// Fetch only flags for these UIDs
		fetchOptions := &imap.FetchOptions{
			Flags: true,
		}

		fetchCmd := client.Fetch(uidSet, fetchOptions)

		// Collect all flag updates for batch DB update
		var flagUpdates []message.FlagUpdate

		for {
			msg := fetchCmd.Next()
			if msg == nil {
				break
			}

			// Collect the fetch data
			var fetchedUID uint32
			var isRead, isStarred, isAnswered, isForwarded, isDraft, isDeleted bool

			for {
				item := msg.Next()
				if item == nil {
					break
				}

				switch data := item.(type) {
				case imapclient.FetchItemDataUID:
					fetchedUID = uint32(data.UID)
				case imapclient.FetchItemDataFlags:
					for _, flag := range data.Flags {
						switch flag {
						case imap.FlagSeen:
							isRead = true
						case imap.FlagFlagged:
							isStarred = true
						case imap.FlagAnswered:
							isAnswered = true
						case imap.FlagDraft:
							isDraft = true
						case imap.FlagDeleted:
							isDeleted = true
						case "$Forwarded", "\\Forwarded":
							isForwarded = true
						}
					}
				}
			}

			// Collect flag update for batch processing
			if fetchedUID > 0 {
				flagUpdates = append(flagUpdates, message.FlagUpdate{
					UID:         fetchedUID,
					IsRead:      isRead,
					IsStarred:   isStarred,
					IsAnswered:  isAnswered,
					IsForwarded: isForwarded,
					IsDraft:     isDraft,
					IsDeleted:   isDeleted,
				})
			}
		}

		if err := fetchCmd.Close(); err != nil {
			return fmt.Errorf("failed to fetch flags: %w", err)
		}

		// Batch update all flags in a single transaction
		if len(flagUpdates) > 0 {
			if err := e.messageStore.UpdateFlagsByUIDBatch(folderID, flagUpdates); err != nil {
				e.log.Warn().Err(err).Int("count", len(flagUpdates)).Msg("Failed to batch update message flags")
			}
		}
	}

	e.log.Debug().Int("count", len(uids)).Msg("Synced message flags")
	return nil
}

// fetchUIDsSince fetches UIDs of messages since the given date.
// Uses a goroutine to allow context cancellation since Wait() blocks indefinitely.
func (e *Engine) fetchUIDsSince(ctx context.Context, client *imapclient.Client, since time.Time) ([]uint32, error) {
	e.log.Debug().Time("since", since).Msg("Fetching UIDs since date")

	// UID Search for messages since the given date
	searchCmd := client.UIDSearch(&imap.SearchCriteria{
		Since:   since,
		NotFlag: []imap.Flag{imap.FlagDeleted},
	}, nil)

	// Run Wait() in a goroutine to allow context cancellation
	type searchResult struct {
		data *imap.SearchData
		err  error
	}
	resultCh := make(chan searchResult, 1)
	go func() {
		data, err := searchCmd.Wait()
		resultCh <- searchResult{data, err}
	}()

	// Wait for either result or context cancellation
	select {
	case <-ctx.Done():
		e.log.Debug().Msg("UID search (since) cancelled by context")
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			e.log.Error().Err(result.err).Msg("UID search (since) command failed")
			return nil, fmt.Errorf("UID search failed: %w", result.err)
		}

		var uids []uint32
		for _, uid := range result.data.AllUIDs() {
			uids = append(uids, uint32(uid))
		}

		e.log.Debug().Int("count", len(uids)).Msg("Fetched UIDs since date")
		return uids, nil
	}
}

// fetchAllUIDs fetches all UIDs from the currently selected mailbox.
// Uses a goroutine to allow context cancellation since Wait() blocks indefinitely.
func (e *Engine) fetchAllUIDs(ctx context.Context, client *imapclient.Client) ([]uint32, error) {
	e.log.Debug().Msg("Fetching all UIDs from server")

	// UID Search for all messages (must use UIDSearch to get UIDs, not sequence numbers)
	searchCmd := client.UIDSearch(&imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagDeleted},
	}, nil)

	// Run Wait() in a goroutine to allow context cancellation
	type searchResult struct {
		data *imap.SearchData
		err  error
	}
	resultCh := make(chan searchResult, 1)
	go func() {
		data, err := searchCmd.Wait()
		resultCh <- searchResult{data, err}
	}()

	// Wait for either result or context cancellation
	select {
	case <-ctx.Done():
		e.log.Debug().Msg("UID search cancelled by context")
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			e.log.Error().Err(result.err).Msg("UID search command failed")
			return nil, fmt.Errorf("UID search failed: %w", result.err)
		}

		var uids []uint32
		for _, uid := range result.data.AllUIDs() {
			uids = append(uids, uint32(uid))
		}

		return uids, nil
	}
}

// fetchMessageHeaders fetches only headers (envelope, flags) for the given UIDs.
// Messages are saved with BodyFetched=false, bodies to be fetched later.
func (e *Engine) fetchMessageHeaders(ctx context.Context, client *imapclient.Client, accountID, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}

	e.log.Debug().Int("count", len(uids)).Msg("Fetching message headers")

	// Convert to imap.UIDSet
	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(imap.UID(uid))
	}

	// Fetch only envelope, flags, size, internal date, and HEADER (not full body)
	fetchOptions := &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		RFC822Size:   true,
		InternalDate: true,
		UID:          true,
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierHeader, // Only headers, not body
				Peek:      true,
			},
		},
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)

	// Stream messages one at a time instead of blocking on Collect()
	// This allows cancellation between messages and prevents indefinite blocking
	var savedMessages []*message.Message
	fetchedCount := 0

	for {
		// Check for cancellation between messages
		if ctx.Err() != nil {
			fetchCmd.Close()
			e.log.Warn().
				Int("fetched", fetchedCount).
				Int("requested", len(uids)).
				Msg("Header fetch cancelled, saved partial results")
			// Don't return error - we saved what we got
			break
		}

		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		// Collect all data items for this message
		var fetchedUID imap.UID
		var envelope *imap.Envelope
		var flags []imap.Flag
		var rfc822Size int64
		var headerBytes []byte

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			switch data := item.(type) {
			case imapclient.FetchItemDataUID:
				fetchedUID = data.UID
			case imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			case imapclient.FetchItemDataFlags:
				flags = data.Flags
			case imapclient.FetchItemDataRFC822Size:
				rfc822Size = data.Size
			case imapclient.FetchItemDataBodySection:
				// Read header bytes from literal reader
				if data.Literal != nil {
					var err error
					headerBytes, err = io.ReadAll(data.Literal)
					if err != nil {
						e.log.Warn().Err(err).Uint32("uid", uint32(fetchedUID)).Msg("Failed to read header literal")
					}
				}
			}
		}

		if fetchedUID == 0 {
			e.log.Warn().Msg("Received message without UID in header fetch")
			continue
		}

		// Build message from streamed data
		m := &message.Message{
			AccountID:   accountID,
			FolderID:    folderID,
			UID:         uint32(fetchedUID),
			ReceivedAt:  time.Now().UTC(),
			BodyFetched: false, // Headers only, no body yet
			Size:        int(rfc822Size),
		}

		// Parse envelope using shared helper
		applyEnvelopeToMessage(m, envelope)

		// Extract References and read receipt header from header bytes
		var references []string
		if len(headerBytes) > 0 {
			references = e.extractReferences(headerBytes)
			m.ReadReceiptTo = e.extractDispositionNotificationTo(headerBytes)
		}

		// Store references as JSON array
		if len(references) > 0 {
			refsJSON, _ := json.Marshal(references)
			m.References = string(refsJSON)
		}

		// Parse flags using shared helper
		applyFlagsToMessage(m, flags)

		// Save to store immediately (don't wait for all messages)
		if err := e.messageStore.Upsert(m); err != nil {
			e.log.Warn().Err(err).Uint32("uid", m.UID).Msg("Failed to save message header")
			continue
		}
		savedMessages = append(savedMessages, m)
		fetchedCount++
	}

	if err := fetchCmd.Close(); err != nil {
		e.log.Warn().Err(err).
			Int("fetched", fetchedCount).
			Int("requested", len(uids)).
			Msg("Header fetch close error, continuing with saved messages")

		// Recovery path for malformed-envelope servers (e.g., Mailfence emitting a bare
		// space where "" or NIL should be for an empty subject — see issue #209). Only
		// triggers on this specific parse error signature; everything else falls through
		// to the existing "log + continue with partial results" behavior.
		if strings.Contains(err.Error(), `expected string, got " "`) && fetchedCount < len(uids) {
			savedUIDs := make(map[uint32]bool, len(savedMessages))
			for _, m := range savedMessages {
				savedUIDs[m.UID] = true
			}
			missing := make([]uint32, 0, len(uids)-fetchedCount)
			for _, u := range uids {
				if !savedUIDs[u] {
					missing = append(missing, u)
				}
			}
			recovered, recErr := e.recoverFailedHeaderBatch(ctx, client, accountID, folderID, missing)
			if recErr != nil {
				e.log.Warn().Err(recErr).Msg("Header recovery returned error")
			}
			savedMessages = append(savedMessages, recovered...)
			fetchedCount += len(recovered)
		}
	}

	e.log.Debug().
		Int("fetched", fetchedCount).
		Int("requested", len(uids)).
		Msg("Header fetch complete")

	// Compute thread IDs after saving and reconcile related messages
	for _, m := range savedMessages {
		threadID := e.computeThreadID(accountID, m)
		if threadID != "" && threadID != m.ThreadID {
			m.ThreadID = threadID
			if err := e.messageStore.UpdateThreadID(m.ID, threadID); err != nil {
				e.log.Warn().Err(err).Str("messageId", m.ID).Msg("Failed to update thread ID")
			}
		}

		// Reconcile threads: link this message with related messages
		// This handles cases where replies were synced before the original message
		if err := e.messageStore.ReconcileThreadsForNewMessage(accountID, m.ID, m.MessageID, m.ThreadID, m.InReplyTo); err != nil {
			e.log.Warn().Err(err).Str("messageId", m.ID).Msg("Failed to reconcile threads")
		}
	}

	return nil
}

/*
// parseMessageHeaderBuffer parses an IMAP FetchMessageBuffer containing only headers.
//
// UNUSED: This function is not called anywhere. Header parsing is done inline in
// fetchMessageHeaders using streaming (Next() loop) instead of FetchMessageBuffer.
func (e *Engine) parseMessageHeaderBuffer(accountID, folderID string, buf *imapclient.FetchMessageBuffer) (*message.Message, error) {
	m := &message.Message{
		AccountID:   accountID,
		FolderID:    folderID,
		UID:         uint32(buf.UID),
		ReceivedAt:  time.Now().UTC(),
		BodyFetched: false, // Headers only, no body yet
	}

	// Parse envelope
	applyEnvelopeToMessage(m, buf.Envelope)

	// Extract References and read receipt header from headers
	var references []string
	for _, section := range buf.BodySection {
		if len(section.Bytes) > 0 {
			references = e.extractReferences(section.Bytes)
			m.ReadReceiptTo = e.extractDispositionNotificationTo(section.Bytes)

			// Check for attachments from Content-Type header
			// This is a heuristic - we'll confirm when fetching body
			headerStr := string(section.Bytes)
			if strings.Contains(strings.ToLower(headerStr), "multipart/mixed") ||
				strings.Contains(strings.ToLower(headerStr), "application/") {
				m.HasAttachments = true
			}
			break
		}
	}

	// Store references as JSON array
	if len(references) > 0 {
		refsJSON, _ := json.Marshal(references)
		m.References = string(refsJSON)
	}

	// Parse flags
	applyFlagsToMessage(m, buf.Flags)

	// Size
	m.Size = int(buf.RFC822Size)

	// No snippet yet - will be generated when body is fetched

	return m, nil
}
*/
