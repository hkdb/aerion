package backend

// Pending-write queue — Phase 2 Chunk 5.
//
// Persists user-initiated writes that failed at the transport layer (DNS,
// connection refused, timeout — anything errors.Is(err, ErrTransport))
// so they can be retried when connectivity returns. The user's local DB
// commit succeeds immediately, the queued write drains later.
//
// HTTP errors (412 conflict, 5xx) are NOT queued — they're real server
// responses that surface immediately to the user via event_crud.go's
// existing error path.
//
// Drain runs:
//   - inside Syncer.syncSourceInner, after each successful per-source
//     sync (catch-up for stuck rows).
//   - implicitly via the system:network-online + system:wake handlers
//     since those call SyncAllSources → SyncSource → syncSourceInner →
//     Drain.
//
// Order: rows for a given source replay in created_unix ASC order.
//
// Multi-edit collapse (e.g., 3 sequential offline edits to the same
// event) is NOT implemented in Chunk 5 — the queue stores each edit
// separately and replays them in order. Worst case: 1-2 spurious
// conflict toasts; end state matches the latest edit.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// PendingOpKind enumerates queueable operations.
type PendingOpKind string

const (
	PendingOpCreate PendingOpKind = "create"
	PendingOpUpdate PendingOpKind = "update"
	PendingOpDelete PendingOpKind = "delete"
)

// pendingMaxAttempts is the per-row retry budget. After this many failed
// drain attempts, Drain skips the row (logs but doesn't fail). A future
// chunk can expose retry / clear UI; for Chunk 5 the row just sits there
// awaiting that future surface.
const pendingMaxAttempts = 3

// PendingOp is the input shape to Enqueue. The caller supplies the
// full event-shaped state needed to replay the push (or delete) without
// re-reading the events row at drain time — important because
// soft-committed deletes have no events row to read.
type PendingOp struct {
	SourceID    string
	CalendarID  string
	EventID     string // local events.id; empty when no row (rare)
	Op          PendingOpKind
	Scope       EditScope // applies to update/delete; empty for create

	// CalendarURL holds the URL/ID the provider uses to address the
	// calendar (CalDAV path, Google calendarId, Microsoft Graph id).
	// Copied from Calendar.URL at enqueue time so Drain doesn't need a
	// fresh DB lookup.
	CalendarURL string

	// Event fields needed to construct the replay request. For deletes,
	// only ETag + Href + ProviderEventID are load-bearing; the rest are
	// stored for traceability.
	UID             string
	ETag            string
	Href            string
	ProviderEventID string
	Summary         string
	Description     string
	Location        string
	DTStartUnix     int64
	DTEndUnix       int64
	IsAllDay        bool
	TZName          string
	RRuleText       string
	ICSBlob         string
}

// pendingRow mirrors a row in the pending_writes table for read-back.
type pendingRow struct {
	ID              string
	SourceID        string
	CalendarID      string
	EventID         string
	Op              string
	Scope           string
	Payload         pendingPayload
	Attempt         int
	LastAttemptUnix int64
	LastError       string
	CreatedUnix     int64
}

// pendingPayload is what we JSON-marshal into pending_writes.payload_json.
// It captures everything Drain needs to replay the push (or delete) so
// the events row doesn't have to exist at drain time.
type pendingPayload struct {
	CalendarURL     string `json:"calendarUrl"`
	UID             string `json:"uid"`
	ETag            string `json:"etag,omitempty"`
	Href            string `json:"href,omitempty"`
	ProviderEventID string `json:"providerEventId,omitempty"`
	Summary         string `json:"summary,omitempty"`
	Description     string `json:"description,omitempty"`
	Location        string `json:"location,omitempty"`
	DTStartUnix     int64  `json:"dtstartUnix,omitempty"`
	DTEndUnix       int64  `json:"dtendUnix,omitempty"`
	IsAllDay        bool   `json:"isAllDay,omitempty"`
	TZName          string `json:"tzName,omitempty"`
	RRuleText       string `json:"rruleText,omitempty"`
	ICSBlob         string `json:"icsBlob,omitempty"`
}

// PendingQueue owns the pending_writes table + the drain loop. Shared
// between API (Enqueue on transport failure) and Syncer (Drain after
// sync). Goroutine-safe — methods serialize via the underlying SQLite
// connection.
type PendingQueue struct {
	store   *Store
	secrets coreapi.Secrets
	auth    coreapi.Auth
	events  coreapi.EventBus
}

// NewPendingQueue constructs a queue. events may be nil (skips the
// conflict-event publish on 412); the other deps are required.
func NewPendingQueue(store *Store, secrets coreapi.Secrets, auth coreapi.Auth, events coreapi.EventBus) *PendingQueue {
	return &PendingQueue{
		store:   store,
		secrets: secrets,
		auth:    auth,
		events:  events,
	}
}

// Enqueue persists a new pending row. Returns the row ID.
func (q *PendingQueue) Enqueue(op PendingOp) (string, error) {
	if op.SourceID == "" || op.CalendarID == "" {
		return "", errors.New("pending queue: SourceID and CalendarID required")
	}
	payload, err := json.Marshal(pendingPayload{
		CalendarURL:     op.CalendarURL,
		UID:             op.UID,
		ETag:            op.ETag,
		Href:            op.Href,
		ProviderEventID: op.ProviderEventID,
		Summary:         op.Summary,
		Description:     op.Description,
		Location:        op.Location,
		DTStartUnix:     op.DTStartUnix,
		DTEndUnix:       op.DTEndUnix,
		IsAllDay:        op.IsAllDay,
		TZName:          op.TZName,
		RRuleText:       op.RRuleText,
		ICSBlob:         op.ICSBlob,
	})
	if err != nil {
		return "", fmt.Errorf("marshal pending payload: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().Unix()
	_, err = q.store.DB().Exec(`
		INSERT INTO pending_writes (
			id, source_id, calendar_id, event_id, op, scope,
			payload_json, attempt, created_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, op.SourceID, op.CalendarID, nullIfEmpty(op.EventID),
		string(op.Op), nullIfEmpty(string(op.Scope)),
		string(payload), now,
	)
	if err != nil {
		return "", fmt.Errorf("insert pending_writes: %w", err)
	}
	// Notify the frontend so the composer's success path can surface a
	// "Saved — will sync when online" toast instead of a regular success.
	if q.events != nil {
		_ = q.events.Publish("calendar:write-queued", map[string]any{
			"sourceId": op.SourceID,
			"op":       string(op.Op),
		})
	}
	return id, nil
}

// Drain replays all pending rows for the given sourceID in
// created_unix ASC order. Stops on ctx cancellation. Per-row outcomes:
//   - success → row deleted, events row updated with new ETag +
//     ProviderEventID where applicable.
//   - ErrTransport → row stays, attempt counter bumped.
//   - ErrConflict (412) → row deleted, calendar:write-conflict event
//     published, user surfaces a "please re-edit" toast via frontend.
//   - other errors → row stays with last_error populated + attempt
//     bumped. Rows past pendingMaxAttempts are skipped on subsequent
//     drains.
//
// Returns the first hard error encountered (ctx canceled, store failure).
// Per-row transport / conflict / hard failures don't bubble up — the
// drain loop continues to the next row so one stuck write doesn't
// block others.
func (q *PendingQueue) Drain(ctx context.Context, sourceID string) error {
	if sourceID == "" {
		return errors.New("pending queue: sourceID required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := q.nextPending(sourceID)
		if err != nil {
			return err
		}
		if row == nil {
			return nil // queue empty for this source
		}
		q.processRow(ctx, *row)
	}
}

// DrainAll iterates all sources known to the store and drains each in
// turn. Used by callers that want a single entry point on the
// system:network-online path.
func (q *PendingQueue) DrainAll(ctx context.Context) error {
	sources, err := q.store.ListSources()
	if err != nil {
		return fmt.Errorf("list sources for drain: %w", err)
	}
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Skip local sources — they can't have pending writes.
		if src.Type == SourceTypeLocal {
			continue
		}
		_ = q.Drain(ctx, src.ID)
	}
	return nil
}

// nextPending returns the oldest pending row for sourceID with attempt <
// pendingMaxAttempts, or nil when none. Skips exhausted rows so they
// don't permanently block the queue.
func (q *PendingQueue) nextPending(sourceID string) (*pendingRow, error) {
	rowRes := q.store.DB().QueryRow(`
		SELECT id, source_id, calendar_id, COALESCE(event_id, ''), op,
		       COALESCE(scope, ''), payload_json, attempt,
		       COALESCE(last_attempt_unix, 0), COALESCE(last_error, ''),
		       created_unix
		FROM pending_writes
		WHERE source_id = ? AND attempt < ?
		ORDER BY created_unix ASC
		LIMIT 1`, sourceID, pendingMaxAttempts)

	var row pendingRow
	var payloadJSON string
	err := rowRes.Scan(
		&row.ID, &row.SourceID, &row.CalendarID, &row.EventID, &row.Op,
		&row.Scope, &payloadJSON, &row.Attempt,
		&row.LastAttemptUnix, &row.LastError,
		&row.CreatedUnix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan pending_writes: %w", err)
	}
	if err := json.Unmarshal([]byte(payloadJSON), &row.Payload); err != nil {
		return nil, fmt.Errorf("unmarshal pending payload: %w", err)
	}
	return &row, nil
}

// processRow dispatches one pending row to the provider, handles the
// result (delete row / bump attempt / publish conflict), and returns.
// All per-row errors are absorbed — the row's state in the table reflects
// what happened.
func (q *PendingQueue) processRow(ctx context.Context, row pendingRow) {
	src, err := q.store.GetSource(row.SourceID)
	if err != nil || src == nil {
		// Source deleted (or load failed); drop the orphaned row.
		_ = q.deleteRow(row.ID)
		return
	}

	provider := ProviderForSource(*src, ProviderDeps{
		Store:   q.store,
		Secrets: q.secrets,
		Events:  q.events,
		Auth:    q.auth,
	})

	cal := Calendar{ID: row.CalendarID, URL: row.Payload.CalendarURL}
	ev := Event{
		ID:              row.EventID,
		CalendarID:      row.CalendarID,
		UID:             row.Payload.UID,
		ETag:            row.Payload.ETag,
		Href:            row.Payload.Href,
		ProviderEventID: row.Payload.ProviderEventID,
		Summary:         row.Payload.Summary,
		Description:     row.Payload.Description,
		Location:        row.Payload.Location,
		DTStartUnix:     row.Payload.DTStartUnix,
		DTEndUnix:       row.Payload.DTEndUnix,
		IsAllDay:        row.Payload.IsAllDay,
		TZName:          row.Payload.TZName,
		RRuleText:       row.Payload.RRuleText,
		ICSBlob:         row.Payload.ICSBlob,
	}

	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var perr error
	switch PendingOpKind(row.Op) {
	case PendingOpCreate, PendingOpUpdate:
		result, presErr := provider.PushEvent(pushCtx, *src, cal, ev)
		perr = presErr
		if perr == nil {
			// Persist returned ETag + ProviderEventID onto the events row.
			_ = q.updateEventTransportFields(row.CalendarID, ev.UID, result.ETag, result.ProviderEventID)
		}
	case PendingOpDelete:
		perr = provider.DeleteRemote(pushCtx, *src, cal, ev)
	default:
		// Unknown op — drop the row.
		_ = q.deleteRow(row.ID)
		return
	}

	q.recordOutcome(row, perr)
}

// recordOutcome translates the per-row push result into table state +
// emits a conflict event when applicable.
func (q *PendingQueue) recordOutcome(row pendingRow, perr error) {
	if perr == nil {
		_ = q.deleteRow(row.ID)
		return
	}
	if errors.Is(perr, ErrConflict) {
		// Drop the row + signal the frontend so the user re-edits.
		_ = q.deleteRow(row.ID)
		if q.events != nil {
			_ = q.events.Publish("calendar:write-conflict", map[string]any{
				"sourceId":   row.SourceID,
				"calendarId": row.CalendarID,
				"eventId":    row.EventID,
				"op":         row.Op,
			})
		}
		return
	}
	// Transport or other error: bump attempt + record last_error. Drain
	// will skip rows past pendingMaxAttempts on subsequent passes.
	_ = q.bumpAttempt(row.ID, perr.Error())
}

func (q *PendingQueue) deleteRow(id string) error {
	_, err := q.store.DB().Exec(`DELETE FROM pending_writes WHERE id = ?`, id)
	return err
}

func (q *PendingQueue) bumpAttempt(id, lastError string) error {
	_, err := q.store.DB().Exec(`
		UPDATE pending_writes
		SET attempt = attempt + 1,
		    last_attempt_unix = ?,
		    last_error = ?
		WHERE id = ?`, time.Now().Unix(), lastError, id)
	return err
}

func (q *PendingQueue) updateEventTransportFields(calendarID, uid, etag, providerEventID string) error {
	_, err := q.store.DB().Exec(`
		UPDATE events
		SET etag = ?, provider_event_id = CASE WHEN ? = '' THEN provider_event_id ELSE ? END
		WHERE calendar_id = ? AND uid = ?`,
		etag, providerEventID, providerEventID, calendarID, uid)
	return err
}
