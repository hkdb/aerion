package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Syncer drives periodic CalDAV sync for the calendar extension. Per
// source: one goroutine + one ticker at the source's sync_interval_min.
// Subscribes to `system:wake` and `system:network-online` on the host
// EventBus to trigger immediate sync after resume from sleep / network
// recovery. Errors are published to `calendar:source-error` for the
// frontend to surface.
//
// Lazy + isolated per the extension architecture: constructed inside
// CalendarBridge.ensureInit on first enabled bridge call. Disabled
// extensions never instantiate the Syncer.
type Syncer struct {
	store    *Store
	secrets  coreapi.Secrets
	events   coreapi.EventBus
	auth     coreapi.Auth   // for googleProvider + microsoftProvider's OAuth client; may be nil
	queue    *PendingQueue  // drains after each per-source sync; may be nil
	settings SettingsStore

	mu            sync.Mutex
	sourceCancels map[string]context.CancelFunc // sourceID → cancel for its goroutine
	parentCtx     context.Context
	parentCancel  context.CancelFunc

	// busySources prevents overlapping syncs on the same source — if a
	// system:wake fires while a tick is still running, we drop the wake
	// trigger for that source rather than queuing.
	busy   map[string]bool
	busyMu sync.Mutex

	unsubs []coreapi.Unsubscribe
}

// NewSyncer constructs a Syncer. Caller must call Start to begin the
// per-source goroutines + event subscriptions. auth and queue may be nil
// (CalDAV + local sources sync without auth; the per-source Drain step
// is skipped without a queue).
func NewSyncer(store *Store, secrets coreapi.Secrets, events coreapi.EventBus, settings SettingsStore, auth coreapi.Auth, queue *PendingQueue) *Syncer {
	return &Syncer{
		store:         store,
		secrets:       secrets,
		events:        events,
		auth:          auth,
		queue:         queue,
		settings:      settings,
		sourceCancels: make(map[string]context.CancelFunc),
		busy:          make(map[string]bool),
	}
}

// Start spawns a goroutine for each configured source + subscribes to
// system wake/network events. Safe to call multiple times — second call
// is effectively a no-op since the underlying state is per-source.
//
// Returns the parent context's cancel func so the caller (typically the
// extension bridge) can shut down all goroutines together if it ever
// implements clean teardown. (Currently the lifecycle pattern is "leave
// them running until process exit," matching contacts.)
func (s *Syncer) Start() context.CancelFunc {
	s.mu.Lock()
	if s.parentCtx == nil {
		s.parentCtx, s.parentCancel = context.WithCancel(context.Background())
	}
	s.mu.Unlock()

	sources, err := s.store.ListSources()
	if err == nil {
		for _, src := range sources {
			s.startSourceLoop(src.ID, time.Duration(src.SyncIntervalMin)*time.Minute)
		}
	}

	s.subscribeOnce()

	// Fire an initial sync of all sources in the background so newly-
	// opened calendars populate without waiting for the first tick.
	go func() { _ = s.SyncAllSources(s.parentCtx) }()

	return s.parentCancel
}

// AddSource starts a sync goroutine for a newly added source. Called by
// the bridge after Calendar_AddCalDAVSource succeeds.
func (s *Syncer) AddSource(sourceID string, intervalMin int) {
	if intervalMin <= 0 {
		intervalMin = 15
	}
	s.startSourceLoop(sourceID, time.Duration(intervalMin)*time.Minute)
	go func() {
		ctx, cancel := context.WithTimeout(s.parentCtx, 2*time.Minute)
		defer cancel()
		_ = s.SyncSource(ctx, sourceID)
	}()
}

// RemoveSource cancels the goroutine for a deleted source. Called by the
// bridge after Calendar_DeleteSource. Idempotent.
func (s *Syncer) RemoveSource(sourceID string) {
	s.mu.Lock()
	if cancel, ok := s.sourceCancels[sourceID]; ok {
		cancel()
		delete(s.sourceCancels, sourceID)
	}
	s.mu.Unlock()
}

// UpdateInterval restarts the per-source goroutine at the new interval so
// the change takes effect immediately rather than waiting for the next
// tick. Called by the bridge after Calendar_SetSyncInterval persists.
func (s *Syncer) UpdateInterval(sourceID string, intervalMin int) {
	if intervalMin <= 0 {
		intervalMin = 15
	}
	s.mu.Lock()
	if cancel, ok := s.sourceCancels[sourceID]; ok {
		cancel()
		delete(s.sourceCancels, sourceID)
	}
	s.mu.Unlock()
	s.startSourceLoop(sourceID, time.Duration(intervalMin)*time.Minute)
}

// SyncAllSources runs SyncSource for every configured source sequentially.
// Used for app-startup + wake/network triggers. Errors per-source are
// emitted via events and stored on the source row; the loop continues
// past individual failures.
func (s *Syncer) SyncAllSources(ctx context.Context) error {
	sources, err := s.store.ListSources()
	if err != nil {
		return fmt.Errorf("list sources for full sync: %w", err)
	}
	for _, src := range sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = s.SyncSource(ctx, src.ID)
	}
	return nil
}

// SyncSource runs one sync pass against the given source. Returns the
// first error encountered; also persists it on the source row + emits
// a `calendar:source-error` event. Gate-checks via settings.IsExtensionEnabled
// — a disabled extension that still has a running ticker (because the
// user toggled disable mid-session) becomes a no-op.
func (s *Syncer) SyncSource(ctx context.Context, sourceID string) error {
	if s.settings != nil {
		enabled, _ := s.settings.IsExtensionEnabled(extensionID)
		if !enabled {
			return nil
		}
	}

	// Local sources have no remote — sync is a no-op.
	if src, err := s.store.GetSource(sourceID); err == nil && src != nil && src.Type == SourceTypeLocal {
		return nil
	}

	if !s.tryAcquireBusy(sourceID) {
		return nil // already syncing this source; skip
	}
	defer s.releaseBusy(sourceID)

	// Tell the frontend that this source's sync is starting. Paired with
	// calendar:sync-complete (success) or calendar:source-error (failure),
	// both already fired below. Best-effort: a missing source lookup just
	// emits an empty name — the source-id alone is enough for UI gating.
	startedName := ""
	if src, err := s.store.GetSource(sourceID); err == nil && src != nil {
		startedName = src.Name
	}
	_ = s.events.Publish("calendar:sync-started", map[string]any{
		"sourceId":   sourceID,
		"sourceName": startedName,
	})

	if err := s.syncSourceInner(ctx, sourceID); err != nil {
		_ = s.store.UpdateSourceSyncStatus(sourceID, err.Error())
		_ = s.events.Publish("calendar:source-error", map[string]any{
			"sourceId": sourceID,
			"message":  err.Error(),
		})
		return err
	}
	_ = s.store.UpdateSourceSyncStatus(sourceID, "")
	// Notify subscribers that this source finished syncing — the frontend's
	// events store refetches its window cache so newly-arrived events
	// render without waiting for the next view-state change.
	_ = s.events.Publish("calendar:sync-complete", map[string]any{
		"sourceId": sourceID,
	})
	return nil
}

func (s *Syncer) syncSourceInner(ctx context.Context, sourceID string) error {
	src, err := s.store.GetSource(sourceID)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
	}

	provider := ProviderForSource(*src, ProviderDeps{
		Store:   s.store,
		Secrets: s.secrets,
		Events:  s.events,
		Auth:    s.auth,
	})

	calendars, err := s.store.ListCalendars(sourceID)
	if err != nil {
		return fmt.Errorf("list calendars: %w", err)
	}

	for i, cal := range calendars {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Per-calendar progress event — drives the sidebar footer's
		// "Syncing Murena · Personal (2/4)" label. 1-indexed for UX.
		_ = s.events.Publish("calendar:sync-progress", map[string]any{
			"sourceId":        sourceID,
			"sourceName":      src.Name,
			"calendarId":      cal.ID,
			"calendarName":    cal.DisplayName,
			"currentCalendar": i + 1,
			"totalCalendars":  len(calendars),
		})
		if err := provider.SyncCalendar(ctx, *src, cal); err != nil {
			// One bad calendar shouldn't block the rest; log via the
			// source-error event but keep going.
			_ = s.events.Publish("calendar:source-error", map[string]any{
				"sourceId":   sourceID,
				"calendarId": cal.ID,
				"message":    err.Error(),
			})
		}
	}

	// One-time writable backfill for existing CalDAV sources that
	// pre-date Chunk 1 (migration v5 left them at writable=0). Trust-
	// on-first-write: the source's PUT capability is verified the next
	// time the user tries to write. Doesn't apply to local (already
	// writable=1 from migration) or to fresh CalDAV adds (AddCalDAVSource
	// sets writable=true at insert).
	if src.Type == SourceTypeCalDAV && !src.Writable {
		_ = s.store.SetSourceWritable(src.ID, true)
	}

	// Drain any pending writes that piled up while we were offline. Runs
	// after sync so the queued replays see the latest server state.
	if s.queue != nil {
		_ = s.queue.Drain(ctx, sourceID)
	}

	return nil
}

// startSourceLoop starts (or replaces) the per-source ticker goroutine.
func (s *Syncer) startSourceLoop(sourceID string, interval time.Duration) {
	// Skip local sources — no remote to poll. Both the bootstrap pass in
	// Start() and explicit AddSource calls hit this guard.
	if src, err := s.store.GetSource(sourceID); err == nil && src != nil && src.Type == SourceTypeLocal {
		return
	}

	if interval <= 0 {
		interval = 15 * time.Minute
	}

	s.mu.Lock()
	if prev, exists := s.sourceCancels[sourceID]; exists {
		prev()
		delete(s.sourceCancels, sourceID)
	}
	ctx, cancel := context.WithCancel(s.parentCtx)
	s.sourceCancels[sourceID] = cancel
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncCtx, syncCancel := context.WithTimeout(ctx, 2*time.Minute)
				_ = s.SyncSource(syncCtx, sourceID)
				syncCancel()
			}
		}
	}()
}

// subscribeOnce wires the system event handlers exactly once. Idempotent.
var subscribeOnceGuard sync.Once

func (s *Syncer) subscribeOnce() {
	subscribeOnceGuard.Do(func() {
		wakeUnsub, _ := s.events.Subscribe("system:wake", func(_ any) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_ = s.SyncAllSources(ctx)
		})
		netUnsub, _ := s.events.Subscribe("system:network-online", func(_ any) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_ = s.SyncAllSources(ctx)
		})
		s.unsubs = append(s.unsubs, wakeUnsub, netUnsub)
	})
}

func (s *Syncer) tryAcquireBusy(sourceID string) bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	if s.busy[sourceID] {
		return false
	}
	s.busy[sourceID] = true
	return true
}

func (s *Syncer) releaseBusy(sourceID string) {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	delete(s.busy, sourceID)
}

// encodeICS re-encodes a parsed ical.Calendar back to ICS text. Needed
// because we want the raw ICS blob stored on the events row for later
// re-parse (recurrence expansion). The go-webdav library hands us a
// parsed *ical.Calendar; we re-encode for storage.
func encodeICS(cal *ical.Calendar) (string, error) {
	if cal == nil {
		return "", fmt.Errorf("encodeICS: nil calendar")
	}
	var sb strings.Builder
	enc := ical.NewEncoder(&sb)
	if err := enc.Encode(cal); err != nil {
		return "", fmt.Errorf("encodeICS: %w", err)
	}
	return sb.String(), nil
}
