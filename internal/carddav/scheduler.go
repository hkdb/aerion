package carddav

import (
	"context"
	"sync"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// Scheduler handles periodic background sync of CardDAV sources
type Scheduler struct {
	syncer *Syncer
	store  *Store
	log    zerolog.Logger

	// Callbacks
	isConnected func() bool // optional: skip sync when offline

	// Control
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	running       bool
	runningMu     sync.Mutex
	checkInterval time.Duration
}

// NewScheduler creates a new sync scheduler
func NewScheduler(syncer *Syncer, store *Store) *Scheduler {
	return &Scheduler{
		syncer:        syncer,
		store:         store,
		log:           logging.WithComponent("carddav-scheduler"),
		checkInterval: 1 * time.Minute, // Check every minute if any source is due
	}
}

// SetConnectivityCheck sets a function to check network connectivity.
// When set, the scheduler skips sync ticks when offline to avoid wasted
// connection attempts and unnecessary error logging.
func (s *Scheduler) SetConnectivityCheck(check func() bool) {
	s.isConnected = check
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

	s.log.Info().Msg("CardDAV sync scheduler started")
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

	s.log.Info().Msg("CardDAV sync scheduler stopped")
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	// Initial sync on startup (after a short delay to let the app initialize)
	select {
	case <-time.After(5 * time.Second):
		s.syncDueSources()
	case <-s.ctx.Done():
		return
	}

	// Periodic check
	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.syncDueSources()
		case <-s.ctx.Done():
			return
		}
	}
}

// syncDueSources checks all sources and syncs those that are due
func (s *Scheduler) syncDueSources() {
	// Skip sync tick if we know we're offline
	if s.isConnected != nil && !s.isConnected() {
		s.log.Debug().Msg("Skipping sync tick — offline")
		return
	}

	sources, err := s.store.ListSources()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to list sources for sync check")
		return
	}

	for _, source := range sources {
		if !source.Enabled {
			continue
		}

		// Skip manual-only sources
		if source.SyncInterval <= 0 {
			continue
		}

		// Check if sync is due
		if !s.isSyncDue(source) {
			continue
		}

		s.log.Info().Str("source", source.Name).Msg("Source is due for sync")

		// Sync in background (don't block the scheduler)
		go func(sourceID string) {
			if err := s.syncer.SyncSource(sourceID); err != nil {
				s.log.Error().Err(err).Str("sourceID", sourceID).Msg("Background sync failed")
			}
		}(source.ID)
	}
}

// isSyncDue returns true if a source is due for sync
func (s *Scheduler) isSyncDue(source *Source) bool {
	// Never synced - definitely due
	if source.LastSyncedAt == nil {
		return true
	}

	// Calculate time since last sync
	elapsed := time.Since(*source.LastSyncedAt)
	interval := time.Duration(source.SyncInterval) * time.Minute

	return elapsed >= interval
}

// TriggerSync manually triggers a sync for a specific source (non-blocking)
func (s *Scheduler) TriggerSync(sourceID string) {
	go func() {
		if err := s.syncer.SyncSource(sourceID); err != nil {
			s.log.Error().Err(err).Str("sourceID", sourceID).Msg("Manual sync failed")
		}
	}()
}

// TriggerSyncAll manually triggers a sync for all enabled sources (non-blocking)
func (s *Scheduler) TriggerSyncAll() {
	go func() {
		if err := s.syncer.SyncAllSources(); err != nil {
			s.log.Error().Err(err).Msg("Manual sync all failed")
		}
	}()
}
