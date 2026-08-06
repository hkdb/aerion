package app

import (
	"context"
	"fmt"
	goSync "sync"
	"time"

	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/notification"
	"github.com/hkdb/aerion/internal/platform"
	"github.com/hkdb/aerion/internal/sync"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ============================================================================
// Background Email Sync (Polling + IDLE)
// ============================================================================

// initBackgroundSync initializes and starts the background sync scheduler
// and IMAP IDLE manager for real-time email notifications
func (a *App) initBackgroundSync(ctx context.Context) {
	log := logging.WithComponent("app")

	// Initialize the sync scheduler for periodic polling
	a.syncScheduler = sync.NewScheduler(a.syncEngine, a.accountStore, a.folderStore)

	// Set callback for new mail notifications
	a.syncScheduler.SetNewMailCallback(func(info sync.NewMailInfo) {
		a.handleNewMailNotification(info)
	})

	// Set callback for sync completion (so frontend clears progress)
	a.syncScheduler.SetSyncCompletedCallback(func(accountID, folderID string, err error) {
		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "folder:syncError", map[string]interface{}{
				"accountId": accountID,
				"folderId":  folderID,
				"error":     err.Error(),
			})
			return
		}
		wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
			"accountId": accountID,
			"folderId":  folderID,
		})
		// Mirror the unread count to the sidebar so badges refresh after a
		// scheduled sync (the manual SyncFolder path emits this at
		// app/sync.go:110; the scheduler path was missing it).
		if folderObj, ferr := a.folderStore.Get(folderID); ferr == nil && folderObj != nil {
			wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", map[string]int{
				folderID: folderObj.UnreadCount,
			})
		}
	})

	// Wire up network connectivity check so scheduler skips ticks when offline
	if a.networkMonitor != nil {
		a.syncScheduler.SetConnectivityCheck(a.networkMonitor.IsConnected)
	}

	// Start the polling scheduler
	a.syncScheduler.Start(ctx)
	log.Info().Msg("Email sync scheduler started")

	// Initialize the IDLE manager for real-time push notifications
	idleConfig := imap.DefaultIdleConfig()
	a.idleManager = imap.NewIdleManager(idleConfig, a.getIMAPCredentials)

	// Wire up network connectivity check so IDLE skips reconnects when offline
	if a.networkMonitor != nil {
		a.idleManager.SetConnectivityCheck(a.networkMonitor.IsConnected)
	}

	a.idleManager.Start(ctx)

	// Start IDLE for all enabled accounts if online.
	// If offline, processNetworkEvents will start them when connectivity is restored.
	if a.networkMonitor == nil || a.networkMonitor.IsConnected() {
		accounts, err := a.accountStore.List()
		if err != nil {
			log.Error().Err(err).Msg("Failed to list accounts for IDLE")
		} else {
			for _, acc := range accounts {
				if acc.Enabled {
					a.idleManager.StartAccount(acc.ID, acc.Name)
				}
			}
		}
	}

	// Start goroutine to process IDLE events
	go a.processIdleEvents(ctx)

	log.Info().Msg("IDLE manager started")
}

// processIdleEvents processes mail events from IDLE connections
func (a *App) processIdleEvents(ctx context.Context) {
	defer recoverPanic("app.idle", "process IDLE events")
	log := logging.WithComponent("app.idle")

	// timerMu guards the debounce/defer timer maps below. The event loop and
	// the self-re-arming timer callbacks (own-expunge deferral) both write them.
	var timerMu goSync.Mutex
	// Per-account debounce for flag-change re-syncs. A single read/unread action
	// on another client can emit a burst of unilateral FETCHes; coalesce them
	// into one inbox flag re-sync so IDLE stays light.
	flagDebounce := make(map[string]*time.Timer)
	// Same idea for EXPUNGE: a bulk delete/move on another client emits a burst
	// of EXPUNGEs; coalesce them into one lightweight deletion reconcile.
	expungeDebounce := make(map[string]*time.Timer)
	// Per-account deferral for new-mail syncs suppressed during our own
	// move/delete bursts (see ownExpungeEchoSuppress).
	newMailDeferred := make(map[string]*time.Timer)

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-a.idleManager.Events():
			log.Debug().
				Str("type", event.Type.String()).
				Str("accountID", event.AccountID).
				Str("folder", event.Folder).
				Uint32("count", event.Count).
				Msg("Received IDLE event")

			switch event.Type {
			case imap.EventNewMail:
				// New mail arrived - trigger sync for this account's INBOX.
				// Gmail/Outlook signal deletes via EXISTS, so this path also
				// fires on the echo of our OWN deletes — and the reconcile it
				// triggers can snapshot the server while a later delete's
				// EXPUNGE is still in flight, re-inserting a just-deleted
				// message that then flickers back into the list. While our own
				// move/delete is in flight, DEFER (not drop) so real new mail
				// still lands once the burst quiets.
				if !a.recentOwnExpunge(event.AccountID) {
					go a.handleIdleNewMail(event)
					break
				}
				log.Debug().Str("accountID", event.AccountID).Msg("Deferring IDLE new-mail sync - own delete/move in flight")
				ev := event
				var fireNewMail func()
				fireNewMail = func() {
					if a.recentOwnExpunge(ev.AccountID) {
						timerMu.Lock()
						newMailDeferred[ev.AccountID] = time.AfterFunc(idleExpungeDebounce, fireNewMail)
						timerMu.Unlock()
						return
					}
					a.handleIdleNewMail(ev)
				}
				timerMu.Lock()
				if t := newMailDeferred[ev.AccountID]; t != nil {
					t.Stop()
				}
				newMailDeferred[ev.AccountID] = time.AfterFunc(idleExpungeDebounce, fireNewMail)
				timerMu.Unlock()

			case imap.EventExpunge:
				// A message was removed on the server. RFC-strict servers (Dovecot,
				// mailcow, etc.) signal a delete with EXPUNGE only — no follow-up
				// EXISTS — so, unlike Gmail, nothing trips the new-mail path and the
				// deletion would otherwise wait for the next scheduled sync. Debounce
				// a burst (bulk delete/move) into one lightweight reconcile whose UID
				// diff removes the rows. While our OWN move/delete is in flight the
				// event is our echo — keep re-arming until the burst quiets so the
				// reconcile can't race an in-flight EXPUNGE and re-insert a
				// just-deleted message. INBOX only, matching IDLE's scope.
				acctID := event.AccountID
				var fireExpunge func()
				fireExpunge = func() {
					if a.recentOwnExpunge(acctID) {
						log.Debug().Str("accountID", acctID).Msg("Deferring IDLE expunge reconcile - own delete/move in flight")
						timerMu.Lock()
						expungeDebounce[acctID] = time.AfterFunc(idleExpungeDebounce, fireExpunge)
						timerMu.Unlock()
						return
					}
					a.handleIdleExpunge(acctID)
				}
				timerMu.Lock()
				if t := expungeDebounce[acctID]; t != nil {
					t.Stop()
				}
				expungeDebounce[acctID] = time.AfterFunc(idleExpungeDebounce, fireExpunge)
				timerMu.Unlock()

			case imap.EventFlagsChanged:
				// A flag changed on the server. Debounce, then re-sync the inbox
				// flags so read/unread state realigns in near real-time — INBOX
				// only, matching IDLE's scope.
				acctID := event.AccountID
				// Ignore the echo of our OWN flag writes so reading mail in
				// Aerion doesn't trigger a self-inflicted sync; only other
				// clients' changes re-sync.
				if a.recentOwnFlagChange(acctID) {
					log.Debug().Str("accountID", acctID).Msg("Ignoring IDLE flag echo of our own change")
					break
				}
				timerMu.Lock()
				if t := flagDebounce[acctID]; t != nil {
					t.Stop()
				}
				flagDebounce[acctID] = time.AfterFunc(idleFlagResyncDebounce, func() {
					a.handleIdleFlagsChanged(acctID)
				})
				timerMu.Unlock()
			}
		}
	}
}

// handleIdleNewMail handles a new mail event from IDLE
func (a *App) handleIdleNewMail(event imap.MailEvent) {
	defer recoverPanic("app.idle", "handle IDLE new mail")
	log := logging.WithComponent("app.idle")

	log.Info().
		Str("accountID", event.AccountID).
		Uint32("count", event.Count).
		Msg("New mail detected via IDLE, triggering sync")

	// Get the INBOX folder ID for events
	inbox, _ := a.folderStore.GetByType(event.AccountID, folder.TypeInbox)
	var folderID string
	if inbox != nil {
		folderID = inbox.ID
	}

	// Use composite key for sync tracking
	syncKey := event.AccountID + ":" + folderID

	// Check if a sync is already running for this folder - skip IDLE sync if so
	a.syncMu.Lock()
	if _, exists := a.syncContexts[syncKey]; exists {
		a.syncMu.Unlock()
		log.Debug().Str("syncKey", syncKey).Msg("Skipping IDLE sync - sync already in progress")
		return
	}
	a.syncMu.Unlock()

	// Use the scheduler's blocking sync to get new mail info
	newMailInfo, err := a.syncScheduler.SyncAccountInboxBlocking(event.AccountID)

	if err != nil {
		log.Error().Err(err).Str("accountID", event.AccountID).Msg("Failed to sync after IDLE notification")
		// Emit folder:synced to clear syncing state even on error
		if folderID != "" {
			wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
				"accountId": event.AccountID,
				"folderId":  folderID,
			})
		}
		return
	}

	// Fetch bodies in background (same as SyncFolder does)
	if folderID != "" {
		// Get account's sync period
		syncPeriodDays := 30 // default
		if acc, accErr := a.accountStore.Get(event.AccountID); accErr == nil && acc != nil {
			syncPeriodDays = acc.SyncPeriodDays
		}

		// Register IDLE sync context so manual sync can cancel it
		a.syncMu.Lock()
		// Double-check no sync started while we were processing
		if _, exists := a.syncContexts[syncKey]; exists {
			a.syncMu.Unlock()
			log.Debug().Str("syncKey", syncKey).Msg("Skipping IDLE body fetch - sync started during processing")
			return
		}
		ctx, cancel := context.WithCancel(a.ctx)
		a.syncContexts[syncKey] = cancel
		a.syncMu.Unlock()

		go func(syncCtx context.Context, syncDays int, fID string, key string) {
			var folderSynced bool // Track whether folder:synced was emitted (to avoid duplicate messages:updated)

			// Cleanup context on completion
			defer func() {
				a.syncMu.Lock()
				delete(a.syncContexts, key)
				a.syncMu.Unlock()

				// Only emit messages:updated if folder:synced wasn't already emitted
				// (both trigger identical reloads in MessageList and ConversationViewer)
				if !folderSynced {
					wailsRuntime.EventsEmit(a.ctx, "messages:updated", map[string]interface{}{
						"accountId": event.AccountID,
						"folderId":  fID,
					})
				}
				// Emit folder counts changed so sidebar unread badge updates
				if updatedFolder, err := a.folderStore.Get(fID); err == nil && updatedFolder != nil {
					wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", map[string]int{
						fID: updatedFolder.UnreadCount,
					})
				}
			}()

			// Panic recovery - ensure we always emit an event so UI doesn't get stuck
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Str("folder", fID).Msg("IDLE body fetch goroutine panicked")
					wailsRuntime.EventsEmit(a.ctx, "folder:syncError", map[string]interface{}{
						"accountId": event.AccountID,
						"folderId":  fID,
						"error":     fmt.Sprintf("body fetch panic: %v", r),
					})
				}
			}()

			bodyErr := a.syncEngine.FetchBodiesInBackground(syncCtx, event.AccountID, fID, syncDays)
			if bodyErr != nil {
				if syncCtx.Err() != nil {
					// Cancelled - not an error, emit synced
					log.Debug().Str("folder", fID).Msg("IDLE body fetch cancelled")
					folderSynced = true
					wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
						"accountId": event.AccountID,
						"folderId":  fID,
					})
				} else {
					// Actual error - emit error event
					log.Error().Err(bodyErr).Str("folder", fID).Msg("Background body fetch failed after IDLE sync")
					wailsRuntime.EventsEmit(a.ctx, "folder:syncError", map[string]interface{}{
						"accountId": event.AccountID,
						"folderId":  fID,
						"error":     bodyErr.Error(),
					})
				}
			} else {
				// Success
				folderSynced = true
				wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
					"accountId": event.AccountID,
					"folderId":  fID,
				})
			}
		}(ctx, syncPeriodDays, folderID, syncKey)
	}

	// Notify about new mail if any
	if newMailInfo != nil && newMailInfo.Count > 0 {
		a.handleNewMailNotification(*newMailInfo)
	}
}

// idleFlagResyncDebounce coalesces a burst of IDLE flag-change notifications
// (a multi-message action on another client can emit several) into one inbox
// flag re-sync. Own-change suppression already removes the burst from our own
// reads, so a short window is enough to keep the update near real-time.
const idleFlagResyncDebounce = 1 * time.Second

// idleExpungeDebounce coalesces a burst of IDLE EXPUNGE notifications (a bulk
// delete/move on another client emits one per message) into a single lightweight
// deletion reconcile.
const idleExpungeDebounce = 1 * time.Second

// ownFlagEchoSuppress is how long after Aerion writes a flag change we treat an
// incoming IDLE flag notification as the echo of our own change (and ignore it),
// so reading mail in Aerion doesn't trigger a self-inflicted re-sync.
const ownFlagEchoSuppress = 5 * time.Second

// noteOwnFlagChange records that Aerion just STOREd a flag change for an account.
func (a *App) noteOwnFlagChange(accountID string) {
	a.ownFlagMu.Lock()
	a.ownFlagChangeAt[accountID] = time.Now()
	a.ownFlagMu.Unlock()
}

// recentOwnFlagChange reports whether Aerion wrote a flag change for the account
// within the suppression window — used to skip the IDLE flag echo of our own change.
func (a *App) recentOwnFlagChange(accountID string) bool {
	a.ownFlagMu.Lock()
	defer a.ownFlagMu.Unlock()
	t, ok := a.ownFlagChangeAt[accountID]
	return ok && time.Since(t) < ownFlagEchoSuppress
}

// ownExpungeEchoSuppress is how long after Aerion runs its own move/delete IMAP
// operation we treat incoming IDLE EXPUNGE/EXISTS notifications as echoes of
// that operation and DEFER the inbox reconcile they would trigger. During a
// rapid-delete burst the reconcile's remote UID snapshot can race a still-in-
// flight EXPUNGE and re-insert a just-deleted message, which then flickers back
// into the list until the next sync removes it again. Our own deletes are
// already applied locally, so the reconcile is only needed once the burst
// quiets. Noted at both start and completion of each op, so the window extends
// past slow operations.
const ownExpungeEchoSuppress = 5 * time.Second

// noteOwnExpunge records that Aerion just ran (or is running) a move/delete
// IMAP operation for an account.
func (a *App) noteOwnExpunge(accountID string) {
	a.ownFlagMu.Lock()
	a.ownExpungeAt[accountID] = time.Now()
	a.ownFlagMu.Unlock()
}

// recentOwnExpunge reports whether Aerion ran a move/delete IMAP operation for
// the account within the suppression window.
func (a *App) recentOwnExpunge(accountID string) bool {
	a.ownFlagMu.Lock()
	defer a.ownFlagMu.Unlock()
	t, ok := a.ownExpungeAt[accountID]
	return ok && time.Since(t) < ownExpungeEchoSuppress
}

// handleIdleFlagsChanged reconciles the inbox after a flag change on the server
// (another client marked read/unread/starred) and refreshes the message list +
// sidebar badge. Lighter than handleIdleNewMail — flags only via the fast
// CONDSTORE incremental path (SyncFolderFlags), no body/header fetch. INBOX only.
func (a *App) handleIdleFlagsChanged(accountID string) {
	a.reconcileInboxFlags(accountID, 0)
}

// When a full sync is mid-flight we re-arm one short retry instead of dropping
// the flag event — if that sync already passed its flag step, our change would
// otherwise wait for the next scheduled poll.
const (
	idleFlagBusyRetryDelay = 5 * time.Second
	idleFlagBusyMaxRetries = 1
)

func (a *App) reconcileInboxFlags(accountID string, attempt int) {
	defer recoverPanic("app.idle", "handle IDLE flags changed")
	log := logging.WithComponent("app.idle")

	inbox, _ := a.folderStore.GetByType(accountID, folder.TypeInbox)
	if inbox == nil {
		return
	}
	folderID := inbox.ID

	// If a full sync is already running for the inbox it reconciles flags itself,
	// but it may have already passed its flag step — so re-arm one short retry
	// rather than dropping the event.
	syncKey := accountID + ":" + folderID
	a.syncMu.Lock()
	_, busy := a.syncContexts[syncKey]
	a.syncMu.Unlock()
	if busy {
		if attempt >= idleFlagBusyMaxRetries {
			log.Debug().Str("syncKey", syncKey).Msg("IDLE flag re-sync still busy after retry - deferring to scheduled sync")
			return
		}
		log.Debug().Str("syncKey", syncKey).Int("attempt", attempt).Msg("IDLE flag re-sync busy - retrying shortly")
		time.AfterFunc(idleFlagBusyRetryDelay, func() {
			a.reconcileInboxFlags(accountID, attempt+1)
		})
		return
	}

	log.Debug().Str("accountID", accountID).Msg("Flags changed via IDLE, reconciling inbox flags (fast path)")
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	syncErr := a.syncEngine.SyncFolderFlags(ctx, accountID, folderID)

	// Always emit folder:synced: SyncFolderFlags emits the progress that drives
	// the sidebar indicator, so we emit the matching completion (clears the bar)
	// and reload the message list with the updated flags.
	wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
		"accountId": accountID,
		"folderId":  folderID,
	})

	if syncErr != nil {
		log.Warn().Err(syncErr).Str("accountID", accountID).Msg("IDLE flag re-sync failed")
		return
	}

	// Update the sidebar unread badge.
	if updated, ferr := a.folderStore.Get(folderID); ferr == nil && updated != nil {
		wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", map[string]int{
			folderID: updated.UnreadCount,
		})
	}
}

// handleIdleExpunge reconciles the inbox after a message was expunged on the
// server (a delete/move on another client). Needed for RFC-strict servers like
// Dovecot that signal deletes via EXPUNGE only, with no EXISTS to trip the
// new-mail path. Reuses the lightweight IDLE inbox sync — its UID diff removes
// the deleted rows while the flag work stays the incremental CHANGEDSINCE path.
// INBOX only.
func (a *App) handleIdleExpunge(accountID string) {
	defer recoverPanic("app.idle", "handle IDLE expunge")
	log := logging.WithComponent("app.idle")

	inbox, _ := a.folderStore.GetByType(accountID, folder.TypeInbox)
	if inbox == nil {
		return
	}
	folderID := inbox.ID

	// If a sync is already running for the inbox, its UID diff will remove the
	// deleted rows — skip (the scheduled sync is the backstop otherwise).
	syncKey := accountID + ":" + folderID
	a.syncMu.Lock()
	_, busy := a.syncContexts[syncKey]
	a.syncMu.Unlock()
	if busy {
		log.Debug().Str("syncKey", syncKey).Msg("Skipping IDLE expunge reconcile - sync already in progress")
		return
	}

	log.Debug().Str("accountID", accountID).Msg("Messages expunged via IDLE, reconciling inbox deletions (lightweight)")
	_, syncErr := a.syncScheduler.SyncAccountInboxBlocking(accountID)

	// Always emit folder:synced: the sync emits progress that drives the sidebar
	// indicator, so we emit the matching completion (clears the bar) and reload
	// the message list with any deleted rows removed.
	wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
		"accountId": accountID,
		"folderId":  folderID,
	})

	if syncErr != nil {
		log.Warn().Err(syncErr).Str("accountID", accountID).Msg("IDLE expunge reconcile failed")
		return
	}

	// Update the sidebar unread badge.
	if updated, ferr := a.folderStore.Get(folderID); ferr == nil && updated != nil {
		wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", map[string]int{
			folderID: updated.UnreadCount,
		})
	}
}

// handleNewMailNotification handles notifications for new mail
func (a *App) handleNewMailNotification(info sync.NewMailInfo) {
	log := logging.WithComponent("app.notify")

	log.Info().
		Str("account", info.AccountName).
		Int("count", info.Count).
		Msg("New mail notification")

	// Get the most recent conversation for the notification
	var subject, fromName, fromEmail, threadID string

	inbox, err := a.folderStore.GetByType(info.AccountID, folder.TypeInbox)
	if err == nil && inbox != nil {
		// Get the most recent conversation (sorted by newest first)
		conversations, err := a.messageStore.ListConversationsByFolder(info.FolderID, 0, 1, "newest", "")
		if err == nil && len(conversations) > 0 {
			conv := conversations[0]
			subject = conv.Subject
			threadID = conv.ThreadID
			// Get sender info from participants
			if len(conv.Participants) > 0 {
				fromName = conv.Participants[0].Name
				fromEmail = conv.Participants[0].Email
			}
		}
	}

	// Send system notification
	a.sendSystemNotification(info, subject, fromName, fromEmail, threadID)
}

// sendSystemNotification sends a desktop notification for new mail
func (a *App) sendSystemNotification(info sync.NewMailInfo, subject, fromName, fromEmail, threadID string) {
	log := logging.WithComponent("app.notify")

	// Build notification title and body
	var title, body string

	if info.Count == 1 && subject != "" {
		// Single message notification
		sender := fromName
		if sender == "" {
			sender = fromEmail
		}
		title = "New email from " + sender
		body = subject
	} else {
		// Multiple messages notification
		title = "New emails"
		body = info.AccountName
	}

	// Use the notifier if available
	if a.notifier != nil {
		_, err := a.notifier.Show(notification.Notification{
			Title: title,
			Body:  body,
			Icon:  "mail-unread",
			Data: notification.NotificationData{
				AccountID: info.AccountID,
				FolderID:  info.FolderID,
				ThreadID:  threadID,
			},
		})
		if err != nil {
			log.Debug().Err(err).Msg("Failed to send notification")
		}
	}
}

// ============================================================================
// Desktop Notifications with Click Handling
// ============================================================================

// initNotifications initializes the desktop notification system with click handling
func (a *App) initNotifications(ctx context.Context) {
	log := logging.WithComponent("app.notify")

	a.notifier = notification.New("Aerion", a.useDirectDBus)

	// Set click handler. Dispatcher routes based on which NotificationData
	// fields are populated: ExtensionID set → extension click (raise window
	// + emit `extension:open` so frontend switches rail tab and processes
	// path); otherwise → mail click (existing path).
	a.notifier.SetClickHandler(func(data notification.NotificationData) {
		a.ShowWindow()

		if data.ExtensionID != "" {
			log.Info().
				Str("extensionId", data.ExtensionID).
				Str("path", data.Path).
				Msg("Notification clicked, routing to extension")
			wailsRuntime.EventsEmit(a.ctx, "extension:open", map[string]interface{}{
				"extensionId": data.ExtensionID,
				"path":        data.Path,
			})
			return
		}

		log.Info().
			Str("accountId", data.AccountID).
			Str("folderId", data.FolderID).
			Str("threadId", data.ThreadID).
			Msg("Notification clicked, navigating to message")
		wailsRuntime.EventsEmit(a.ctx, "notification:clicked", map[string]interface{}{
			"accountId": data.AccountID,
			"folderId":  data.FolderID,
			"threadId":  data.ThreadID,
		})
	})

	// Start the notification listener
	if err := a.notifier.Start(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to start notification listener (click handling may not work)")
	}
}

// ============================================================================
// Network Connectivity Monitoring
// ============================================================================

// initNetworkMonitor initializes the network connectivity monitor.
// This runs for the app's lifetime, providing event-driven (zero polling)
// connectivity state that other components can use to avoid wasted operations.
func (a *App) initNetworkMonitor(ctx context.Context) {
	log := logging.WithComponent("app.network")

	a.networkMonitor = platform.NewNetworkMonitor()

	if err := a.networkMonitor.Start(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to start network monitor — assuming online")
		return
	}

	// Process connectivity change events in background
	go a.processNetworkEvents(ctx)

	log.Info().Msg("Network connectivity monitor initialized")
}

// processNetworkEvents handles network connectivity changes:
// offline → stop IDLE, clear pool, notify frontend
// online  → clear stale connections, full sync, restart IDLE, notify frontend
func (a *App) processNetworkEvents(ctx context.Context) {
	defer recoverPanic("app.network", "process network events")
	log := logging.WithComponent("app.network")

	if a.networkMonitor == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.networkMonitor.Events():
			if !ok {
				return
			}

			if event.Connected {
				log.Info().Msg("Network connectivity restored — starting full sync")
				wailsRuntime.EventsEmit(a.ctx, "network:online", nil)
				// Bus event for Go-side subscribers (e.g., calendar Syncer).
				_ = a.coreEventBus().Publish("system:network-online", nil)
				a.syncAfterWake()
			} else {
				log.Info().Msg("Network connectivity lost — stopping IDLE and clearing pool")
				wailsRuntime.EventsEmit(a.ctx, "network:offline", nil)
				_ = a.coreEventBus().Publish("system:network-offline", nil)

				if a.idleManager != nil {
					a.idleManager.Stop()
				}
				if a.imapPool != nil {
					a.imapPool.CloseAll()
				}
			}
		}
	}
}

// ============================================================================
// Sleep/Wake Detection for Auto-Sync
// ============================================================================

// initSleepWakeMonitor initializes the sleep/wake monitor for auto-sync on wake
func (a *App) initSleepWakeMonitor(ctx context.Context) {
	log := logging.WithComponent("app.sleep-wake")

	// Create the platform-specific monitor
	a.sleepWakeMonitor = platform.NewSleepWakeMonitor()

	// Start the monitor
	if err := a.sleepWakeMonitor.Start(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to start sleep/wake monitor - auto-sync on wake disabled")
		return
	}

	// Process events in background
	go a.processSleepWakeEvents(ctx)

	log.Info().Msg("Sleep/wake monitor initialized")
}

// processSleepWakeEvents handles sleep/wake events from the monitor
func (a *App) processSleepWakeEvents(ctx context.Context) {
	defer recoverPanic("app.wake", "process sleep/wake events")
	if a.sleepWakeMonitor == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.sleepWakeMonitor.Events():
			if !ok {
				return
			}

			if event.IsSleeping {
				a.handleSystemSleep()
			} else {
				a.handleSystemWake()
			}
		}
	}
}

// handleSystemSleep handles system going to sleep
// Gracefully disconnects IMAP connections to avoid stale connection errors on wake
func (a *App) handleSystemSleep() {
	log := logging.WithComponent("app.sleep-wake")
	log.Info().Msg("System going to sleep - disconnecting IMAP connections")

	// Stop all IDLE connections gracefully
	if a.idleManager != nil {
		a.idleManager.Stop()
	}

	// Close all IMAP pool connections to avoid stale connections on wake
	if a.imapPool != nil {
		a.imapPool.CloseAll()
	}

	// Invalidate the network monitor's cached state so WaitForConnection
	// will wait for a fresh signal on wake instead of returning immediately
	if a.networkMonitor != nil {
		a.networkMonitor.Invalidate()
	}

	// Publish to the host EventBus so extensions can react (e.g., calendar's
	// Syncer pauses its in-flight HTTPS calls to avoid stale TLS sessions).
	// Lazy: bus stays uninitialized + zero-cost when no one subscribes.
	_ = a.coreEventBus().Publish("system:sleep", nil)

	log.Info().Msg("IMAP connections closed for sleep")
}

// handleSystemWake handles system waking from sleep
// Waits for network via the network monitor, then syncs all accounts and restarts IDLE
func (a *App) handleSystemWake() {
	log := logging.WithComponent("app.sleep-wake")
	log.Info().Msg("System woke from sleep - waiting for network...")

	// NOTE: We intentionally do NOT call Invalidate() or CloseAll() here.
	// handleSystemSleep already did both. Calling them here would race with
	// portal signals that may have already arrived and triggered a sync via
	// processNetworkEvents — Invalidate would reset connected=false after
	// the portal set it to true, and CloseAll would kill in-progress sync
	// connections.

	// Wait for network connectivity (event-driven, no polling).
	// Use a 30-second timeout so we don't block forever if network never comes up.
	// The network monitor will trigger a sync via processNetworkEvents if
	// connectivity is restored later.
	waitCtx, waitCancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer waitCancel()

	if a.networkMonitor == nil || !a.networkMonitor.WaitForConnection(waitCtx) {
		log.Warn().Msg("Network not available after wake — deferring to network monitor / scheduler")
		return
	}

	wailsRuntime.EventsEmit(a.ctx, "network:online", nil)

	// Publish to the host EventBus so extensions can sync on wake. Separate
	// event name from `network:online` (which is the frontend-facing name);
	// `system:wake` is the Go-side infrastructure signal. Lazy: bus stays
	// uninitialized + zero-cost when no one subscribes.
	_ = a.coreEventBus().Publish("system:wake", nil)

	log.Info().Msg("Network available — syncing all accounts after wake")
	a.syncAfterWake()
}

// syncAfterWake performs the post-wake sync: updates LastSync, runs SyncAllComplete,
// then restarts IDLE. Called from handleSystemWake and from processNetworkEvents
// when connectivity is restored. Both paths may fire on the same wake event
// (sleep/wake monitor + network online signal), so the guard ensures only one
// sync actually runs.
func (a *App) syncAfterWake() {
	log := logging.WithComponent("app.sleep-wake")

	// Guard: only one syncAfterWake can run at a time.
	// Both handleSystemWake and processNetworkEvents may call this for the
	// same wake event — the first caller runs, the second returns immediately.
	a.syncMu.Lock()
	if a.wakeSyncing {
		a.syncMu.Unlock()
		log.Debug().Msg("syncAfterWake already in progress, skipping")
		return
	}
	a.wakeSyncing = true
	a.syncMu.Unlock()

	// Cooldown: skip sync if any inbox was synced within the last 2 minutes.
	// This prevents excessive syncs when the network flaps.
	// IDLE is always restarted regardless of cooldown since handleSystemSleep
	// stops all IDLE connections.
	const syncCooldown = 2 * time.Minute
	skipSync := false
	accounts, err := a.accountStore.List()
	if err == nil {
		for _, acc := range accounts {
			if !acc.Enabled {
				continue
			}
			inbox, err := a.folderStore.GetByType(acc.ID, folder.TypeInbox)
			if err == nil && inbox != nil && inbox.LastSync != nil {
				if time.Since(*inbox.LastSync) < syncCooldown {
					log.Info().Str("account", acc.Name).Msg("Skipping full sync — last sync was recent")
					skipSync = true
					break
				}
			}
		}
	}

	if skipSync {
		// Still restart IDLE even though we're skipping the sync
		a.restartIDLE()
		a.syncMu.Lock()
		a.wakeSyncing = false
		a.syncMu.Unlock()
		return
	}

	// Clear stale pool connections in case old goroutines created some
	if a.imapPool != nil {
		a.imapPool.CloseAll()
	}

	// Update LastSync on all inbox folders BEFORE starting sync
	// This prevents the scheduler from thinking sync is overdue and interfering
	now := time.Now()
	accounts, err = a.accountStore.List()
	if err == nil {
		for _, acc := range accounts {
			if !acc.Enabled {
				continue
			}
			inbox, err := a.folderStore.GetByType(acc.ID, folder.TypeInbox)
			if err == nil && inbox != nil {
				inbox.LastSync = &now
				if err := a.folderStore.Update(inbox); err != nil {
					log.Warn().Err(err).Str("account", acc.Name).Msg("Failed to update LastSync before wake sync")
				}
			}
		}
	}

	// Trigger master sync for all accounts, then restart IDLE after.
	// IDLE is restarted AFTER sync completes to avoid pool contention:
	// IDLE detects new mail immediately and triggers its own SyncMessages +
	// FetchBodiesInBackground, consuming pool connections that SyncAllComplete
	// also needs (max 3 per account), causing 2+ minute waiter timeouts.
	go func() {
		defer recoverPanic("app.wake", "post-wake sync")
		defer func() {
			a.syncMu.Lock()
			a.wakeSyncing = false
			a.syncMu.Unlock()
		}()

		if err := a.SyncAllComplete(); err != nil {
			log.Warn().Err(err).Msg("Post-wake sync encountered errors")
		} else {
			log.Info().Msg("Post-wake sync completed successfully")
		}

		// Now restart IDLE for real-time push notifications going forward
		a.restartIDLE()
	}()

	log.Info().Msg("Post-wake sync triggered for all accounts")
}

// restartIDLE restarts IDLE connections for all enabled accounts.
func (a *App) restartIDLE() {
	log := logging.WithComponent("app.sleep-wake")

	if a.idleManager == nil {
		return
	}

	a.idleManager.Start(a.ctx)

	accounts, err := a.accountStore.List()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list accounts for IDLE restart")
		return
	}

	for _, acc := range accounts {
		if acc.Enabled {
			a.idleManager.StartAccount(acc.ID, acc.Name)
		}
	}
	log.Info().Int("accounts", len(accounts)).Msg("IDLE restarted for accounts")
}
