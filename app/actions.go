package app

import (
	"context"
	"fmt"
	"time"

	goImap "github.com/emersion/go-imap/v2"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/undo"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// withIMAPRetry wraps an IMAP operation with stale-connection retry.
// If the operation fails with a connection error, the dead connection is discarded
// and the operation is retried once with a fresh connection.
func (a *App) withIMAPRetry(accountID string, op func(conn *imap.Client) error) error {
	log := logging.WithComponent("app.imapRetry")

	poolConn, err := a.imapPool.GetConnection(a.ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get IMAP connection: %w", err)
	}

	err = op(poolConn.Client())
	if err == nil {
		a.imapPool.Release(poolConn)
		return nil
	}

	if !imap.IsConnectionError(err) {
		a.imapPool.Release(poolConn)
		return err
	}

	// Stale connection — discard and retry once with fresh connection
	log.Warn().Err(err).Str("account", accountID).Msg("IMAP connection error, retrying with fresh connection")
	a.imapPool.Discard(poolConn)

	poolConn, err = a.imapPool.GetConnection(a.ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get IMAP connection on retry: %w", err)
	}
	defer a.imapPool.Release(poolConn)

	return op(poolConn.Client())
}

// ============================================================================
// Message Actions API - Exposed to frontend via Wails bindings
// ============================================================================

// MarkAsRead marks messages as read
func (a *App) MarkAsRead(messageIDs []string) error {
	return a.setReadStatus(messageIDs, true)
}

// MarkAllFolderMessagesAsRead marks all unread messages in a folder as read
func (a *App) MarkAllFolderMessagesAsRead(folderID string) error {
	ids, err := a.messageStore.GetUnreadMessageIDsByFolder(folderID)
	if err != nil {
		return fmt.Errorf("failed to get unread messages: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	return a.MarkAsRead(ids)
}

// MarkAllFolderMessagesAsUnread marks all read messages in a folder as unread
func (a *App) MarkAllFolderMessagesAsUnread(folderID string) error {
	ids, err := a.messageStore.GetReadMessageIDsByFolder(folderID)
	if err != nil {
		return fmt.Errorf("failed to get read messages: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	return a.MarkAsUnread(ids)
}

// MarkAsUnread marks messages as unread
func (a *App) MarkAsUnread(messageIDs []string) error {
	return a.setReadStatus(messageIDs, false)
}

func (a *App) setReadStatus(messageIDs []string, isRead bool) error {
	log := logging.WithComponent("app")

	if len(messageIDs) == 0 {
		return nil
	}

	// Get messages to find their UIDs and folders
	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	// Group by folder for IMAP operations
	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Update local DB first (local-first)
	isReadPtr := &isRead
	if err := a.messageStore.UpdateFlagsBatch(messageIDs, isReadPtr, nil); err != nil {
		return fmt.Errorf("failed to update local flags: %w", err)
	}

	// Emit event for UI update with flag state
	wailsRuntime.EventsEmit(a.ctx, "messages:readChanged", map[string]interface{}{
		"messageIds": messageIDs,
		"isRead":     isRead,
	})

	// Update folder unread counts in background to avoid blocking other DB operations
	go func() {
		defer recoverPanic("app.actions", "update folder counts")
		folderCounts := make(map[string]int)
		for folderID := range byFolder {
			unreadCount, err := a.messageStore.CountUnreadByFolder(folderID)
			if err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to count unread messages")
				continue
			}
			folderObj, err := a.folderStore.Get(folderID)
			if err != nil || folderObj == nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to get folder")
				continue
			}
			if err := a.folderStore.UpdateCounts(folderID, folderObj.TotalCount, unreadCount); err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to update folder counts")
				continue
			}
			folderCounts[folderID] = unreadCount
		}
		if len(folderCounts) > 0 {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", folderCounts)
		}
	}()

	// Sync to IMAP in background with retry
	go func() {
		defer recoverPanic("app.actions", "sync flags to IMAP")
		for folderID, msgs := range byFolder {
			var err error
			for attempt := 1; attempt <= 3; attempt++ {
				err = a.syncFlagsToIMAP(msgs, folderID, "read", isRead)
				if err == nil {
					break
				}
				log.Warn().Err(err).Int("attempt", attempt).Str("folderID", folderID).Msg("Failed to sync read flags to IMAP, retrying...")
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to sync read flags to IMAP after 3 attempts")
			}
		}
	}()

	// Read-flag changes are intentionally NOT pushed onto the undo stack.
	// The original design treated every mark-read/unread as undoable, but
	// in practice the auto-mark-as-read timer (and bulk operations) flooded
	// the stack so Cmd+Z routinely undid a background read flip instead of
	// the user's last real action (archive / move / trash). The cure is
	// simpler than per-call-site filtering: just don't make read changes
	// undoable at all — the user can re-flip manually if they need to.
	// Closes #243.

	return nil
}

// Star marks messages as starred
func (a *App) Star(messageIDs []string) error {
	return a.setStarredStatus(messageIDs, true)
}

// Unstar removes star from messages
func (a *App) Unstar(messageIDs []string) error {
	return a.setStarredStatus(messageIDs, false)
}

func (a *App) setStarredStatus(messageIDs []string, isStarred bool) error {
	log := logging.WithComponent("app")

	if len(messageIDs) == 0 {
		return nil
	}

	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Update local DB first
	isStarredPtr := &isStarred
	if err := a.messageStore.UpdateFlagsBatch(messageIDs, nil, isStarredPtr); err != nil {
		return fmt.Errorf("failed to update local flags: %w", err)
	}

	wailsRuntime.EventsEmit(a.ctx, "messages:starredChanged", map[string]interface{}{
		"messageIds": messageIDs,
		"isStarred":  isStarred,
	})

	// Sync to IMAP in background with retry
	go func() {
		defer recoverPanic("app.actions", "sync star flags to IMAP")
		for folderID, msgs := range byFolder {
			var err error
			for attempt := 1; attempt <= 3; attempt++ {
				err = a.syncFlagsToIMAP(msgs, folderID, "starred", isStarred)
				if err == nil {
					break
				}
				log.Warn().Err(err).Int("attempt", attempt).Str("folderID", folderID).Msg("Failed to sync starred flags to IMAP, retrying...")
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to sync starred flags to IMAP after 3 attempts")
			}
		}
	}()

	// Star changes are intentionally NOT pushed onto the undo stack. Same
	// rationale as setReadStatus: the cost of stack pollution outweighs
	// the value of undo for a flag flip that the user can trivially
	// reverse with another star/unstar click. Closes #243.

	return nil
}

// syncFlagsToIMAP syncs flag changes to IMAP server
func (a *App) syncFlagsToIMAP(messages []*message.Message, folderID, flagType string, flagValue bool) error {
	if len(messages) == 0 {
		return nil
	}

	folderObj, err := a.folderStore.Get(folderID)
	if err != nil || folderObj == nil {
		return fmt.Errorf("folder not found: %s", folderID)
	}

	uids := make([]goImap.UID, len(messages))
	for i, m := range messages {
		uids[i] = goImap.UID(m.UID)
	}

	var flag goImap.Flag
	switch flagType {
	case "read":
		flag = goImap.FlagSeen
	case "starred":
		flag = goImap.FlagFlagged
	}

	return a.withIMAPRetry(messages[0].AccountID, func(conn *imap.Client) error {
		if _, err := conn.SelectMailbox(a.ctx, folderObj.Path); err != nil {
			return fmt.Errorf("failed to select mailbox: %w", err)
		}

		if flagValue {
			return conn.AddMessageFlags(uids, []goImap.Flag{flag})
		}
		return conn.RemoveMessageFlags(uids, []goImap.Flag{flag})
	})
}

// MoveToFolder moves messages to a specified folder
func (a *App) MoveToFolder(messageIDs []string, destFolderID string) error {
	log := logging.WithComponent("app")

	if len(messageIDs) == 0 {
		return nil
	}

	// Cross-account selections (Unified Inbox or any mixed-account multi-
	// select) route to a partition helper that recurses through this
	// same function with uniform single-account slices. The existing
	// messages[0].AccountID != destFolder.AccountID guard below then
	// fires exactly once per partition with a correct full-batch
	// classification.
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.moveToFolderCrossAccount(messageIDs, destFolderID)
	}

	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	destFolder, err := a.folderStore.Get(destFolderID)
	if err != nil || destFolder == nil {
		return fmt.Errorf("destination folder not found: %s", destFolderID)
	}

	// Cross-account move: APPEND raw bytes to destination first, then route source
	// cleanup through the existing Trash() pipeline (which handles local DB, IMAP
	// COPY+EXPUNGE within source, folder counts, events, undo, Gmail labels).
	// Strict ordering: APPEND completes synchronously before Trash runs — if
	// APPEND fails, source stays untouched.
	if messages[0].AccountID != destFolder.AccountID {
		if err := a.copyMessagesAcrossAccounts(messages, destFolder); err != nil {
			return fmt.Errorf("cross-account move: append failed: %w", err)
		}
		_, trashErr := a.Trash(messageIDs)
		// Sync destination so appended messages get correct UIDs locally.
		go func() {
			defer recoverPanic("app.actions", "cross-account move dest sync")
			_ = a.SyncFolder(destFolder.AccountID, destFolder.ID)
		}()
		return trashErr
	}

	// Group by source folder
	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Update local DB first
	if err := a.messageStore.MoveMessages(messageIDs, destFolderID); err != nil {
		return fmt.Errorf("failed to move messages locally: %w", err)
	}

	wailsRuntime.EventsEmit(a.ctx, "messages:moved", map[string]interface{}{
		"messageIds":   messageIDs,
		"destFolderId": destFolderID,
	})

	// Update folder unread counts for source and destination folders
	go func() {
		defer recoverPanic("app.actions", "update folder counts after move")
		folderCounts := make(map[string]int)

		// Update source folders
		for folderID, msgs := range byFolder {
			unreadCount, err := a.messageStore.CountUnreadByFolder(folderID)
			if err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to count unread messages")
				continue
			}
			folderObj, err := a.folderStore.Get(folderID)
			if err != nil || folderObj == nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to get folder")
				continue
			}
			newTotalCount := folderObj.TotalCount - len(msgs)
			if newTotalCount < 0 {
				newTotalCount = 0
			}
			if err := a.folderStore.UpdateCounts(folderID, newTotalCount, unreadCount); err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to update folder counts")
				continue
			}
			folderCounts[folderID] = unreadCount
		}

		// Update destination folder
		unreadCount, err := a.messageStore.CountUnreadByFolder(destFolderID)
		if err != nil {
			log.Error().Err(err).Str("folderID", destFolderID).Msg("Failed to count unread messages for destination")
		} else {
			destFolderObj, err := a.folderStore.Get(destFolderID)
			if err == nil && destFolderObj != nil {
				newTotalCount := destFolderObj.TotalCount + len(messageIDs)
				if err := a.folderStore.UpdateCounts(destFolderID, newTotalCount, unreadCount); err != nil {
					log.Error().Err(err).Str("folderID", destFolderID).Msg("Failed to update destination folder counts")
				} else {
					folderCounts[destFolderID] = unreadCount
				}
			}
		}

		if len(folderCounts) > 0 {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", folderCounts)
		}
	}()

	// Sync to IMAP in background (COPY + DELETE), then sync destination to get correct UIDs.
	// Use SyncFolder instead of calling SyncMessages/FetchBodiesInBackground directly
	// so that back-to-back moves to the same folder are serialized — the second call
	// cancels the first and starts fresh, preventing the first sync from deleting
	// locally-moved messages whose IMAP COPY hasn't completed yet.
	go func() {
		defer recoverPanic("app.actions", "move messages on IMAP")
		for sourceFolderID, msgs := range byFolder {
			if err := a.moveMessagesToIMAP(msgs, sourceFolderID, destFolder); err != nil {
				log.Error().Err(err).
					Str("sourceFolderID", sourceFolderID).
					Str("destFolderID", destFolderID).
					Msg("Failed to move messages on IMAP")
				return
			}
		}

		// Sync destination folder so moved messages get correct UIDs (headers + bodies).
		// Clear the debounce timestamp so this request isn't silently dropped.
		if len(messages) > 0 {
			accountID := messages[0].AccountID
			syncKey := accountID + ":" + destFolderID
			a.syncMu.Lock()
			delete(a.syncLastRequest, syncKey)
			a.syncMu.Unlock()

			if err := a.SyncFolder(accountID, destFolderID); err != nil && err != context.Canceled {
				log.Warn().Err(err).Str("destFolderID", destFolderID).Msg("Failed to sync destination folder after move")
			}

			// Clean up temporary negative-UID rows left by MoveMessages.
			// The sync above fetched the real messages with correct UIDs.
			if err := a.messageStore.DeleteTempUIDs(destFolderID); err != nil {
				log.Warn().Err(err).Str("destFolderID", destFolderID).Msg("Failed to clean up temp UIDs after move")
			}
		}
	}()

	// Create undo command for each source folder
	for sourceFolderID, msgs := range byFolder {
		rfc822IDs := make([]string, 0, len(msgs))
		for _, m := range msgs {
			if m.MessageID != "" {
				rfc822IDs = append(rfc822IDs, m.MessageID)
			}
		}
		if len(rfc822IDs) == 0 {
			continue
		}

		cmd := undo.NewMoveCommand(
			a,
			msgs[0].AccountID,
			rfc822IDs,
			sourceFolderID,
			destFolderID,
			fmt.Sprintf("Move to %s", destFolder.Name),
		)
		a.undoStack.Push(cmd)
	}

	return nil
}

// isGmailAccount checks if the account uses Gmail's IMAP server.
// Gmail uses labels instead of folders — IMAP COPY adds a label rather than
// creating an independent copy, and adding the Trash/Spam label hides the
// message from all other IMAP mailbox views.
func (a *App) isGmailAccount(accountID string) bool {
	acc, err := a.accountStore.Get(accountID)
	if err != nil || acc == nil {
		return false
	}
	return acc.IMAPHost == "imap.gmail.com"
}

func (a *App) moveMessagesToIMAP(messages []*message.Message, sourceFolderID string, destFolder *folder.Folder) error {
	log := logging.WithComponent("app.moveMessagesToIMAP")

	if len(messages) == 0 {
		return nil
	}

	sourceFolder, err := a.folderStore.Get(sourceFolderID)
	if err != nil || sourceFolder == nil {
		return fmt.Errorf("source folder not found")
	}

	// Collect UIDs for logging
	uidList := make([]uint32, len(messages))
	for i, m := range messages {
		uidList[i] = m.UID
	}

	log.Info().
		Str("sourceFolder", sourceFolder.Path).
		Str("destFolder", destFolder.Path).
		Uints32("uids", uidList).
		Int("count", len(messages)).
		Msg("Starting IMAP move operation")

	accountID := messages[0].AccountID
	isGmail := a.isGmailAccount(accountID)
	destIsTrashOrSpam := destFolder.Type == folder.TypeTrash || destFolder.Type == folder.TypeSpam

	// For Gmail + dest is Trash/Spam: partition messages by whether they have
	// copies in other folders. Messages with copies only need label removal
	// (DELETE without COPY). Messages without copies need a real move (COPY + DELETE).
	var moveUIDs, labelRemovalUIDs []goImap.UID
	if isGmail && destIsTrashOrSpam {
		for _, m := range messages {
			hasCopies := false
			if m.MessageID != "" {
				var copyErr error
				hasCopies, copyErr = a.messageStore.HasCopiesInOtherFolders(m.MessageID, sourceFolderID, accountID)
				if copyErr != nil {
					log.Warn().Err(copyErr).Str("messageID", m.MessageID).Msg("Failed to check for copies, treating as sole copy")
				}
			}
			if hasCopies {
				labelRemovalUIDs = append(labelRemovalUIDs, goImap.UID(m.UID))
				continue
			}
			moveUIDs = append(moveUIDs, goImap.UID(m.UID))
		}
		log.Info().
			Int("moveCount", len(moveUIDs)).
			Int("labelRemovalCount", len(labelRemovalUIDs)).
			Msg("Gmail: partitioned messages for trash/spam operation")
	}
	if !isGmail || !destIsTrashOrSpam {
		for _, m := range messages {
			moveUIDs = append(moveUIDs, goImap.UID(m.UID))
		}
	}

	// Combine all UIDs that need DELETE from source
	allUIDs := append(moveUIDs, labelRemovalUIDs...)
	if len(allUIDs) == 0 {
		return nil
	}

	err = a.withIMAPRetry(accountID, func(conn *imap.Client) error {
		// Select source mailbox
		log.Debug().Str("mailbox", sourceFolder.Path).Msg("Selecting source mailbox")
		if _, err := conn.SelectMailbox(a.ctx, sourceFolder.Path); err != nil {
			return fmt.Errorf("failed to select source mailbox: %w", err)
		}

		// COPY only the messages that need a real move (not label-removal-only)
		if len(moveUIDs) > 0 {
			log.Debug().Str("destMailbox", destFolder.Path).Int("count", len(moveUIDs)).Msg("Copying messages to destination")
			if _, err := conn.CopyMessages(moveUIDs, destFolder.Path); err != nil {
				return fmt.Errorf("failed to copy messages: %w", err)
			}
			log.Debug().Msg("Messages copied successfully")
		}

		// DELETE all UIDs from source (both moved and label-removed)
		log.Debug().Int("count", len(allUIDs)).Msg("Deleting messages from source (marking deleted + expunge)")
		if err := conn.DeleteMessagesByUID(allUIDs); err != nil {
			return fmt.Errorf("failed to delete messages from source: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("IMAP move operation failed")
		return err
	}

	log.Info().
		Str("sourceFolder", sourceFolder.Path).
		Str("destFolder", destFolder.Path).
		Int("count", len(messages)).
		Msg("IMAP move operation completed successfully")

	return nil
}

// CopyToFolder copies messages to a specified folder (keeps original)
// Unlike MoveToFolder, this only copies - original messages remain in place
func (a *App) CopyToFolder(messageIDs []string, destFolderID string) error {
	log := logging.WithComponent("app")

	if len(messageIDs) == 0 {
		return nil
	}

	// Cross-account selections route to a partition helper that recurses
	// through this same function with uniform single-account slices. See
	// MoveToFolder for the rationale — same shape applied to Copy (which
	// keeps originals in place rather than deleting them).
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.copyToFolderCrossAccount(messageIDs, destFolderID)
	}

	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	destFolder, err := a.folderStore.Get(destFolderID)
	if err != nil || destFolder == nil {
		return fmt.Errorf("destination folder not found: %s", destFolderID)
	}

	// Cross-account copy: APPEND raw bytes to destination. Fire-and-forget to
	// match intra-account CopyToFolder's existing async behavior.
	if messages[0].AccountID != destFolder.AccountID {
		go func() {
			defer recoverPanic("app.actions", "cross-account copy")
			if err := a.copyMessagesAcrossAccounts(messages, destFolder); err != nil {
				log.Error().Err(err).
					Str("sourceAccountID", messages[0].AccountID).
					Str("destAccountID", destFolder.AccountID).
					Str("destFolderID", destFolder.ID).
					Msg("Cross-account copy failed")
				return
			}
			// Dest folder refresh + sidebar count bump ride on folder:synced from SyncFolder.
			_ = a.SyncFolder(destFolder.AccountID, destFolder.ID)
		}()
		return nil
	}

	// Group by source folder
	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Copy on IMAP (no local DB change - messages stay in source folder)
	go func() {
		defer recoverPanic("app.actions", "copy messages on IMAP")
		for sourceFolderID, msgs := range byFolder {
			if err := a.copyMessagesToIMAP(msgs, sourceFolderID, destFolder); err != nil {
				log.Error().Err(err).
					Str("sourceFolderID", sourceFolderID).
					Str("destFolderID", destFolderID).
					Msg("Failed to copy messages on IMAP")
			}
		}

		// Sync destination folder so copied messages appear (headers + bodies)
		// Clear debounce so this request isn't silently dropped
		if len(messages) > 0 {
			accountID := messages[0].AccountID
			syncKey := accountID + ":" + destFolderID
			a.syncMu.Lock()
			delete(a.syncLastRequest, syncKey)
			a.syncMu.Unlock()

			if err := a.SyncFolder(accountID, destFolderID); err != nil && err != context.Canceled {
				log.Warn().Err(err).Str("destFolderID", destFolderID).Msg("Failed to sync destination folder after copy")
			}
		}
		// Dest folder refresh + sidebar count bump ride on folder:synced from SyncFolder above.
	}()

	return nil
}

func (a *App) copyMessagesToIMAP(messages []*message.Message, sourceFolderID string, destFolder *folder.Folder) error {
	if len(messages) == 0 {
		return nil
	}

	sourceFolder, err := a.folderStore.Get(sourceFolderID)
	if err != nil || sourceFolder == nil {
		return fmt.Errorf("source folder not found")
	}

	uids := make([]goImap.UID, len(messages))
	for i, m := range messages {
		uids[i] = goImap.UID(m.UID)
	}

	return a.withIMAPRetry(messages[0].AccountID, func(conn *imap.Client) error {
		if _, err := conn.SelectMailbox(a.ctx, sourceFolder.Path); err != nil {
			return fmt.Errorf("failed to select source mailbox: %w", err)
		}

		// COPY to destination (no DELETE - messages stay in source)
		if _, err := conn.CopyMessages(uids, destFolder.Path); err != nil {
			return fmt.Errorf("failed to copy messages: %w", err)
		}

		return nil
	})
}

// flagsForAppend maps a local message's flag fields to the IMAP flag slice
// used by AppendMessage on cross-account copy/move.
func flagsForAppend(m *message.Message) []goImap.Flag {
	flags := []goImap.Flag{}
	if m.IsRead {
		flags = append(flags, goImap.FlagSeen)
	}
	if m.IsStarred {
		flags = append(flags, goImap.FlagFlagged)
	}
	if m.IsAnswered {
		flags = append(flags, goImap.FlagAnswered)
	}
	if m.IsDraft {
		flags = append(flags, goImap.FlagDraft)
	}
	return flags
}

// copyMessagesAcrossAccounts fetches each message's raw RFC822 bytes from the
// source account's IMAP and APPENDs them to the destination account's IMAP.
// Used by the cross-account branches of MoveToFolder and CopyToFolder.
//
// Synchronous: returns when every APPEND has succeeded. Caller is expected to
// short-circuit on error so the source side is never touched after a partial
// success/failure.
func (a *App) copyMessagesAcrossAccounts(messages []*message.Message, destFolder *folder.Folder) error {
	log := logging.WithComponent("app.copyMessagesAcrossAccounts")

	if len(messages) == 0 {
		return nil
	}

	log.Info().
		Str("sourceAccountID", messages[0].AccountID).
		Str("destAccountID", destFolder.AccountID).
		Str("destFolder", destFolder.Path).
		Int("count", len(messages)).
		Msg("Starting cross-account append")

	return a.withIMAPRetry(destFolder.AccountID, func(conn *imap.Client) error {
		if _, err := conn.SelectMailbox(a.ctx, destFolder.Path); err != nil {
			return fmt.Errorf("failed to select destination mailbox: %w", err)
		}
		for _, m := range messages {
			raw, err := a.syncEngine.FetchRawMessage(a.ctx, m.AccountID, m.FolderID, m.UID)
			if err != nil {
				return fmt.Errorf("failed to fetch raw message from source: %w", err)
			}
			if _, err := conn.AppendMessage(destFolder.Path, flagsForAppend(m), m.Date, raw); err != nil {
				return fmt.Errorf("failed to append to destination: %w", err)
			}
		}
		log.Info().
			Str("destFolder", destFolder.Path).
			Int("count", len(messages)).
			Msg("Cross-account append completed")
		return nil
	})
}

// Archive moves messages to the Archive folder
func (a *App) Archive(messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	// Cross-account selections (Unified Inbox) route to a partition
	// helper that recurses through this same function with uniform
	// single-account slices. SpansMultipleAccounts errors fall through
	// to the fast path so a metadata hiccup never breaks what works.
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.archiveCrossAccount(messageIDs)
	}

	// Get first message to determine account
	messages, err := a.messageStore.GetByIDs(messageIDs[:1])
	if err != nil || len(messages) == 0 {
		return fmt.Errorf("failed to get message")
	}

	archiveFolder, err := a.GetSpecialFolder(messages[0].AccountID, folder.TypeArchive)
	if err != nil {
		return fmt.Errorf("failed to get archive folder: %w", err)
	}
	if archiveFolder == nil {
		return fmt.Errorf("no archive folder configured")
	}

	return a.MoveToFolder(messageIDs, archiveFolder.ID)
}

// Trash moves messages to the Trash folder.
// Returns true if at least one message was moved to trash (show undo toast).
// Returns false if all messages were just label-removed on Gmail (no undo).
func (a *App) Trash(messageIDs []string) (bool, error) {
	if len(messageIDs) == 0 {
		return false, nil
	}

	// Cross-account selections (Unified Inbox) route to a partition
	// helper that recurses through this same function with uniform
	// single-account slices. SpansMultipleAccounts errors fall through
	// to the fast path so a metadata hiccup never breaks what works.
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.trashCrossAccount(messageIDs)
	}

	messages, err := a.messageStore.GetByIDs(messageIDs[:1])
	if err != nil || len(messages) == 0 {
		return false, fmt.Errorf("failed to get message")
	}

	accountID := messages[0].AccountID

	trashFolder, err := a.GetSpecialFolder(accountID, folder.TypeTrash)
	if err != nil {
		return false, fmt.Errorf("failed to get trash folder: %w", err)
	}
	if trashFolder == nil {
		return false, fmt.Errorf("no trash folder configured")
	}

	// Non-Gmail: normal move to trash for all messages
	if !a.isGmailAccount(accountID) {
		return true, a.MoveToFolder(messageIDs, trashFolder.ID)
	}

	// Gmail: partition messages into copies (label-remove) vs sole copies (move to trash)
	return a.gmailTrashOrSpam(messageIDs, trashFolder)
}

// gmailTrashOrSpam handles Gmail-specific trash/spam behavior.
// Messages with copies in other folders get label-removed (DELETE only).
// Messages without copies get moved to the destination folder (COPY + DELETE).
// Returns true if at least one message was moved to dest (show undo toast).
func (a *App) gmailTrashOrSpam(messageIDs []string, destFolder *folder.Folder) (bool, error) {
	log := logging.WithComponent("app.gmailTrashOrSpam")

	allMessages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return false, fmt.Errorf("failed to get messages: %w", err)
	}
	if len(allMessages) == 0 {
		return false, nil
	}

	accountID := allMessages[0].AccountID

	// Partition: copies (exist in other folders) vs non-copies (sole copy)
	var copyIDs, nonCopyIDs []string
	var copyMsgs []*message.Message
	for _, m := range allMessages {
		hasCopies := false
		if m.MessageID != "" {
			hasCopies, err = a.messageStore.HasCopiesInOtherFolders(m.MessageID, m.FolderID, accountID)
			if err != nil {
				log.Warn().Err(err).Str("messageID", m.MessageID).Msg("Failed to check for copies, treating as sole copy")
			}
		}
		if hasCopies {
			copyIDs = append(copyIDs, m.ID)
			copyMsgs = append(copyMsgs, m)
			continue
		}
		nonCopyIDs = append(nonCopyIDs, m.ID)
	}

	log.Info().
		Int("copyCount", len(copyIDs)).
		Int("nonCopyCount", len(nonCopyIDs)).
		Str("destFolder", destFolder.Name).
		Msg("Gmail: partitioned messages for trash/spam")

	// Handle copies: just remove the label (delete locally + IMAP DELETE without COPY)
	if len(copyIDs) > 0 {
		if err := a.gmailRemoveLabel(copyMsgs); err != nil {
			log.Error().Err(err).Msg("Failed to remove Gmail labels for copies")
			return len(nonCopyIDs) > 0, err
		}
	}

	// Handle non-copies: normal move to trash/spam
	if len(nonCopyIDs) > 0 {
		if err := a.MoveToFolder(nonCopyIDs, destFolder.ID); err != nil {
			return true, err
		}
	}

	return len(nonCopyIDs) > 0, nil
}

// gmailRemoveLabel removes messages from their current folder (label) on Gmail.
// This deletes them locally and does IMAP DELETE from source without COPY —
// effectively removing the label while the message stays in other labels.
func (a *App) gmailRemoveLabel(messages []*message.Message) error {
	log := logging.WithComponent("app.gmailRemoveLabel")

	if len(messages) == 0 {
		return nil
	}

	// Group by source folder
	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Collect all IDs for local delete
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}

	// Delete from local DB
	if err := a.messageStore.DeleteBatch(ids); err != nil {
		return fmt.Errorf("failed to delete messages locally: %w", err)
	}

	wailsRuntime.EventsEmit(a.ctx, "messages:deleted", ids)

	// Update folder counts
	go func() {
		defer recoverPanic("app.actions", "update folder counts after label removal")
		folderCounts := make(map[string]int)
		for folderID, msgs := range byFolder {
			unreadCount, countErr := a.messageStore.CountUnreadByFolder(folderID)
			if countErr != nil {
				log.Error().Err(countErr).Str("folderID", folderID).Msg("Failed to count unread messages")
				continue
			}
			folderObj, getErr := a.folderStore.Get(folderID)
			if getErr != nil || folderObj == nil {
				continue
			}
			newTotalCount := folderObj.TotalCount - len(msgs)
			if newTotalCount < 0 {
				newTotalCount = 0
			}
			if updateErr := a.folderStore.UpdateCounts(folderID, newTotalCount, unreadCount); updateErr != nil {
				log.Error().Err(updateErr).Str("folderID", folderID).Msg("Failed to update folder counts")
				continue
			}
			folderCounts[folderID] = unreadCount
		}
		if len(folderCounts) > 0 {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", folderCounts)
		}
	}()

	// IMAP: DELETE from source folders (no COPY — just remove the label)
	go func() {
		defer recoverPanic("app.actions", "remove Gmail label on IMAP")
		for folderID, msgs := range byFolder {
			if err := a.removeFromIMAPFolder(msgs, folderID); err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to remove messages from IMAP folder")
			}
		}
	}()

	return nil
}

// removeFromIMAPFolder does SELECT + DELETE by UID on the given folder.
// Unlike deleteMessagesFromIMAP, this does NOT check HasCopiesInOtherFolders —
// it unconditionally removes the messages from the folder (removes the Gmail label).
func (a *App) removeFromIMAPFolder(messages []*message.Message, folderID string) error {
	if len(messages) == 0 {
		return nil
	}

	folderObj, err := a.folderStore.Get(folderID)
	if err != nil || folderObj == nil {
		return fmt.Errorf("folder not found")
	}

	var uids []goImap.UID
	for _, m := range messages {
		if m.UID == 0 || int32(m.UID) < 0 {
			continue
		}
		uids = append(uids, goImap.UID(m.UID))
	}
	if len(uids) == 0 {
		return nil
	}

	return a.withIMAPRetry(messages[0].AccountID, func(conn *imap.Client) error {
		if _, err := conn.SelectMailbox(a.ctx, folderObj.Path); err != nil {
			return fmt.Errorf("failed to select mailbox: %w", err)
		}
		return conn.DeleteMessagesByUID(uids)
	})
}

// MarkAsSpam moves messages to the Spam folder.
// Returns true if at least one message was moved to spam (show undo toast).
// Returns false if all messages were just label-removed on Gmail (no undo).
func (a *App) MarkAsSpam(messageIDs []string) (bool, error) {
	if len(messageIDs) == 0 {
		return false, nil
	}

	// Cross-account selections (Unified Inbox) route to a partition
	// helper that recurses through this same function with uniform
	// single-account slices.
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.markAsSpamCrossAccount(messageIDs)
	}

	messages, err := a.messageStore.GetByIDs(messageIDs[:1])
	if err != nil || len(messages) == 0 {
		return false, fmt.Errorf("failed to get message")
	}

	accountID := messages[0].AccountID

	spamFolder, err := a.GetSpecialFolder(accountID, folder.TypeSpam)
	if err != nil {
		return false, fmt.Errorf("failed to get spam folder: %w", err)
	}
	if spamFolder == nil {
		return false, fmt.Errorf("no spam folder configured")
	}

	// Non-Gmail: normal move to spam
	if !a.isGmailAccount(accountID) {
		return true, a.MoveToFolder(messageIDs, spamFolder.ID)
	}

	// Gmail: partition messages into copies (label-remove) vs sole copies (move to spam)
	return a.gmailTrashOrSpam(messageIDs, spamFolder)
}

// MarkAsNotSpam moves messages from Spam to Inbox
func (a *App) MarkAsNotSpam(messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	// Cross-account selections (Unified Inbox) route to a partition
	// helper that recurses through this same function with uniform
	// single-account slices.
	if spans, _ := a.messageStore.SpansMultipleAccounts(messageIDs); spans {
		return a.markAsNotSpamCrossAccount(messageIDs)
	}

	messages, err := a.messageStore.GetByIDs(messageIDs[:1])
	if err != nil || len(messages) == 0 {
		return fmt.Errorf("failed to get message")
	}

	inboxFolder, err := a.folderStore.GetByType(messages[0].AccountID, folder.TypeInbox)
	if err != nil {
		return fmt.Errorf("failed to get inbox folder: %w", err)
	}
	if inboxFolder == nil {
		return fmt.Errorf("no inbox folder found")
	}

	return a.MoveToFolder(messageIDs, inboxFolder.ID)
}

// EmptyTrash permanently deletes all messages in a trash folder
func (a *App) EmptyTrash(accountID, folderID string) error {
	ids, err := a.messageStore.GetAllIDsByFolder(folderID)
	if err != nil {
		return fmt.Errorf("failed to get messages in trash: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	return a.DeletePermanently(ids)
}

// DeletePermanently permanently deletes messages
func (a *App) DeletePermanently(messageIDs []string) error {
	log := logging.WithComponent("app")

	if len(messageIDs) == 0 {
		return nil
	}

	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	// Group by folder
	byFolder := make(map[string][]*message.Message)
	for _, m := range messages {
		byFolder[m.FolderID] = append(byFolder[m.FolderID], m)
	}

	// Delete from local DB first
	if err := a.messageStore.DeleteBatch(messageIDs); err != nil {
		return fmt.Errorf("failed to delete messages locally: %w", err)
	}

	wailsRuntime.EventsEmit(a.ctx, "messages:deleted", messageIDs)

	// Update folder unread counts
	go func() {
		defer recoverPanic("app.actions", "update folder counts after delete")
		folderCounts := make(map[string]int)
		for folderID, msgs := range byFolder {
			unreadCount, err := a.messageStore.CountUnreadByFolder(folderID)
			if err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to count unread messages")
				continue
			}
			folderObj, err := a.folderStore.Get(folderID)
			if err != nil || folderObj == nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to get folder")
				continue
			}
			newTotalCount := folderObj.TotalCount - len(msgs)
			if newTotalCount < 0 {
				newTotalCount = 0
			}
			if err := a.folderStore.UpdateCounts(folderID, newTotalCount, unreadCount); err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to update folder counts")
				continue
			}
			folderCounts[folderID] = unreadCount
		}
		if len(folderCounts) > 0 {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", folderCounts)
		}
	}()

	// Delete from IMAP in background
	go func() {
		defer recoverPanic("app.actions", "delete from IMAP")
		for folderID, msgs := range byFolder {
			if err := a.deleteMessagesFromIMAP(msgs, folderID); err != nil {
				log.Error().Err(err).Str("folderID", folderID).Msg("Failed to delete messages from IMAP")
			}
		}
	}()

	// Note: Permanent delete undo is complex - would need to store full message content
	// For now, we don't add to undo stack for permanent deletes

	return nil
}

func (a *App) deleteMessagesFromIMAP(messages []*message.Message, folderID string) error {
	if len(messages) == 0 {
		return nil
	}

	log := logging.WithComponent("app.deleteMessagesFromIMAP")

	folderObj, err := a.folderStore.Get(folderID)
	if err != nil || folderObj == nil {
		return fmt.Errorf("folder not found")
	}

	accountID := messages[0].AccountID
	isGmail := a.isGmailAccount(accountID)

	var uids []goImap.UID
	for _, m := range messages {
		// Skip messages with temp UIDs (negative values from local move operations)
		// that haven't been reconciled with the IMAP server yet
		if m.UID == 0 || int32(m.UID) < 0 {
			continue
		}

		// For Gmail: skip IMAP delete if the same RFC 822 Message-ID exists in
		// other local folders. On Gmail there's only ONE underlying message — an
		// IMAP EXPUNGE here would destroy it across ALL labels (Inbox, etc.).
		if isGmail && m.MessageID != "" {
			hasCopies, copyErr := a.messageStore.HasCopiesInOtherFolders(m.MessageID, folderID, accountID)
			if copyErr != nil {
				log.Warn().Err(copyErr).Str("messageID", m.MessageID).Msg("Failed to check for copies in other folders")
			}
			if hasCopies {
				log.Debug().Str("messageID", m.MessageID).Msg("Gmail: skipping IMAP delete — message exists in other folders")
				continue
			}
		}

		uids = append(uids, goImap.UID(m.UID))
	}
	if len(uids) == 0 {
		return nil
	}

	return a.withIMAPRetry(accountID, func(conn *imap.Client) error {
		if _, err := conn.SelectMailbox(a.ctx, folderObj.Path); err != nil {
			return fmt.Errorf("failed to select mailbox: %w", err)
		}

		return conn.DeleteMessagesByUID(uids)
	})
}

// ============================================================================
// Cross-account dispatch helpers
//
// Bulk actions invoked from Unified Inbox may carry a selection that spans
// multiple accounts. The public entry points (Trash, Archive, MarkAsSpam,
// MarkAsNotSpam, MoveToFolder, CopyToFolder) detect this up front via
// messageStore.SpansMultipleAccounts and route here. Each helper partitions
// the IDs by AccountID, then loops by re-invoking the public function with
// each partition's single-account slice. The recursive call takes the
// untouched single-account fast path. Recursion depth is bounded at 1 by
// construction (partition slices are uniform-account by definition).
// ============================================================================

// partitionByAccount groups message IDs by their owning account. Invalid
// IDs (no matching row in the store) are silently dropped — matching the
// behavior of every other call site that consumes GetByIDs results.
func (a *App) partitionByAccount(messageIDs []string) (map[string][]string, error) {
	msgs, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	byAccount := make(map[string][]string)
	for _, m := range msgs {
		byAccount[m.AccountID] = append(byAccount[m.AccountID], m.ID)
	}
	return byAccount, nil
}

// trashCrossAccount fan-outs Trash() per account partition. Aggregates the
// (bool, error) returns: anyMoved is OR'd across partitions, firstErr wins.
func (a *App) trashCrossAccount(messageIDs []string) (bool, error) {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return false, err
	}
	var anyMoved bool
	var firstErr error
	for _, ids := range byAccount {
		moved, err := a.Trash(ids)
		if moved {
			anyMoved = true
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return anyMoved, firstErr
}

// archiveCrossAccount fan-outs Archive() per account partition. Aggregates
// first error encountered.
func (a *App) archiveCrossAccount(messageIDs []string) error {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return err
	}
	var firstErr error
	for _, ids := range byAccount {
		if err := a.Archive(ids); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// markAsSpamCrossAccount fan-outs MarkAsSpam() per account partition.
func (a *App) markAsSpamCrossAccount(messageIDs []string) (bool, error) {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return false, err
	}
	var anyMoved bool
	var firstErr error
	for _, ids := range byAccount {
		moved, err := a.MarkAsSpam(ids)
		if moved {
			anyMoved = true
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return anyMoved, firstErr
}

// markAsNotSpamCrossAccount fan-outs MarkAsNotSpam() per account partition.
func (a *App) markAsNotSpamCrossAccount(messageIDs []string) error {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return err
	}
	var firstErr error
	for _, ids := range byAccount {
		if err := a.MarkAsNotSpam(ids); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// moveToFolderCrossAccount fan-outs MoveToFolder() per source-account
// partition. Each recursive call hits the public MoveToFolder with a
// uniform-source-account slice; the existing
// `messages[0].AccountID != destFolder.AccountID` guard in that function
// then classifies the WHOLE partition correctly:
//   - partition's account == dest's account → same-account move path.
//   - partition's account != dest's account → existing cross-account
//     APPEND-then-Trash path (copyMessagesAcrossAccounts + recursive
//     Trash on the partition's IDs).
//
// Each partition's outcome is independent — a Gmail partition's failure
// doesn't block an IMAP partition's success.
func (a *App) moveToFolderCrossAccount(messageIDs []string, destFolderID string) error {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return err
	}
	var firstErr error
	for _, ids := range byAccount {
		if err := a.MoveToFolder(ids, destFolderID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// copyToFolderCrossAccount fan-outs CopyToFolder() per source-account
// partition. Mirrors moveToFolderCrossAccount but Copy keeps originals.
func (a *App) copyToFolderCrossAccount(messageIDs []string, destFolderID string) error {
	byAccount, err := a.partitionByAccount(messageIDs)
	if err != nil {
		return err
	}
	var firstErr error
	for _, ids := range byAccount {
		if err := a.CopyToFolder(ids, destFolderID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
