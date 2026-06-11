package app

import (
	"context"
	"fmt"

	"github.com/hkdb/aerion/internal/imap"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ============================================================================
// Undo API - Exposed to frontend via Wails bindings
// ============================================================================

// Undo reverses the most recent undoable action
// Returns the description of what was undone, or error if nothing to undo
func (a *App) Undo() (string, error) {
	cmd := a.undoStack.Pop()
	if cmd == nil {
		return "", fmt.Errorf("nothing to undo")
	}

	if err := cmd.Undo(); err != nil {
		return "", fmt.Errorf("undo failed: %w", err)
	}

	// Emit event to refresh UI
	wailsRuntime.EventsEmit(a.ctx, "undo:completed", cmd.Description())

	return cmd.Description(), nil
}

// CanUndo returns true if there's an action that can be undone
func (a *App) CanUndo() bool {
	return a.undoStack.CanUndo()
}

// GetUndoDescription returns the description of what would be undone
func (a *App) GetUndoDescription() string {
	cmd := a.undoStack.Peek()
	if cmd == nil {
		return ""
	}
	return cmd.Description()
}

// ============================================================================
// UndoContext Implementation - Required for undo.Command operations
// ============================================================================

// GetIMAPConnectionForUndo implements undo.UndoContext
func (a *App) GetIMAPConnectionForUndo(ctx context.Context, accountID string) (*imap.Client, func(), error) {
	poolConn, err := a.imapPool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	return poolConn.Client(), func() { a.imapPool.Release(poolConn) }, nil
}

// UpdateLocalFlags implements undo.UndoContext
func (a *App) UpdateLocalFlags(messageIDs []string, isRead, isStarred *bool) error {
	err := a.messageStore.UpdateFlagsBatch(messageIDs, isRead, isStarred)
	if err != nil {
		return err
	}
	// Emit a per-flag event so each listener sees the typed payload it
	// expects. UpdateLocalFlags is called by undo commands; today each
	// command flips exactly one flag (either read or starred), so in
	// practice only one branch fires. The if/if (not if/else) shape
	// covers a hypothetical future undo that combines both without
	// changing this call site.
	if isRead != nil {
		wailsRuntime.EventsEmit(a.ctx, "messages:readChanged", map[string]interface{}{
			"messageIds": messageIDs,
			"isRead":     *isRead,
		})
	}
	if isStarred != nil {
		wailsRuntime.EventsEmit(a.ctx, "messages:starredChanged", map[string]interface{}{
			"messageIds": messageIDs,
			"isStarred":  *isStarred,
		})
	}
	return nil
}

// MoveLocalMessages implements undo.UndoContext
func (a *App) MoveLocalMessages(messageIDs []string, folderID string) error {
	// Get the source folder IDs before moving (for count updates)
	messages, err := a.messageStore.GetByIDs(messageIDs)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	// Group by source folder
	sourceFolderIDs := make(map[string]bool)
	for _, msg := range messages {
		sourceFolderIDs[msg.FolderID] = true
	}

	// Move messages in database
	err = a.messageStore.MoveMessages(messageIDs, folderID)
	if err != nil {
		return err
	}

	// Emit messages:moved event
	wailsRuntime.EventsEmit(a.ctx, "messages:moved", map[string]interface{}{
		"messageIds":   messageIDs,
		"destFolderId": folderID,
	})

	// Update folder counts for all affected folders (source + destination)
	go func() {
		defer recoverPanic("app.undo", "update folder counts")
		folderCounts := make(map[string]int)

		// Update source folders
		for sourceFolderID := range sourceFolderIDs {
			unreadCount, err := a.messageStore.CountUnreadByFolder(sourceFolderID)
			if err == nil {
				folderObj, err := a.folderStore.Get(sourceFolderID)
				if err == nil && folderObj != nil {
					totalCount, _ := a.messageStore.CountByFolder(sourceFolderID)
					_ = a.folderStore.UpdateCounts(sourceFolderID, totalCount, unreadCount)
					folderCounts[sourceFolderID] = unreadCount
				}
			}
		}

		// Update destination folder
		unreadCount, err := a.messageStore.CountUnreadByFolder(folderID)
		if err == nil {
			folderObj, err := a.folderStore.Get(folderID)
			if err == nil && folderObj != nil {
				totalCount, _ := a.messageStore.CountByFolder(folderID)
				_ = a.folderStore.UpdateCounts(folderID, totalCount, unreadCount)
				folderCounts[folderID] = unreadCount
			}
		}

		if len(folderCounts) > 0 {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", folderCounts)
		}
	}()

	return nil
}

// DeleteLocalMessages implements undo.UndoContext
func (a *App) DeleteLocalMessages(messageIDs []string) error {
	err := a.messageStore.DeleteBatch(messageIDs)
	if err == nil {
		wailsRuntime.EventsEmit(a.ctx, "messages:deleted", messageIDs)
	}
	return err
}

// FindLocalMessageIDs implements undo.UndoContext
// Finds current local DB message IDs by RFC822 Message-ID header and folder
func (a *App) FindLocalMessageIDs(accountID, folderID string, rfc822MessageIDs []string) ([]string, error) {
	return a.messageStore.GetIDsByMessageIDs(accountID, folderID, rfc822MessageIDs)
}

// MoveMessagesToFolder implements undo.UndoContext
// Delegates to the standard MoveToFolder pipeline (IMAP + local DB + events)
func (a *App) MoveMessagesToFolder(messageIDs []string, destFolderID string) error {
	return a.MoveToFolder(messageIDs, destFolderID)
}
