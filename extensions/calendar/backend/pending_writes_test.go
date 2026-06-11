package backend

// Tests for the PendingQueue (Phase 2 Chunk 5).
//
// Coverage:
//   - Enqueue persists with the right shape + ordering.
//   - Drain success → row deleted + events.etag/provider_event_id updated.
//   - Drain transport failure → row stays + attempt bumped.
//   - Drain conflict → row deleted + calendar:write-conflict published.
//   - Drain skips rows past pendingMaxAttempts.
//   - DrainAll skips local sources.
//
// Drain interacts with provider.PushEvent / DeleteRemote. For Google,
// we already have the fakeAuth + rewriteTransport plumbing from
// provider_google_test.go; we reuse it here so the queue tests exercise
// the real googleProvider end-to-end against an httptest server.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// --- recordingEventBus ----------------------------------------------------

// recordingEventBus implements coreapi.EventBus, capturing every Publish
// for assertions.
type recordingEventBus struct {
	mu       sync.Mutex
	captured []capturedEvent
}

type capturedEvent struct {
	Name    string
	Payload any
}

func (r *recordingEventBus) Publish(name string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.captured = append(r.captured, capturedEvent{Name: name, Payload: payload})
	return nil
}

func (r *recordingEventBus) Subscribe(_ string, _ func(any)) (coreapi.Unsubscribe, error) {
	return func() {}, nil
}

func (r *recordingEventBus) events() []capturedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedEvent, len(r.captured))
	copy(out, r.captured)
	return out
}

// --- Test helpers ---------------------------------------------------------

// newTestStore opens a Store on a temp dir. Cleaned up by t.TempDir.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// seedGoogleSource inserts a Google source + one calendar so Drain has
// targets. Returns (sourceID, calendarID).
func seedGoogleSource(t *testing.T, store *Store, calURL string) (string, string) {
	t.Helper()
	srcID := "src-g1"
	calID := "cal-g1"
	now := time.Now().Unix()
	err := store.WithTx(func(tx *sql.Tx) error {
		if err := store.CreateSourceTx(tx, Source{
			ID:        srcID,
			Type:      SourceTypeGoogle,
			Name:      "Test",
			AccountID: "acct-1",
			Enabled:   true,
			Writable:  true,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		return store.CreateCalendarTx(tx, Calendar{
			ID:          calID,
			SourceID:    srcID,
			URL:         calURL,
			DisplayName: "Personal",
			Visible:     true,
			CreatedAt:   now,
		})
	})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	return srcID, calID
}

// (no helpers needed — *sql.Tx is used directly.)

// --- Enqueue --------------------------------------------------------------

func TestPendingQueue_Enqueue(t *testing.T) {
	store := newTestStore(t)
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, fakeAuth{target: ""}, &recordingEventBus{})

	srcID, calID := seedGoogleSource(t, store, "primary")

	id, err := queue.Enqueue(PendingOp{
		SourceID:    srcID,
		CalendarID:  calID,
		Op:          PendingOpCreate,
		CalendarURL: "primary",
		UID:         "evt-uid@aerion-google",
		Summary:     "Test",
		DTStartUnix: 1700000000,
		DTEndUnix:   1700003600,
		ICSBlob:     "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == "" {
		t.Errorf("Enqueue returned empty id")
	}

	// Verify row is in the table.
	row, err := queue.nextPending(srcID)
	if err != nil {
		t.Fatalf("nextPending: %v", err)
	}
	if row == nil {
		t.Fatalf("expected pending row, got nil")
	}
	if row.Op != string(PendingOpCreate) {
		t.Errorf("row.Op = %q, want create", row.Op)
	}
	if row.Payload.UID != "evt-uid@aerion-google" {
		t.Errorf("row.Payload.UID = %q", row.Payload.UID)
	}
	if row.Attempt != 0 {
		t.Errorf("row.Attempt = %d, want 0", row.Attempt)
	}
}

// --- Drain: success path --------------------------------------------------

func TestPendingQueue_Drain_SuccessUpdatesEventAndDeletesRow(t *testing.T) {
	store := newTestStore(t)
	bus := &recordingEventBus{}

	// httptest server that returns success on POST /calendars/primary/events.
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(googleEvent{
			ID:      "server-event-id",
			ICalUID: "evt-uid@aerion-google",
			ETag:    `"server-etag"`,
		})
	}))
	defer srv.Close()

	auth := fakeAuth{target: srv.URL}
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, auth, bus)

	srcID, calID := seedGoogleSource(t, store, "primary")

	// Seed an event row matching the queued UID so the success path's
	// updateEventTransportFields has something to update.
	now := time.Now().Unix()
	_ = store.WithTx(func(tx *sql.Tx) error {
		return store.UpsertEventTx(tx, Event{
			ID: "evt-row-1", CalendarID: calID, UID: "evt-uid@aerion-google",
			Summary: "Test", DTStartUnix: now, DTEndUnix: now + 3600,
			ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n",
		})
	})

	if _, err := queue.Enqueue(PendingOp{
		SourceID: srcID, CalendarID: calID,
		Op: PendingOpCreate, CalendarURL: "primary",
		UID: "evt-uid@aerion-google", ICSBlob: minimalGoogleICS(t, "evt-uid@aerion-google"),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := queue.Drain(context.Background(), srcID); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}

	// Row should be gone.
	row, _ := queue.nextPending(srcID)
	if row != nil {
		t.Errorf("expected queue empty after success, got row %+v", row)
	}

	// Event row should have the new ETag + ProviderEventID.
	ev, err := store.GetEvent("evt-row-1")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if ev.ETag != `"server-etag"` {
		t.Errorf("event.ETag = %q, want \"server-etag\"", ev.ETag)
	}
	if ev.ProviderEventID != "server-event-id" {
		t.Errorf("event.ProviderEventID = %q, want server-event-id", ev.ProviderEventID)
	}
}

// minimalGoogleICS — small helper for queue tests to share with the
// Google-provider tests' pattern.
func minimalGoogleICS(t *testing.T, uid string) string {
	t.Helper()
	return minimalICSBlob(t, uid) // defined in provider_caldav_test.go
}

// --- Drain: transport failure → row stays --------------------------------

func TestPendingQueue_Drain_TransportFailureKeepsRowAndBumpsAttempt(t *testing.T) {
	store := newTestStore(t)
	bus := &recordingEventBus{}

	// httptest server that immediately closes the connection, triggering
	// a transport error on the client side.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("not a hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	auth := fakeAuth{target: srv.URL}
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, auth, bus)

	srcID, calID := seedGoogleSource(t, store, "primary")
	if _, err := queue.Enqueue(PendingOp{
		SourceID: srcID, CalendarID: calID,
		Op: PendingOpCreate, CalendarURL: "primary",
		UID: "evt@aerion-google", ICSBlob: minimalGoogleICS(t, "evt@aerion-google"),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := queue.Drain(context.Background(), srcID); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	// Drain loops until nextPending returns nil. Transport-failure rows
	// keep their place but the attempt counter climbs each pass; after
	// pendingMaxAttempts the row is skipped (not deleted). Query the
	// table directly so we see the row regardless of attempt level.
	var attempt int
	var lastError string
	if err := store.DB().QueryRow(
		`SELECT attempt, COALESCE(last_error, '') FROM pending_writes WHERE source_id = ?`,
		srcID,
	).Scan(&attempt, &lastError); err != nil {
		t.Fatalf("expected row to stay after transport failure, query err: %v", err)
	}
	if attempt < 1 {
		t.Errorf("attempt = %d, want >= 1", attempt)
	}
	if lastError == "" {
		t.Errorf("last_error should be populated after failure")
	}
}

// --- Drain: conflict path → row deleted, event published -----------------

func TestPendingQueue_Drain_ConflictDropsRowAndPublishesEvent(t *testing.T) {
	store := newTestStore(t)
	bus := &recordingEventBus{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	auth := fakeAuth{target: srv.URL}
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, auth, bus)

	srcID, calID := seedGoogleSource(t, store, "primary")
	if _, err := queue.Enqueue(PendingOp{
		SourceID: srcID, CalendarID: calID,
		Op:              PendingOpUpdate,
		Scope:           EditScopeAll,
		CalendarURL:     "primary",
		UID:             "evt@aerion-google",
		ProviderEventID: "existing-id",
		ETag:            `"stale"`,
		ICSBlob:         minimalGoogleICS(t, "evt@aerion-google"),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := queue.Drain(context.Background(), srcID); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	row, _ := queue.nextPending(srcID)
	if row != nil {
		t.Errorf("expected row dropped on conflict, still present")
	}

	captured := bus.events()
	found := false
	for _, e := range captured {
		if e.Name == "calendar:write-conflict" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected calendar:write-conflict event, captured: %+v", captured)
	}
}

// --- Drain: skips rows past pendingMaxAttempts ---------------------------

func TestPendingQueue_Drain_SkipsExhaustedRows(t *testing.T) {
	store := newTestStore(t)
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, fakeAuth{target: ""}, &recordingEventBus{})

	srcID, calID := seedGoogleSource(t, store, "primary")
	id, err := queue.Enqueue(PendingOp{
		SourceID: srcID, CalendarID: calID, Op: PendingOpCreate,
		CalendarURL: "primary", UID: "evt@aerion-google",
		ICSBlob: minimalGoogleICS(t, "evt@aerion-google"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Manually crank up the attempt counter past the limit.
	_, _ = store.DB().Exec(
		`UPDATE pending_writes SET attempt = ? WHERE id = ?`,
		pendingMaxAttempts, id,
	)

	// nextPending should return nil (row exhausted).
	row, err := queue.nextPending(srcID)
	if err != nil {
		t.Fatalf("nextPending: %v", err)
	}
	if row != nil {
		t.Errorf("expected nextPending to skip exhausted row, got %+v", row)
	}
}

// --- DrainAll skips local sources ----------------------------------------

func TestPendingQueue_DrainAll_SkipsLocalSources(t *testing.T) {
	store := newTestStore(t)
	queue := NewPendingQueue(store, fakeSecrets{password: "x"}, fakeAuth{target: ""}, &recordingEventBus{})

	// Seed both a local and a google source.
	now := time.Now().Unix()
	_ = store.WithTx(func(tx *sql.Tx) error {
		if err := store.CreateSourceTx(tx, Source{
			ID:        "src-local",
			Type:      SourceTypeLocal,
			Name:      "Local",
			Enabled:   true,
			Writable:  true,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		return store.CreateSourceTx(tx, Source{
			ID:        "src-google",
			Type:      SourceTypeGoogle,
			Name:      "Google",
			AccountID: "acct-1",
			Enabled:   true,
			Writable:  true,
			CreatedAt: now,
		})
	})

	// DrainAll should silently no-op on the local source and not error
	// on the google source (no pending rows = empty drain).
	if err := queue.DrainAll(context.Background()); err != nil {
		t.Errorf("DrainAll: %v", err)
	}
}
