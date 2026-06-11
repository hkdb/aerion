package backend

// Local event CRUD — Phase 3.
//
// ARCHITECTURAL DECISION (please read before changing this file):
//
// Local events serialize to ICS blobs at write time so all existing read
// code — rrule_expand.go, alarm.go, the calendar views, EventDetail, the
// agenda grouping, and Calendar_ListEventsInRange — works identically on
// CalDAV-fetched and locally-composed events. The only new code path is
// the write direction.
//
// Alternatives considered and rejected:
//
//   - Store local events as columns only, fork the read path → would
//     duplicate the recurrence engine, the VALARM extractor, and the
//     view-side rendering. Hundreds of LOC of negative gain.
//   - Store as JSON, parse on display → loses the index benefit of the
//     denormalized columns AND duplicates the read path the same way.
//
// The ICS blob is the source of truth. Denormalized columns (summary,
// dtstart_unix, etc.) exist for fast queries but are always rebuilt from
// the blob on write. Phase 2 CalDAV write support will reuse the same
// serializeVEVENT helper — the only addition there is a PUT to the
// server, which is transport, not encoding.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"
)

// EventInput is the shape the frontend sends for create and update operations.
//
// TZName is the IANA timezone the wall-clock components should be anchored to
// when serializing DTSTART/DTEND. Empty string → write as UTC ("Z" form);
// non-empty → write with `TZID=<TZName>` parameter so other CalDAV clients
// label the event in that zone instead of UTC.
type EventInput struct {
	CalendarID  string          `json:"calendarId"`
	Summary     string          `json:"summary"`
	Description string          `json:"description,omitempty"`
	Location    string          `json:"location,omitempty"`
	DTStartUnix int64           `json:"dtstartUnix"`
	DTEndUnix   int64           `json:"dtendUnix"`
	IsAllDay    bool            `json:"isAllDay,omitempty"`
	TZName      string          `json:"tz,omitempty"`
	Recurrence  *RecurrenceSpec `json:"recurrence,omitempty"`
	Reminder    *ReminderSpec   `json:"reminder,omitempty"`

	// Attendees + Organizer. Optional — local single-user events typically
	// have neither. Phase E adds SendUpdates to control invitation delivery
	// per-event; for now it's per-source default.
	Attendees []AttendeeInput `json:"attendees,omitempty"`
	Organizer *OrganizerInput `json:"organizer,omitempty"`

	// SendUpdates controls invitation email delivery on Google + RFC-6638
	// CalDAV. Microsoft Graph v1.0 always sends — see plan §6. Empty
	// string defaults to "all" when attendees are present. Values:
	//   "all"          — invite all attendees.
	//   "externalOnly" — only attendees outside the user's domain
	//                    (Google-specific; CalDAV treats as "all").
	//   "none"         — suppress (Google: sendUpdates=none; CalDAV:
	//                    Schedule-Reply: F header; Microsoft: ignored,
	//                    Graph sends anyway).
	SendUpdates string `json:"sendUpdates,omitempty"`
}

// RecurrenceSpec describes the RRULE shape the composer offers in v1.
type RecurrenceSpec struct {
	Freq      string `json:"freq"`      // "DAILY" | "WEEKLY" | "MONTHLY" | "YEARLY"
	UntilUnix int64  `json:"untilUnix"` // 0 = open-ended (mutually exclusive with Count)
	Count     int    `json:"count"`     // 0 = open-ended
}

// ReminderSpec describes a single DISPLAY VALARM relative to DTSTART.
type ReminderSpec struct {
	OffsetMinutes int `json:"offsetMinutes"` // minutes BEFORE DTSTART
}

// EventCreateInput aliases EventInput for type clarity in Wails bindings.
type EventCreateInput = EventInput

// EventUpdateInput is EventInput + target event ID.
type EventUpdateInput struct {
	EventID string `json:"eventId"`
	EventInput
}

// EditScope controls how recurring-event updates and deletes behave.
type EditScope string

const (
	EditScopeThis          EditScope = "this"
	EditScopeThisAndFuture EditScope = "this-and-future"
	EditScopeAll           EditScope = "all"
)

// CreateEvent serializes the input as a VEVENT, pushes to the remote (no-op
// for local), then inserts the events row plus any VALARM-driven event_alarms
// rows. The bridge calls AlarmScheduler.Reevaluate() after.
//
// Push-first ordering: if the remote PUT fails (network error, 412 conflict),
// nothing is persisted locally — user retries. Chunk 5's offline queue will
// invert this to commit-locally + queue-push-for-retry.
func (a *API) CreateEvent(in EventInput) (string, error) {
	if err := validateInput(in); err != nil {
		return "", err
	}
	cal, src, err := a.lookupCalendarAndSource(in.CalendarID)
	if err != nil {
		return "", err
	}
	if !src.Writable {
		return "", ErrNotWritable
	}

	uid := uuid.NewString() + "@aerion-" + src.Type
	icsBlob, err := serializeVEVENT(uid, in)
	if err != nil {
		return "", fmt.Errorf("serialize event: %w", err)
	}

	ev := Event{
		ID:          uuid.NewString(),
		CalendarID:  in.CalendarID,
		UID:         uid,
		Summary:     in.Summary,
		Description: in.Description,
		Location:    in.Location,
		DTStartUnix: in.DTStartUnix,
		DTEndUnix:   in.DTEndUnix,
		IsAllDay:    in.IsAllDay,
		TZName:      in.TZName,
		RRuleText:   rruleText(in.Recurrence),
		ICSBlob:     icsBlob,
		SendUpdates: in.SendUpdates, // transient write-time hint (Phase E)
		// ETag + Href empty: caldavProvider.PushEvent synthesizes the href
		// from cal.URL + uid, and captures the server's returned ETag.
	}
	// Persist attendees + organizer onto the Event struct so UpsertEventTx
	// writes them into the attendees_json + organizer_json columns + the
	// side index. WITHOUT this copy, the ICS blob (which DOES carry the
	// ATTENDEE lines because the serializer reads from `in`) is the only
	// place attendee data ends up — EventDetail / EventComposerDialog
	// read the JSON columns, not the parsed ICS, so attendees disappear
	// from the UI even though the provider received them and sent
	// invitations. (Bug: invitee got the email but Aerion's UI lost them.)
	ev.Attendees, ev.Organizer = attendeesFromInput(in)

	provider := ProviderForSource(*src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
	pushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := provider.PushEvent(pushCtx, *src, *cal, ev)
	if err == nil {
		ev.ETag = result.ETag
		ev.ProviderEventID = result.ProviderEventID
		if src.Type == SourceTypeCalDAV {
			ev.Href = joinHref(cal.URL, uid+".ics")
		}
		// Server's authoritative attendee/organizer state takes precedence.
		// On create Google/Microsoft enrich the attendees with self/organizer
		// flags and may rewrite the organizer entry; capture those.
		if result.Attendees != nil {
			ev.Attendees = result.Attendees
		}
		if result.Organizer != nil {
			ev.Organizer = result.Organizer
		}
	}
	if err != nil {
		// Transport-level failure → soft-commit + enqueue. Drain
		// (triggered by next sync / system:network-online) replays the
		// push and fills in ETag + ProviderEventID. Any other error
		// (HTTP 4xx/5xx, ErrConflict, ErrScopeNotSupported) surfaces
		// immediately.
		if !errors.Is(err, ErrTransport) || a.queue == nil {
			return "", fmt.Errorf("push event to remote: %w", err)
		}
		if _, qerr := a.queue.Enqueue(PendingOp{
			SourceID:    src.ID,
			CalendarID:  cal.ID,
			EventID:     ev.ID,
			Op:          PendingOpCreate,
			CalendarURL: cal.URL,
			UID:         ev.UID,
			Summary:     ev.Summary,
			Description: ev.Description,
			Location:    ev.Location,
			DTStartUnix: ev.DTStartUnix,
			DTEndUnix:   ev.DTEndUnix,
			IsAllDay:    ev.IsAllDay,
			TZName:      ev.TZName,
			RRuleText:   ev.RRuleText,
			ICSBlob:     ev.ICSBlob,
		}); qerr != nil {
			return "", fmt.Errorf("queue offline write: %w", qerr)
		}
	}

	err = a.store.WithTx(func(tx *sql.Tx) error {
		if err := a.store.UpsertEventTx(tx, ev); err != nil {
			return err
		}
		return a.extractAndUpsertAlarmsTx(tx, ev)
	})
	if err != nil {
		return "", fmt.Errorf("persist event: %w", err)
	}
	return ev.ID, nil
}

// UpdateEvent dispatches on scope. Non-recurring events ignore scope.
//
// CalDAV recurring + scope=this / this-and-future returns ErrScopeNotSupported
// in Chunk 2 — the VCALENDAR-composition helper (master + overrides → combined
// PUT payload) lands in a follow-up. CalDAV scope=All works because PUTting a
// new master-only VCALENDAR drops server-side overrides, matching Phase 3
// local semantics.
func (a *API) UpdateEvent(in EventUpdateInput, scope EditScope) error {
	if in.EventID == "" {
		return errors.New("calendar: event ID required")
	}
	if err := validateInput(in.EventInput); err != nil {
		return err
	}
	master, err := a.store.GetEvent(in.EventID)
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	if master == nil {
		return errors.New("calendar: event not found")
	}
	cal, src, err := a.lookupCalendarAndSource(master.CalendarID)
	if err != nil {
		return err
	}
	if !src.Writable {
		return ErrNotWritable
	}

	// Non-recurring or scope=All → straight replace + remote PUT.
	if master.RRuleText == "" || scope == EditScopeAll || scope == "" {
		return a.updateAllAndPush(*src, *cal, *master, in.EventInput)
	}

	switch scope {
	case EditScopeThis:
		return a.updateInstance(*src, *cal, *master, in.EventInput, InstanceOpUpdate, EditScopeThis)
	case EditScopeThisAndFuture:
		return a.updateInstance(*src, *cal, *master, in.EventInput, InstanceOpUpdate, EditScopeThisAndFuture)
	}
	return fmt.Errorf("calendar: unknown edit scope %q", scope)
}

// updateInstance handles scope=this and scope=this-and-future updates for
// any writable source. Push-first ordering: provider.PushInstance commits
// the remote change; then we persist the local-side state (override row
// for scope=this, new events row + updated master for scope=this-and-future)
// with the returned identifiers.
//
// localProvider's PushInstance is a no-op, so this same code path drives
// local scope=this / scope=this-and-future too — replacing the previous
// updateThis / updateThisAndFuture local-only helpers.
func (a *API) updateInstance(src Source, cal Calendar, master Event, in EventInput, kind InstanceOpKind, op EditScope) error {
	provider := ProviderForSource(src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
	pushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := provider.PushInstance(pushCtx, src, cal, PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: in.DTStartUnix,
		Op:               op,
		Kind:             kind,
		In:               in,
	})
	if err != nil {
		return fmt.Errorf("push instance to remote: %w", err)
	}

	switch {
	case op == EditScopeThis && kind == InstanceOpUpdate:
		return a.persistThisUpdate(master, in, result)
	case op == EditScopeThis && kind == InstanceOpDelete:
		return a.persistThisDelete(master, in.DTStartUnix, result)
	case op == EditScopeThisAndFuture && kind == InstanceOpUpdate:
		return a.persistThisAndFutureUpdateLocally(master, in, result)
	case op == EditScopeThisAndFuture && kind == InstanceOpDelete:
		return a.persistThisAndFutureDeleteLocally(master, in.DTStartUnix, result)
	}
	return fmt.Errorf("calendar: unsupported scope/kind combo %q/%q", op, kind)
}

// persistThisUpdate writes the override row + updates the master's ETag
// when the provider returned one (CalDAV — the master's resource was
// rewritten to embed the override).
func (a *API) persistThisUpdate(master Event, in EventInput, result PushInstanceResult) error {
	overrideBlob, err := serializeVEVENTWithRecurrenceID(master.UID, in)
	if err != nil {
		return fmt.Errorf("serialize override: %w", err)
	}
	return a.store.WithTx(func(tx *sql.Tx) error {
		if err := a.store.UpsertOverrideTx(tx, master.ID, in.DTStartUnix, overrideBlob); err != nil {
			return err
		}
		if result.MasterNewETag != "" {
			if _, err := tx.Exec(
				`UPDATE events SET etag = ? WHERE id = ?`,
				result.MasterNewETag, master.ID,
			); err != nil {
				return fmt.Errorf("update master etag: %w", err)
			}
		}
		return a.extractAndUpsertAlarmsTx(tx, master)
	})
}

// persistThisDelete adds EXDATE to the master + drops any existing
// matching override row.
func (a *API) persistThisDelete(master Event, instanceUnix int64, result PushInstanceResult) error {
	updatedICS, err := addEXDATE(master.ICSBlob, instanceUnix)
	if err != nil {
		return err
	}
	updated := master
	updated.ICSBlob = updatedICS
	if result.MasterNewETag != "" {
		updated.ETag = result.MasterNewETag
	}
	return a.store.WithTx(func(tx *sql.Tx) error {
		if err := a.store.UpsertEventTx(tx, updated); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`DELETE FROM event_recurrence_overrides WHERE event_id = ? AND recurrence_id_unix = ?`,
			master.ID, instanceUnix,
		); err != nil {
			return err
		}
		return a.extractAndUpsertAlarmsTx(tx, updated)
	})
}

// persistThisAndFutureUpdateLocally clamps the master locally + inserts
// a new events row for the future series. NewSeries identifiers come from
// the provider (or are generated locally for local sources via the same
// pattern as the legacy updateThisAndFuture).
func (a *API) persistThisAndFutureUpdateLocally(master Event, in EventInput, result PushInstanceResult) error {
	splitUnix := in.DTStartUnix

	// 1. Clamp master's RRULE locally.
	clampedRRULE := clampRRuleUntil(master.RRuleText, splitUnix-1)
	clampedICS, err := reserializeMasterICS(master, clampedRRULE)
	if err != nil {
		return err
	}
	clampedMaster := master
	clampedMaster.RRuleText = clampedRRULE
	clampedMaster.ICSBlob = clampedICS
	if result.MasterNewETag != "" {
		clampedMaster.ETag = result.MasterNewETag
	}

	// 2. New master for the future series — UID + identifiers from the
	//    provider's NewSeries (non-nil for any non-local source), or
	//    locally-generated for local.
	var newUID string
	var newETag string
	var newHref string
	var newProviderEventID string
	if result.NewSeries != nil {
		newUID = result.NewSeries.UID
		newETag = result.NewSeries.ETag
		newHref = result.NewSeries.Href
		newProviderEventID = result.NewSeries.ProviderEventID
	}
	if newUID == "" {
		newUID = uuid.NewString() + "@aerion-local"
	}
	newICS, err := serializeVEVENT(newUID, in)
	if err != nil {
		return err
	}
	newMaster := Event{
		ID:              uuid.NewString(),
		CalendarID:      master.CalendarID,
		UID:             newUID,
		ETag:            newETag,
		Href:            newHref,
		ProviderEventID: newProviderEventID,
		Summary:         in.Summary,
		Description:     in.Description,
		Location:        in.Location,
		DTStartUnix:     in.DTStartUnix,
		DTEndUnix:       in.DTEndUnix,
		IsAllDay:        in.IsAllDay,
		RRuleText:       rruleText(in.Recurrence),
		ICSBlob:         newICS,
	}

	return a.store.WithTx(func(tx *sql.Tx) error {
		if err := a.store.UpsertEventTx(tx, clampedMaster); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`DELETE FROM event_recurrence_overrides WHERE event_id = ? AND recurrence_id_unix >= ?`,
			master.ID, splitUnix,
		); err != nil {
			return err
		}
		if err := a.store.UpsertEventTx(tx, newMaster); err != nil {
			return err
		}
		if err := a.extractAndUpsertAlarmsTx(tx, clampedMaster); err != nil {
			return err
		}
		return a.extractAndUpsertAlarmsTx(tx, newMaster)
	})
}

// persistThisAndFutureDeleteLocally clamps the master locally + drops
// future overrides. No new series row.
func (a *API) persistThisAndFutureDeleteLocally(master Event, splitUnix int64, result PushInstanceResult) error {
	clampedRRULE := clampRRuleUntil(master.RRuleText, splitUnix-1)
	clampedICS, err := reserializeMasterICS(master, clampedRRULE)
	if err != nil {
		return err
	}
	updated := master
	updated.RRuleText = clampedRRULE
	updated.ICSBlob = clampedICS
	if result.MasterNewETag != "" {
		updated.ETag = result.MasterNewETag
	}
	return a.store.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`DELETE FROM event_recurrence_overrides WHERE event_id = ? AND recurrence_id_unix >= ?`,
			master.ID, splitUnix,
		); err != nil {
			return err
		}
		if err := a.store.UpsertEventTx(tx, updated); err != nil {
			return err
		}
		return a.extractAndUpsertAlarmsTx(tx, updated)
	})
}

// DeleteEvent removes an event with scope semantics symmetric to UpdateEvent.
//
// CalDAV recurring + scope=this / this-and-future returns ErrScopeNotSupported
// (same reasoning as UpdateEvent). CalDAV scope=All issues an HTTP DELETE with
// If-Match for optimistic concurrency, then CASCADEs the local rows.
func (a *API) DeleteEvent(eventID string, scope EditScope) error {
	if eventID == "" {
		return errors.New("calendar: event ID required")
	}
	master, err := a.store.GetEvent(eventID)
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	if master == nil {
		return nil // idempotent
	}
	cal, src, err := a.lookupCalendarAndSource(master.CalendarID)
	if err != nil {
		return err
	}
	if !src.Writable {
		return ErrNotWritable
	}

	// Non-recurring or scope=All → delete remote first, then cascade local.
	if master.RRuleText == "" || scope == EditScopeAll || scope == "" {
		provider := ProviderForSource(*src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		derr := provider.DeleteRemote(delCtx, *src, *cal, *master)
		if derr != nil {
			if !errors.Is(derr, ErrTransport) || a.queue == nil {
				return fmt.Errorf("delete event on remote: %w", derr)
			}
			// Soft-commit delete: queue the remote DELETE so Drain
			// replays when connectivity returns. Payload carries enough
			// to reach the resource (ProviderEventID for Google/Microsoft,
			// Href for CalDAV) even after the local row is gone.
			if _, qerr := a.queue.Enqueue(PendingOp{
				SourceID:        src.ID,
				CalendarID:      cal.ID,
				EventID:         master.ID,
				Op:              PendingOpDelete,
				Scope:           EditScopeAll,
				CalendarURL:     cal.URL,
				UID:             master.UID,
				ETag:            master.ETag,
				Href:            master.Href,
				ProviderEventID: master.ProviderEventID,
			}); qerr != nil {
				return fmt.Errorf("queue offline delete: %w", qerr)
			}
		}
		// CASCADE removes overrides + alarms.
		return a.store.WithTx(func(tx *sql.Tx) error {
			return a.store.DeleteEventByUIDTx(tx, master.CalendarID, master.UID)
		})
	}

	// For recurring "this" / "this-and-future", the caller's intent is
	// based on a specific instance. The bridge passes the original
	// instance start via master.DTStartUnix as a placeholder; a future
	// bridge update can thread a specific instance time through when the
	// frontend selects an instance other than the master's first.
	splitUnix := master.DTStartUnix

	// Push delete to remote first via PushInstance, then persist locally.
	// Symmetric with UpdateEvent's scope=this / scope=this-and-future
	// branches.
	deleteIn := EventInput{
		Summary:     master.Summary,
		Description: master.Description,
		Location:    master.Location,
		DTStartUnix: splitUnix,
		DTEndUnix:   master.DTEndUnix,
		IsAllDay:    master.IsAllDay,
	}

	switch scope {
	case EditScopeThis:
		return a.updateInstance(*src, *cal, *master, deleteIn, InstanceOpDelete, EditScopeThis)
	case EditScopeThisAndFuture:
		return a.updateInstance(*src, *cal, *master, deleteIn, InstanceOpDelete, EditScopeThisAndFuture)
	}
	return fmt.Errorf("calendar: unknown edit scope %q", scope)
}

// updateAllAndPush builds the new event, pushes to remote (no-op for local),
// then persists. Used for non-recurring events (any scope) and recurring
// events with scope=All (drops overrides — matches Phase 3 local semantics
// and CalDAV's whole-VCALENDAR-replacement semantics).
func (a *API) updateAllAndPush(src Source, cal Calendar, master Event, in EventInput) error {
	icsBlob, err := serializeVEVENT(master.UID, in)
	if err != nil {
		return fmt.Errorf("serialize event: %w", err)
	}
	ev := master // preserve ID, UID, CalendarID, Href; ETag flows to PushEvent as If-Match
	ev.Summary = in.Summary
	ev.Description = in.Description
	ev.Location = in.Location
	ev.DTStartUnix = in.DTStartUnix
	ev.DTEndUnix = in.DTEndUnix
	ev.IsAllDay = in.IsAllDay
	ev.TZName = in.TZName
	ev.RRuleText = rruleText(in.Recurrence)
	ev.ICSBlob = icsBlob
	ev.SendUpdates = in.SendUpdates // transient write-time hint (Phase E)
	// Overwrite master's persisted attendees + organizer with the user's
	// edit. Same reason as CreateEvent: without this copy the JSON columns
	// keep stale values (or empty), even though the new ICS blob carries
	// the right ATTENDEE lines and the provider receives them.
	ev.Attendees, ev.Organizer = attendeesFromInput(in)

	provider := ProviderForSource(src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
	pushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := provider.PushEvent(pushCtx, src, cal, ev)
	if err == nil {
		ev.ETag = result.ETag
		if result.ProviderEventID != "" {
			ev.ProviderEventID = result.ProviderEventID
		}
		// Server's authoritative attendee/organizer state takes precedence
		// over what we sent — Google + Microsoft reset attendee
		// responseStatus to needsAction on significant changes (time
		// moves, summary edits), and we want that reset to surface
		// locally without waiting for the next sync. CalDAV PUT returns
		// no body so result.Attendees/.Organizer are nil there; the
		// existing values from attendeesFromInput stay in place and the
		// next sync reconciles any server-side changes.
		if result.Attendees != nil {
			ev.Attendees = result.Attendees
		}
		if result.Organizer != nil {
			ev.Organizer = result.Organizer
		}
	}
	if err != nil {
		if !errors.Is(err, ErrTransport) || a.queue == nil {
			return fmt.Errorf("push event to remote: %w", err)
		}
		// Soft-commit update: queued row carries the master's old ETag +
		// ProviderEventID + Href so Drain can replay with If-Match.
		if _, qerr := a.queue.Enqueue(PendingOp{
			SourceID:        src.ID,
			CalendarID:      cal.ID,
			EventID:         ev.ID,
			Op:              PendingOpUpdate,
			Scope:           EditScopeAll,
			CalendarURL:     cal.URL,
			UID:             ev.UID,
			ETag:            master.ETag,
			Href:            ev.Href,
			ProviderEventID: ev.ProviderEventID,
			Summary:         ev.Summary,
			Description:     ev.Description,
			Location:        ev.Location,
			DTStartUnix:     ev.DTStartUnix,
			DTEndUnix:       ev.DTEndUnix,
			IsAllDay:        ev.IsAllDay,
			TZName:          ev.TZName,
			RRuleText:       ev.RRuleText,
			ICSBlob:         ev.ICSBlob,
		}); qerr != nil {
			return fmt.Errorf("queue offline write: %w", qerr)
		}
	}

	return a.store.WithTx(func(tx *sql.Tx) error {
		if err := a.store.UpsertEventTx(tx, ev); err != nil {
			return err
		}
		// Drop ALL overrides — they were attached to the old RRULE shape
		// and may not map cleanly to the new occurrence set.
		if _, err := tx.Exec(
			`DELETE FROM event_recurrence_overrides WHERE event_id = ?`, ev.ID); err != nil {
			return err
		}
		return a.extractAndUpsertAlarmsTx(tx, ev)
	})
}

// --- Internal helpers ---------------------------------------------------------

func validateInput(in EventInput) error {
	if in.CalendarID == "" {
		return errors.New("calendar: calendar ID required")
	}
	if in.Summary == "" {
		return errors.New("calendar: summary required")
	}
	if in.DTStartUnix == 0 {
		return errors.New("calendar: dtstart required")
	}
	if in.DTEndUnix == 0 {
		return errors.New("calendar: dtend required")
	}
	if in.DTEndUnix < in.DTStartUnix {
		return errors.New("calendar: dtend must be >= dtstart")
	}
	if in.Recurrence == nil {
		return nil
	}
	switch in.Recurrence.Freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return fmt.Errorf("calendar: invalid recurrence freq %q", in.Recurrence.Freq)
	}
	if in.Recurrence.UntilUnix != 0 && in.Recurrence.Count != 0 {
		return errors.New("calendar: recurrence UntilUnix and Count are mutually exclusive")
	}
	return nil
}

func (a *API) lookupCalendarAndSource(calendarID string) (*Calendar, *Source, error) {
	sources, err := a.store.ListSources()
	if err != nil {
		return nil, nil, err
	}
	for i := range sources {
		cals, err := a.store.ListCalendars(sources[i].ID)
		if err != nil {
			continue
		}
		for j := range cals {
			if cals[j].ID == calendarID {
				return &cals[j], &sources[i], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("calendar: calendar %q not found", calendarID)
}

func rruleText(spec *RecurrenceSpec) string {
	if spec == nil {
		return ""
	}
	parts := []string{"FREQ=" + spec.Freq}
	if spec.UntilUnix > 0 {
		parts = append(parts, "UNTIL="+formatICSDateTime(time.Unix(spec.UntilUnix, 0)))
	}
	if spec.Count > 0 {
		parts = append(parts, fmt.Sprintf("COUNT=%d", spec.Count))
	}
	return strings.Join(parts, ";")
}

// setEventStartEnd writes DTSTART + DTEND on the event, choosing between
// DATE form (all-day) and DATE-TIME form (timed). When in.TZName is a
// loadable IANA zone, the timed-event branch writes wall-clock + TZID
// (so peers like Nextcloud's web UI label the event in that zone rather
// than UTC); otherwise it falls back to UTC "Z" form.
func setEventStartEnd(event *ical.Event, in EventInput) {
	if in.IsAllDay {
		setDateValue(event, ical.PropDateTimeStart, in.DTStartUnix)
		setDateValue(event, ical.PropDateTimeEnd, in.DTEndUnix)
		return
	}
	loc := time.UTC
	if in.TZName != "" {
		if l, err := time.LoadLocation(in.TZName); err == nil {
			loc = l
		}
	}
	event.Props.SetDateTime(ical.PropDateTimeStart, time.Unix(in.DTStartUnix, 0).In(loc))
	event.Props.SetDateTime(ical.PropDateTimeEnd, time.Unix(in.DTEndUnix, 0).In(loc))
}

// serializeVEVENT builds a single-event VCALENDAR for events.ics_blob.
func serializeVEVENT(uid string, in EventInput) (string, error) {
	event := ical.NewEvent()
	event.Props.SetText(ical.PropUID, uid)
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	event.Props.SetText(ical.PropSummary, in.Summary)
	if in.Description != "" {
		event.Props.SetText(ical.PropDescription, in.Description)
	}
	if in.Location != "" {
		event.Props.SetText(ical.PropLocation, in.Location)
	}

	setEventStartEnd(event, in)

	if rt := rruleText(in.Recurrence); rt != "" {
		setRRuleText(event.Props, rt)
	}

	if in.Reminder != nil {
		alarm := &ical.Component{Name: ical.CompAlarm, Props: ical.Props{}}
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.Value = fmt.Sprintf("-PT%dM", in.Reminder.OffsetMinutes)
		alarm.Props.Add(trigger)
		alarm.Props.SetText(ical.PropDescription, in.Summary)
		event.Component.Children = append(event.Component.Children, alarm)
	}

	// ORGANIZER + ATTENDEE properties. Emitted after the base props so they
	// land in a consistent position in the wire form.
	emitAttendeesIntoVEVENT(event, in.Organizer, in.Attendees)

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Aerion//Calendar Extension//EN")
	cal.Children = append(cal.Children, event.Component)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// setDateValue stamps a DATE-only property for all-day events.
func setDateValue(event *ical.Event, propName string, unix int64) {
	prop := ical.NewProp(propName)
	prop.Value = time.Unix(unix, 0).UTC().Format("20060102")
	prop.Params = ical.Params{}
	prop.Params.Set(ical.ParamValue, "DATE")
	event.Props.Set(prop)
}

func formatICSDateTime(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// setRRuleText writes the RRULE value WITHOUT the TEXT-type semicolon escape
// go-ical applies by default via SetText. The RRULE property uses RECUR type
// per RFC 5545 §3.3.10 — semicolons are part-separators, not escaped.
func setRRuleText(props ical.Props, rt string) {
	prop := ical.NewProp(ical.PropRecurrenceRule)
	prop.SetValueType(ical.ValueRecurrence)
	prop.Value = rt
	props.Set(prop)
}

// extractAndUpsertAlarmsTx re-parses the event's blob, extracts VALARMs,
// and upserts them into event_alarms. Keeps create/update → alarms atomic.
func (a *API) extractAndUpsertAlarmsTx(tx *sql.Tx, ev Event) error {
	overrides, err := a.store.ListOverrides(ev.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	instances, err := ExpandInRange(ev, overrides, now, now.Add(7*24*time.Hour))
	if err != nil {
		return fmt.Errorf("expand for alarms: %w", err)
	}
	alarms, err := ExtractAlarms(ev, overrides, instances)
	if err != nil {
		return fmt.Errorf("extract alarms: %w", err)
	}
	for _, alm := range alarms {
		if err := a.store.UpsertAlarmTx(tx, alm); err != nil {
			return err
		}
	}
	return nil
}

// --- ICS manipulation helpers -------------------------------------------------

// serializeVEVENTWithRecurrenceID is like serializeVEVENT but uses the
// caller-supplied uid (same as the master's per RFC 5545 §3.8.4.4) and
// adds RECURRENCE-ID = DTStartUnix so the override binds to a specific
// occurrence.
func serializeVEVENTWithRecurrenceID(uid string, in EventInput) (string, error) {
	// Overrides are single-instance, never recurring.
	in.Recurrence = nil

	icsBlob, err := serializeVEVENT(uid, in)
	if err != nil {
		return "", err
	}
	cal, err := ical.NewDecoder(strings.NewReader(icsBlob)).Decode()
	if err != nil {
		return "", err
	}
	if len(cal.Events()) == 0 {
		return "", errors.New("calendar: re-encoded event has no VEVENT")
	}
	ev := cal.Events()[0]
	recIDProp := ical.NewProp(ical.PropRecurrenceID)
	recIDProp.Value = formatICSDateTime(time.Unix(in.DTStartUnix, 0))
	ev.Props.Set(recIDProp)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// reserializeMasterICS rewrites the RRULE on an existing master's ICS blob.
func reserializeMasterICS(master Event, newRRULE string) (string, error) {
	cal, err := ical.NewDecoder(strings.NewReader(master.ICSBlob)).Decode()
	if err != nil {
		return "", err
	}
	if len(cal.Events()) == 0 {
		return "", errors.New("calendar: master ICS has no VEVENT")
	}
	ev := cal.Events()[0]
	if newRRULE == "" {
		ev.Props.Del(ical.PropRecurrenceRule)
	}
	if newRRULE != "" {
		setRRuleText(ev.Props, newRRULE)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// addEXDATE injects an EXDATE property onto the master's VEVENT.
func addEXDATE(icsBlob string, instanceUnix int64) (string, error) {
	cal, err := ical.NewDecoder(strings.NewReader(icsBlob)).Decode()
	if err != nil {
		return "", err
	}
	if len(cal.Events()) == 0 {
		return "", errors.New("calendar: master ICS has no VEVENT")
	}
	ev := cal.Events()[0]
	exdateStr := formatICSDateTime(time.Unix(instanceUnix, 0))

	existing := ev.Props.Get(ical.PropExceptionDates)
	if existing != nil {
		existing.Value = existing.Value + "," + exdateStr
	}
	if existing == nil {
		p := ical.NewProp(ical.PropExceptionDates)
		p.Value = exdateStr
		ev.Props.Set(p)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// clampRRuleUntil returns the RRULE text with an UNTIL=<unix> clause added,
// replacing any existing UNTIL or COUNT.
func clampRRuleUntil(rrule string, untilUnix int64) string {
	if rrule == "" {
		return ""
	}
	body := strings.TrimPrefix(rrule, "RRULE:")
	parts := strings.Split(body, ";")
	out := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		upper := strings.ToUpper(strings.SplitN(p, "=", 2)[0])
		if upper == "UNTIL" || upper == "COUNT" {
			continue
		}
		out = append(out, p)
	}
	out = append(out, "UNTIL="+formatICSDateTime(time.Unix(untilUnix, 0)))
	return strings.Join(out, ";")
}
