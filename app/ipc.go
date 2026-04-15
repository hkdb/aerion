package app

import (
	"context"
	"os"
	"os/exec"

	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/ipc"
	"github.com/hkdb/aerion/internal/logging"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ============================================================================
// IPC for Multi-Window Support (Detachable Composer)
// ============================================================================

// initIPC initializes the IPC server for multi-window communication.
// This allows detached composer windows to communicate with the main window.
func (a *App) initIPC(ctx context.Context) {
	log := logging.WithComponent("app.ipc")

	// Create token manager for secure client authentication
	tokenMgr, err := ipc.NewTokenManager()
	if err != nil {
		log.Error().Err(err).Msg("Failed to create IPC token manager")
		return
	}
	a.ipcTokenMgr = tokenMgr

	// Create platform-specific IPC server
	a.ipcServer = ipc.NewServer(tokenMgr)

	// Register message handler
	a.ipcServer.OnMessage(a.handleIPCMessage)

	// Log the address (available immediately after NewServer)
	log.Info().Str("address", a.ipcServer.Address()).Msg("Starting IPC server")

	// Start server in background
	go func() {
		defer recoverPanic("app.ipc", "IPC server")
		if err := a.ipcServer.Start(ctx); err != nil {
			// Context cancellation is expected during shutdown
			if ctx.Err() == nil {
				log.Error().Err(err).Msg("IPC server error")
			}
		}
	}()
}

// handleIPCMessage processes messages received from composer windows.
func (a *App) handleIPCMessage(clientID string, msg ipc.Message) {
	log := logging.WithComponent("app.ipc")

	log.Debug().
		Str("clientID", clientID).
		Str("type", msg.Type).
		Msg("Received IPC message")

	switch msg.Type {
	case ipc.TypeMessageSent:
		var payload ipc.MessageSentPayload
		if err := msg.ParsePayload(&payload); err != nil {
			log.Error().Err(err).Msg("Failed to parse message_sent payload")
			return
		}
		a.handleComposerMessageSent(payload)

	case ipc.TypeDraftSaved:
		var payload ipc.DraftSavedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			log.Error().Err(err).Msg("Failed to parse draft_saved payload")
			return
		}
		a.handleComposerDraftSaved(payload)

	case ipc.TypeDraftDeleted:
		var payload ipc.DraftDeletedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			log.Error().Err(err).Msg("Failed to parse draft_deleted payload")
			return
		}
		a.handleComposerDraftDeleted(payload)

	case ipc.TypeComposerReady:
		log.Info().Str("clientID", clientID).Msg("Composer window ready")

	case ipc.TypeComposerClosed:
		var payload ipc.ComposerClosedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			log.Warn().Err(err).Msg("Failed to parse composer_closed payload")
		}
		log.Info().Str("clientID", clientID).Msg("Composer window closed")

	default:
		log.Warn().Str("type", msg.Type).Msg("Unknown IPC message type")
	}
}

// handleComposerMessageSent is called when a composer successfully sends a message.
// Emits an event to the main window frontend to show a toast and refresh folders.
func (a *App) handleComposerMessageSent(payload ipc.MessageSentPayload) {
	log := logging.WithComponent("app.ipc")

	log.Info().
		Str("accountID", payload.AccountID).
		Int64("folderID", payload.FolderID).
		Msg("Composer sent message notification")

	// Emit event to frontend for toast notification and folder refresh
	wailsRuntime.EventsEmit(a.ctx, "composer:messageSent", map[string]interface{}{
		"accountId": payload.AccountID,
		"folderId":  payload.FolderID,
	})

	// Sync Sent folder to pick up the new message
	go func() {
		defer recoverPanic("app.ipc", "sync sent folder")
		if err := a.syncSentFolder(payload.AccountID); err != nil {
			log.Warn().Err(err).Msg("Failed to sync Sent folder after composer send")
		}
	}()
}

// handleComposerDraftSaved is called when a composer saves a draft.
// The composer window handles its own IMAP sync directly. We sync the Drafts
// folder here so the main window's folder view shows the newly uploaded draft.
func (a *App) handleComposerDraftSaved(payload ipc.DraftSavedPayload) {
	log := logging.WithComponent("app.ipc")

	log.Debug().
		Str("accountID", payload.AccountID).
		Str("draftID", payload.DraftID).
		Msg("Composer saved draft notification")

	// Emit event to frontend
	wailsRuntime.EventsEmit(a.ctx, "composer:draftSaved", map[string]interface{}{
		"accountId": payload.AccountID,
		"draftId":   payload.DraftID,
	})

	// Sync the Drafts folder to pick up the newly uploaded draft
	// The notification is sent after the composer's IMAP upload completes,
	// so we can sync immediately
	go func() {
		defer recoverPanic("app.ipc", "sync drafts folder")
		draftsFolder, err := a.GetSpecialFolder(payload.AccountID, folder.TypeDrafts)
		if err != nil || draftsFolder == nil {
			log.Warn().Err(err).Str("accountID", payload.AccountID).Msg("Could not find Drafts folder for sync")
			return
		}

		if err := a.SyncFolder(payload.AccountID, draftsFolder.ID); err != nil {
			log.Warn().Err(err).Str("folderID", draftsFolder.ID).Msg("Failed to sync Drafts folder")
			return
		}

		log.Debug().Str("folderID", draftsFolder.ID).Msg("Synced Drafts folder after composer draft save")
	}()
}

// handleComposerDraftDeleted is called when a composer deletes a draft.
// Emits messages:updated for immediate UI refresh and syncs the Drafts folder
// so the main window's folder view reflects the deletion.
func (a *App) handleComposerDraftDeleted(payload ipc.DraftDeletedPayload) {
	log := logging.WithComponent("app.ipc")

	log.Debug().
		Str("accountID", payload.AccountID).
		Msg("Composer deleted draft notification")

	draftsFolder, err := a.GetSpecialFolder(payload.AccountID, folder.TypeDrafts)
	if err != nil || draftsFolder == nil {
		log.Warn().Err(err).Str("accountID", payload.AccountID).Msg("Could not find Drafts folder for sync")
		return
	}

	// Notify frontend to refresh the message list immediately
	wailsRuntime.EventsEmit(a.ctx, "messages:updated", map[string]interface{}{
		"accountId": payload.AccountID,
		"folderId":  draftsFolder.ID,
	})

	// Sync the Drafts folder in background to reconcile with IMAP
	go func() {
		defer recoverPanic("app.ipc", "sync drafts folder")
		if err := a.SyncFolder(payload.AccountID, draftsFolder.ID); err != nil {
			log.Warn().Err(err).Str("folderID", draftsFolder.ID).Msg("Failed to sync Drafts folder after deletion")
			return
		}
		log.Debug().Str("folderID", draftsFolder.ID).Msg("Synced Drafts folder after composer draft delete")
	}()
}

// OpenComposerWindow spawns a new detached composer window.
// This creates a separate process of the same executable with composer mode flags.
// If mailtoURL is non-empty, the --mailto flag is appended for the composer to handle.
func (a *App) OpenComposerWindow(accountID, mode, messageID, draftID, mailtoURL string) error {
	log := logging.WithComponent("app.ipc")

	if a.ipcServer == nil || a.ipcTokenMgr == nil {
		return nil // IPC not initialized - not an error, just skip
	}

	execPath, err := os.Executable()
	if err != nil {
		return nil // Can't get executable path - skip
	}

	// Build command line arguments
	args := []string{
		"--compose",
		"--account", accountID,
		"--ipc-address", a.ipcServer.Address(),
	}

	// Add mode-specific arguments
	if draftID != "" {
		args = append(args, "--draft-id", draftID)
	} else if mode != "" && mode != "new" {
		args = append(args, "--mode", mode)
		if messageID != "" {
			args = append(args, "--message-id", messageID)
		}
	}

	// Add mailto URL if provided
	if mailtoURL != "" {
		args = append(args, "--mailto", mailtoURL)
	}

	log.Info().
		Str("execPath", execPath).
		Strs("args", args).
		Msg("Spawning composer window")

	cmd := exec.Command(execPath, args...)

	// Pass token securely via stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil // Can't create stdin pipe - skip
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil // Can't start process - skip
	}

	// Write token to stdin and close immediately
	go func() {
		defer recoverPanic("app.ipc", "write IPC token")
		token := a.ipcTokenMgr.GetToken()
		stdin.Write([]byte(token))
		stdin.Close()
	}()

	log.Info().Int("pid", cmd.Process.Pid).Msg("Composer window spawned")

	return nil
}

// BroadcastThemeChange notifies all composer windows that the theme has changed.
func (a *App) BroadcastThemeChange(theme string) {
	if a.ipcServer == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeThemeChanged, ipc.ThemeChangedPayload{
		Theme: theme,
	})
	if err != nil {
		return
	}

	a.ipcServer.Broadcast(msg)
}

// BroadcastAccountUpdated notifies all composer windows that an account was updated.
func (a *App) BroadcastAccountUpdated(accountID string) {
	if a.ipcServer == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeAccountUpdated, ipc.AccountUpdatedPayload{
		AccountID: accountID,
	})
	if err != nil {
		return
	}

	a.ipcServer.Broadcast(msg)
}

// BroadcastContactsUpdated notifies all composer windows that contacts were updated.
func (a *App) BroadcastContactsUpdated(accountID string) {
	if a.ipcServer == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeContactsUpdated, ipc.ContactsUpdatedPayload{
		AccountID: accountID,
	})
	if err != nil {
		return
	}

	a.ipcServer.Broadcast(msg)
}

// GetIPCAddress returns the IPC server address (for testing/debugging).
func (a *App) GetIPCAddress() string {
	if a.ipcServer == nil {
		return ""
	}
	return a.ipcServer.Address()
}

// GetConnectedComposers returns the number of connected composer windows.
func (a *App) GetConnectedComposers() int {
	if a.ipcServer == nil {
		return 0
	}
	return len(a.ipcServer.Clients())
}
