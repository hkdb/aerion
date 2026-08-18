package sync

import (
	"context"
	"sync"
	"time"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// NewMailInfo contains information about newly arrived mail
type NewMailInfo struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	FolderID    string `json:"folderId"`
	Subject     string `json:"subject"`
	FromName    string `json:"fromName"`
	FromEmail   string `json:"fromEmail"`
	Count       int    `json:"count"` // Number of new messages
}

// NewMailCallback is called when new mail arrives
type NewMailCallback func(info NewMailInfo)

// SyncCompletedCallback is called when a sync operation completes (success or error)
type SyncCompletedCallback func(accountID, folderID string, err error)

// Scheduler handles periodic background sync of email accounts
type Scheduler struct {
	engine       *Engine
	accountStore *account.Store
	folderStore  *folder.Store
	log          zerolog.Logger

	// Callbacks
	newMailCallback       NewMailCallback
	syncCompletedCallback SyncCompletedCallback
	isConnected           func() bool                 // optional: skip sync when offline
	suppressSync          func(accountID string) bool // optional: skip scheduled ticks during an own-delete burst

	// Control
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	running       bool
	runningMu     sync.Mutex
	checkInterval time.Duration

	// Track syncing accounts to prevent concurrent syncs
	syncing   map[string]bool
	syncingMu sync.Mutex

	// Per-account cancellation for running syncs
	syncCancels  map[string]context.CancelFunc
	syncCancelMu sync.Mutex
}

// NewScheduler creates a new sync scheduler
func NewScheduler(engine *Engine, accountStore *account.Store, folderStore *folder.Store) *Scheduler {
	return &Scheduler{
		engine:        engine,
		accountStore:  accountStore,
		folderStore:   folderStore,
		log:           logging.WithComponent("sync-scheduler"),
		checkInterval: 1 * time.Minute, // Check every minute if any account is due
		syncing:       make(map[string]bool),
		syncCancels:   make(map[string]context.CancelFunc),
	}
}

// SetNewMailCallback sets the callback for new mail notifications
func (s *Scheduler) SetNewMailCallback(callback NewMailCallback) {
	s.newMailCallback = callback
}

// SetSyncCompletedCallback sets the callback for sync completion notifications
func (s *Scheduler) SetSyncCompletedCallback(callback SyncCompletedCallback) {
	s.syncCompletedCallback = callback
}

// SetConnectivityCheck sets a function to check network connectivity.
// When set, the scheduler skips sync ticks when offline to avoid wasted
// connection attempts and unnecessary error logging.
func (s *Scheduler) SetConnectivityCheck(check func() bool) {
	s.isConnected = check
}

// SetSyncSuppressionCheck sets a per-account check consulted before scheduled
// sync ticks. When it returns true (an own delete/move's EXPUNGE is still in
// flight), the tick is skipped so the reconcile can't race the expunge and
// resurrect just-deleted messages; the next tick retries. Applies only to
// interval ticks — user-triggered syncs are never dropped by this.
func (s *Scheduler) SetSyncSuppressionCheck(check func(accountID string) bool) {
	s.suppressSync = check
}

// Start starts the background sync scheduler
func (s *Scheduler) Start(ctx context.Context) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		s.log.Warn().Msg("Scheduler already running")
		return
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	s.wg.Add(1)
	go s.run()

	s.log.Info().Msg("Email sync scheduler started")
}

// Stop stops the background sync scheduler
func (s *Scheduler) Stop() {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return
	}

	s.cancel()
	s.wg.Wait()
	s.running = false

	s.log.Info().Msg("Email sync scheduler stopped")
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	// Initial sync on startup (after a short delay to let the app initialize)
	select {
	case <-time.After(10 * time.Second):
		s.syncDueAccounts()
	case <-s.ctx.Done():
		return
	}

	// Periodic check
	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.syncDueAccounts()
		case <-s.ctx.Done():
			return
		}
	}
}

// syncDueAccounts checks all accounts and syncs those that are due
func (s *Scheduler) syncDueAccounts() {
	// Skip sync tick if we know we're offline
	if s.isConnected != nil && !s.isConnected() {
		s.log.Debug().Msg("Skipping sync tick — offline")
		return
	}

	accounts, err := s.accountStore.List()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to list accounts for sync check")
		return
	}

	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}

		// Skip manual-only accounts
		if acc.SyncInterval <= 0 {
			continue
		}

		// Check if sync is due for INBOX
		if !s.isSyncDue(acc) {
			continue
		}

		// Skip the tick while one of our own delete/move EXPUNGEs is in
		// flight — the reconcile would race it (ghost resurrection); the
		// next tick retries once the window is quiet
		if s.suppressSync != nil && s.suppressSync(acc.ID) {
			s.log.Debug().Str("account", acc.Name).Msg("Skipping scheduled sync during own-expunge window")
			continue
		}

		s.log.Debug().Str("account", acc.Name).Msg("Account is due for sync")

		// Sync in background (don't block the scheduler)
		go s.syncAccountInbox(acc)
	}
}

// isSyncDue returns true if an account's INBOX is due for sync
func (s *Scheduler) isSyncDue(acc *account.Account) bool {
	// Get the INBOX folder for this account
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		s.log.Warn().Err(err).Str("account", acc.ID).Msg("Failed to get INBOX folder")
		return true // Sync anyway to create the folder
	}
	if inbox == nil {
		return true // No INBOX yet, needs sync
	}

	// Never synced - definitely due
	if inbox.LastSync == nil {
		return true
	}

	// Calculate time since last sync
	elapsed := time.Since(*inbox.LastSync)
	interval := time.Duration(acc.SyncInterval) * time.Minute

	return elapsed >= interval
}

// syncAccountInbox syncs the INBOX for an account
func (s *Scheduler) syncAccountInbox(acc *account.Account) {
	// Prevent concurrent syncs for the same account
	s.syncingMu.Lock()
	if s.syncing[acc.ID] {
		s.syncingMu.Unlock()
		s.log.Debug().Str("account", acc.Name).Msg("Sync already in progress, skipping")
		return
	}
	s.syncing[acc.ID] = true
	s.syncingMu.Unlock()

	// Create a cancellable context with timeout for this sync operation
	// 30 minute timeout prevents syncs from running forever if connection hangs
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	s.syncCancelMu.Lock()
	s.syncCancels[acc.ID] = cancel
	s.syncCancelMu.Unlock()

	defer func() {
		// Clean up cancel function
		cancel() // Always call cancel to release timeout resources
		s.syncCancelMu.Lock()
		delete(s.syncCancels, acc.ID)
		s.syncCancelMu.Unlock()

		s.syncingMu.Lock()
		delete(s.syncing, acc.ID)
		s.syncingMu.Unlock()
	}()

	s.log.Info().Str("account", acc.Name).Msg("Starting scheduled sync for INBOX")

	// First, ensure folders are synced
	if err := s.engine.SyncFolders(ctx, acc.ID); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during folder sync")
			return
		}
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to sync folders")
		return
	}

	// Get the INBOX folder
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to get INBOX folder")
		return
	}
	if inbox == nil {
		s.log.Warn().Str("account", acc.Name).Msg("INBOX folder not found")
		return
	}

	// Get current message count before sync
	previousCount := inbox.TotalCount

	// Sync messages (use account's sync period setting)
	if err := s.engine.SyncMessages(ctx, acc.ID, inbox.ID, acc.SyncPeriodDays, false); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during message sync")
			// Notify completion even on cancel so frontend clears progress
			if s.syncCompletedCallback != nil {
				s.syncCompletedCallback(acc.ID, inbox.ID, nil)
			}
			return
		}
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to sync messages")
		// Notify completion with error so frontend clears progress
		if s.syncCompletedCallback != nil {
			s.syncCompletedCallback(acc.ID, inbox.ID, err)
		}
		return
	}

	// Get updated folder info
	updatedInbox, err := s.folderStore.Get(inbox.ID)
	if err != nil {
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to get updated INBOX folder")
		// Notify completion with error
		if s.syncCompletedCallback != nil {
			s.syncCompletedCallback(acc.ID, inbox.ID, err)
		}
		return
	}

	// Check if there are new messages
	if updatedInbox != nil && updatedInbox.TotalCount > previousCount {
		newCount := updatedInbox.TotalCount - previousCount
		s.log.Info().
			Str("account", acc.Name).
			Int("newMessages", newCount).
			Msg("New messages arrived")

		// Notify about new mail
		if s.newMailCallback != nil {
			s.newMailCallback(NewMailInfo{
				AccountID:   acc.ID,
				AccountName: acc.Name,
				FolderID:    inbox.ID,
				Count:       newCount,
			})
		}
	}

	// Notify that sync completed for Inbox
	if s.syncCompletedCallback != nil && inbox != nil {
		s.syncCompletedCallback(acc.ID, inbox.ID, nil)
	}

	// Sync additional subscribed/all folders (beyond Inbox)
	s.syncAdditionalFolders(ctx, acc, inbox)

	s.log.Debug().Str("account", acc.Name).Msg("Scheduled sync completed")
}

// syncAdditionalFolders syncs subscribed or all folders beyond Inbox,
// based on the account's SyncAllFolders setting.
func (s *Scheduler) syncAdditionalFolders(ctx context.Context, acc *account.Account, inbox *folder.Folder) {
	folders, err := s.getAccountSyncFolders(acc)
	if err != nil {
		s.log.Warn().Err(err).Str("account", acc.Name).Msg("Failed to get sync folders")
		return
	}

	// Filter out Inbox (already synced) and limit to 2 concurrent syncs
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup

	for _, f := range folders {
		// Skip Inbox — already synced above with notification handling
		if inbox != nil && f.ID == inbox.ID {
			continue
		}

		// Check context
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(f *folder.Folder) {
			defer wg.Done()
			defer func() { <-sem }()

			if syncErr := s.engine.SyncMessages(ctx, acc.ID, f.ID, acc.SyncPeriodDays, false); syncErr != nil {
				if ctx.Err() == nil {
					s.log.Warn().Err(syncErr).Str("folder", f.Path).Msg("Failed to sync additional folder")
				}
			}
			// Notify completion for this folder
			if s.syncCompletedCallback != nil {
				s.syncCompletedCallback(acc.ID, f.ID, nil)
			}
		}(f)
	}
	wg.Wait()
}

// getAccountSyncFolders returns the folders to sync for an account.
func (s *Scheduler) getAccountSyncFolders(acc *account.Account) ([]*folder.Folder, error) {
	if acc.SyncAllFolders {
		return s.folderStore.List(acc.ID)
	}
	if acc.SyncFoldersEnabled {
		return s.folderStore.ListSubscribed(acc.ID)
	}
	// Default: core folders only (backward compatible)
	coreTypes := []folder.Type{folder.TypeInbox, folder.TypeDrafts, folder.TypeSent}
	var folders []*folder.Folder
	for _, ft := range coreTypes {
		f, err := s.folderStore.GetByType(acc.ID, ft)
		if err != nil {
			continue
		}
		if f != nil {
			folders = append(folders, f)
		}
	}
	return folders, nil
}

// TriggerSync manually triggers a sync for a specific account (non-blocking)
func (s *Scheduler) TriggerSync(accountID string) {
	acc, err := s.accountStore.Get(accountID)
	if err != nil {
		s.log.Error().Err(err).Str("accountID", accountID).Msg("Failed to get account for manual sync")
		return
	}

	go s.syncAccountInbox(acc)
}

// CancelSync cancels any running sync for the specified account
func (s *Scheduler) CancelSync(accountID string) {
	s.syncCancelMu.Lock()
	if cancel, ok := s.syncCancels[accountID]; ok {
		s.log.Info().Str("accountID", accountID).Msg("Cancelling running sync")
		cancel()
	}
	s.syncCancelMu.Unlock()
}

// TriggerSyncAll manually triggers a sync for all enabled accounts (non-blocking)
func (s *Scheduler) TriggerSyncAll() {
	accounts, err := s.accountStore.List()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to list accounts for manual sync")
		return
	}

	for _, acc := range accounts {
		if acc.Enabled {
			go s.syncAccountInbox(acc)
		}
	}
}

// SyncAccountInboxBlocking syncs INBOX and returns new mail info (blocking)
// This is useful for IDLE-triggered syncs where we want to wait for completion
func (s *Scheduler) SyncAccountInboxBlocking(accountID string) (*NewMailInfo, error) {
	acc, err := s.accountStore.Get(accountID)
	if err != nil {
		return nil, err
	}

	// Prevent concurrent syncs for the same account
	s.syncingMu.Lock()
	if s.syncing[acc.ID] {
		s.syncingMu.Unlock()
		s.log.Debug().Str("account", acc.Name).Msg("Sync already in progress, skipping")
		return nil, nil
	}
	s.syncing[acc.ID] = true
	s.syncingMu.Unlock()

	// Create a cancellable context with timeout for this sync operation
	// 30 minute timeout prevents syncs from running forever if connection hangs
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	s.syncCancelMu.Lock()
	s.syncCancels[acc.ID] = cancel
	s.syncCancelMu.Unlock()

	defer func() {
		// Clean up cancel function
		cancel() // Always call cancel to release timeout resources
		s.syncCancelMu.Lock()
		delete(s.syncCancels, acc.ID)
		s.syncCancelMu.Unlock()

		s.syncingMu.Lock()
		delete(s.syncing, acc.ID)
		s.syncingMu.Unlock()
	}()

	// Get the INBOX folder
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		return nil, err
	}
	if inbox == nil {
		// Try syncing folders first
		if err := s.engine.SyncFolders(ctx, acc.ID); err != nil {
			if ctx.Err() != nil {
				s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during folder sync")
				return nil, ctx.Err()
			}
			return nil, err
		}
		inbox, err = s.folderStore.GetByType(acc.ID, folder.TypeInbox)
		if err != nil || inbox == nil {
			return nil, err
		}
	}

	// Get current message count before sync
	previousCount := inbox.TotalCount

	// Sync messages (use account's sync period setting)
	// IDLE-triggered inbox sync (new mail + cross-client deletions) — use the
	// lightweight incremental flag path; the scheduled sync above stays the
	// authoritative full reconciliation.
	if err := s.engine.SyncMessages(ctx, acc.ID, inbox.ID, acc.SyncPeriodDays, true); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during message sync")
			return nil, ctx.Err()
		}
		return nil, err
	}

	// Get updated folder info
	updatedInbox, err := s.folderStore.Get(inbox.ID)
	if err != nil {
		return nil, err
	}

	// Check if there are new messages
	if updatedInbox != nil && updatedInbox.TotalCount > previousCount {
		newCount := updatedInbox.TotalCount - previousCount
		return &NewMailInfo{
			AccountID:   acc.ID,
			AccountName: acc.Name,
			FolderID:    inbox.ID,
			Count:       newCount,
		}, nil
	}

	return nil, nil
}
