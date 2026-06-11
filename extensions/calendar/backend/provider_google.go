package backend

// Google Calendar provider — Phase 2 Chunk 3.
//
// Implements the Provider interface for Google Calendar (API v3) using
// coreapi.Auth's OAuth-vended *http.Client. Translation between Google's
// event JSON and Aerion's ICS blob lives in provider_google_translate.go.
//
// Storage model unchanged from CalDAV: events.ics_blob holds a single-VEVENT
// VCALENDAR per row, event_recurrence_overrides holds per-instance overrides.
// `calendars.url` stores Google's calendarId; `calendars.ctag` stores the
// incremental syncToken.
//
// Chunk 3 scope:
//   - SyncCalendar: incremental via syncToken; falls back to full sync on
//     410 Gone. Master events fully supported; events with recurringEventId
//     are skipped with a log line (per-instance override sync is a
//     follow-up; rrule_expand still drives correct master-level expansion).
//   - PushEvent: POST for create, PATCH for update with If-Match.
//   - DeleteRemote: DELETE with If-Match; 404 idempotent; 412 → ErrConflict.
//   - scope=this / scope=this-and-future deferred to a follow-up (parity
//     with CalDAV Chunk 2's ErrScopeNotSupported).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

const (
	googleAPIBase   = "https://www.googleapis.com/calendar/v3"
	googleRWScope   = "https://www.googleapis.com/auth/calendar"
	googleRWReason  = "Sync and edit your Google Calendar events"
	googleSyncLimit = 250 // events per page; Google's max is 2500 but we keep batches small
)

type googleProvider struct {
	store *Store
	auth  coreapi.Auth
}

func (googleProvider) Capabilities() Capabilities {
	return Capabilities{
		CanWrite:        true,
		CanDeleteSeries: true,
		CanSetReminders: true,
	}
}

// --- HTTP client + helpers -------------------------------------------------

func (p googleProvider) httpClient(src Source) (*http.Client, error) {
	if p.auth == nil {
		return nil, fmt.Errorf("googleProvider: no Auth handle (extension built without coreapi.Core)")
	}
	if src.AccountID == "" {
		return nil, fmt.Errorf("googleProvider: source %q has no account ID", src.ID)
	}
	return p.auth.HTTPClient(src.AccountID, []coreapi.AuthScope{
		{Resource: googleRWScope, Reason: googleRWReason},
	})
}

// --- Sync ------------------------------------------------------------------

// SyncCalendar fetches new+changed events from Google for the given calendar
// and upserts them locally. Uses syncToken stored in calendars.ctag for
// incremental sync; resets to a full sync on 410 Gone (Google's signal that
// the token is too old).
func (p googleProvider) SyncCalendar(ctx context.Context, src Source, cal Calendar) error {
	client, err := p.httpClient(src)
	if err != nil {
		return err
	}

	syncToken := cal.Ctag
	nextSyncToken, syncErr := p.syncOnce(ctx, client, cal, syncToken)
	if syncErr == errGoogleSyncTokenInvalid && syncToken != "" {
		// Reset + full sync.
		nextSyncToken, syncErr = p.syncOnce(ctx, client, cal, "")
	}
	if syncErr != nil {
		return syncErr
	}

	if nextSyncToken == "" {
		return nil
	}
	return p.store.WithTx(func(tx *sql.Tx) error {
		return p.store.UpdateCalendarCtagTx(tx, cal.ID, nextSyncToken, time.Now().Unix())
	})
}

var errGoogleSyncTokenInvalid = fmt.Errorf("google sync token invalid (410 Gone)")

// syncOnce drives the paginated list-events loop. Returns the final
// nextSyncToken (suitable for storage), or empty when the server didn't
// return one (multi-page sweep is still in progress per pageToken).
func (p googleProvider) syncOnce(ctx context.Context, client *http.Client, cal Calendar, syncToken string) (string, error) {
	pageToken := ""
	var nextSyncToken string

	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		resp, err := p.fetchEventsPage(ctx, client, cal, syncToken, pageToken)
		if err != nil {
			return "", err
		}

		if err := p.persistEventsPage(cal, resp.Items); err != nil {
			return "", err
		}

		if resp.NextPageToken != "" {
			pageToken = resp.NextPageToken
			continue
		}
		nextSyncToken = resp.NextSyncToken
		return nextSyncToken, nil
	}
}

type googleEventsListResponse struct {
	Items         []googleEvent `json:"items"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
	NextSyncToken string        `json:"nextSyncToken,omitempty"`
}

func (p googleProvider) fetchEventsPage(ctx context.Context, client *http.Client, cal Calendar, syncToken, pageToken string) (*googleEventsListResponse, error) {
	u := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) + "/events"
	q := url.Values{}
	q.Set("maxResults", fmt.Sprintf("%d", googleSyncLimit))
	if syncToken != "" {
		q.Set("syncToken", syncToken)
	}
	if syncToken == "" {
		q.Set("showDeleted", "true") // initial sync needs deletes to seed state
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	u = u + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build list request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google events.list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return nil, errGoogleSyncTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("google events.list %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	var out googleEventsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode events.list: %w", err)
	}
	return &out, nil
}

// persistEventsPage upserts the master events in items into the events
// table. Cancelled masters are deleted. Per-instance override events
// (those with recurringEventId) are skipped in Chunk 3 — a follow-up
// adds override handling.
func (p googleProvider) persistEventsPage(cal Calendar, items []googleEvent) error {
	return p.store.WithTx(func(tx *sql.Tx) error {
		for _, item := range items {
			if item.RecurringEventID != "" {
				// Override event. Chunk 3 punts; rrule_expand will still
				// produce the unmodified occurrence for the master's
				// RRULE expansion, which is "close enough" for most cases.
				continue
			}
			if item.Status == "cancelled" {
				if item.ICalUID == "" {
					continue
				}
				if err := p.store.DeleteEventByUIDTx(tx, cal.ID, item.ICalUID); err != nil {
					return err
				}
				continue
			}
			blob, err := translateGoogleEventToICS(item)
			if err != nil {
				// Skip malformed events rather than abort the whole sync.
				continue
			}

			// Re-use existing event row's ID when present (so foreign keys
			// on overrides + alarms stay stable across syncs).
			eventID := uuid.New().String()
			if existing, lerr := p.lookupEventIDByUID(cal.ID, item.ICalUID); lerr == nil && existing != "" {
				eventID = existing
			}

			ev := Event{
				ID:              eventID,
				CalendarID:      cal.ID,
				UID:             item.ICalUID,
				ETag:            item.ETag,
				ProviderEventID: item.ID,
				Summary:         item.Summary,
				Description:     item.Description,
				Location:        item.Location,
				ICSBlob:         blob,
			}
			fillDenormalizedFieldsFromICS(&ev, blob)
			if len(item.Recurrence) > 0 {
				for _, line := range item.Recurrence {
					if strings.HasPrefix(line, "RRULE:") {
						ev.RRuleText = strings.TrimPrefix(line, "RRULE:")
						break
					}
				}
			}

			if err := p.store.UpsertEventTx(tx, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// lookupEventIDByUID returns the existing local row ID for a (calendarID,
// uid) pair, or empty when missing. Mirrors the helper Syncer used pre-
// Chunk 2; lives here now that sync logic per-provider is independent.
func (p googleProvider) lookupEventIDByUID(calendarID, uid string) (string, error) {
	var id string
	err := p.store.DB().QueryRow(
		`SELECT id FROM events WHERE calendar_id = ? AND uid = ?`,
		calendarID, uid,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// fillDenormalizedFieldsFromICS parses the just-built ICS blob to populate
// dtstart_unix / dtend_unix / is_all_day / tz_name AND attendees /
// organizer on the row. The provider JSON has the canonical values; we
// re-parse through the ICS path so the same code that powers CalDAV+local
// also sets these fields.
//
// Attendees + organizer matter for the JSON columns + side index that
// EventDetail / EventComposerDialog read from — without copying them
// here, every Google/Microsoft sync overwrites the local row with empty
// attendees, even though the parsed ICS has them (since buildEvent calls
// parseAttendeesFromVEVENT). Same shape bug as the create-time / update
// paths in event_crud.go, just on the sync side.
func fillDenormalizedFieldsFromICS(ev *Event, blob string) {
	parsed, err := ParseCalendarObject(blob)
	if err != nil {
		return
	}
	ev.DTStartUnix = parsed.Master.DTStartUnix
	ev.DTEndUnix = parsed.Master.DTEndUnix
	ev.IsAllDay = parsed.Master.IsAllDay
	ev.TZName = parsed.Master.TZName
	ev.Attendees = parsed.Master.Attendees
	ev.Organizer = parsed.Master.Organizer
}

// --- Write (PUT) -----------------------------------------------------------

// PushEvent POSTs a new event or PATCHes an existing one. ev.ProviderEventID
// empty ⇒ create; non-empty ⇒ update with If-Match: ev.ETag.
func (p googleProvider) PushEvent(ctx context.Context, src Source, cal Calendar, ev Event) (PushResult, error) {
	client, err := p.httpClient(src)
	if err != nil {
		return PushResult{}, err
	}

	body, err := translateICSToGoogleJSON(ev.ICSBlob)
	if err != nil {
		return PushResult{}, fmt.Errorf("translate ICS to google JSON: %w", err)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return PushResult{}, fmt.Errorf("marshal google event: %w", err)
	}

	method := http.MethodPost
	endpoint := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) + "/events"
	if ev.ProviderEventID != "" {
		method = http.MethodPatch
		endpoint += "/" + url.PathEscape(ev.ProviderEventID)
	}
	// Phase E: per-event sendUpdates honors the user's choice. Empty (or
	// unset) falls through to Google's default — no `sendUpdates` query
	// param. Validated values: "all" | "externalOnly" | "none".
	if su := ev.SendUpdates; su == "all" || su == "externalOnly" || su == "none" {
		endpoint += "?sendUpdates=" + url.QueryEscape(su)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return PushResult{}, fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if ev.ProviderEventID != "" && ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := client.Do(req)
	if err != nil {
		return PushResult{}, fmt.Errorf("google %s event: %w: %v", strings.ToLower(method), ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out googleEvent
		if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
			return PushResult{}, fmt.Errorf("decode google response: %w", derr)
		}
		// Extract authoritative attendees/organizer from the response so
		// updateAllAndPush can persist the server's view (e.g., responseStatus
		// resets to needsAction on significant changes like time moves).
		// We reuse the sync-time ICS round-trip path so the same parser
		// produces both flows' Attendees / Organizer.
		atts, org := googleEventToAttendees(out)
		return PushResult{
			ETag:            out.ETag,
			ProviderEventID: out.ID,
			Attendees:       atts,
			Organizer:       org,
		}, nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		return PushResult{}, ErrConflict
	}
	body2, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return PushResult{}, fmt.Errorf("google %s event %d %s: %s",
		strings.ToLower(method), resp.StatusCode, resp.Status, strings.TrimSpace(string(body2)))
}

// googleEventToAttendees converts the Google response's attendees +
// organizer fields into Aerion's shape. Reuses the existing translation
// by going through the ICS round-trip — same path sync uses — so the
// PartStat / Role / CUType normalization stays in one place.
//
// Returns nil/nil when the source has no attendees AND no organizer (the
// common local-event shape).
func googleEventToAttendees(out googleEvent) ([]Attendee, *Organizer) {
	if len(out.Attendees) == 0 && out.Organizer == nil {
		return nil, nil
	}
	blob, err := translateGoogleEventToICS(out)
	if err != nil {
		return nil, nil
	}
	parsed, perr := ParseCalendarObject(blob)
	if perr != nil {
		return nil, nil
	}
	return parsed.Master.Attendees, parsed.Master.Organizer
}

// --- Delete ---------------------------------------------------------------

// DeleteRemote deletes the event from Google. Honors If-Match for
// optimistic concurrency. 404 is idempotent (event already gone server-side).
func (p googleProvider) DeleteRemote(ctx context.Context, src Source, cal Calendar, ev Event) error {
	if ev.ProviderEventID == "" {
		// Event was never on the server (or sync hadn't run). Local delete
		// still proceeds; nothing to do here.
		return nil
	}
	client, err := p.httpClient(src)
	if err != nil {
		return err
	}

	endpoint := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) +
		"/events/" + url.PathEscape(ev.ProviderEventID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build DELETE request: %w", err)
	}
	if ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("google delete event: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
		return nil
	case http.StatusPreconditionFailed:
		return ErrConflict
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("google delete event %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// --- Calendar list (for the add-calendar picker) ---------------------------

// ListGoogleCalendars returns the user's Google calendars suitable for the
// "Add Google Calendar" picker dialog. NOT part of the Provider interface
// — called only from the bridge's AddGoogleSource flow.
type googleCalendarListResponse struct {
	Items         []googleCalendarListEntry `json:"items"`
	NextPageToken string                    `json:"nextPageToken,omitempty"`
}

// PushInstance for Google — looks up the target instance via the
// events.instances endpoint, then PATCHes (update) or DELETEs (delete)
// that single instance. For scope=this-and-future + update: clamps the
// master's RRULE via PATCH on the master, then POSTs the new series as
// a separate event.
func (p googleProvider) PushInstance(ctx context.Context, src Source, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	if payload.Master.ProviderEventID == "" {
		return PushInstanceResult{}, fmt.Errorf("google PushInstance: master has no ProviderEventID")
	}
	client, err := p.httpClient(src)
	if err != nil {
		return PushInstanceResult{}, err
	}

	switch payload.Op {
	case EditScopeThis:
		return p.pushThis(ctx, client, cal, payload)
	case EditScopeThisAndFuture:
		return p.pushThisAndFuture(ctx, client, cal, payload)
	}
	return PushInstanceResult{}, fmt.Errorf("google PushInstance: unsupported scope %q", payload.Op)
}

// pushThis modifies or deletes one instance.
func (p googleProvider) pushThis(ctx context.Context, client *http.Client, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	instanceID, err := p.findInstanceID(ctx, client, cal, payload.Master.ProviderEventID, payload.InstanceTimeUnix)
	if err != nil {
		return PushInstanceResult{}, err
	}

	instanceURL := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) +
		"/events/" + url.PathEscape(instanceID)

	if payload.Kind == InstanceOpDelete {
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, instanceURL, nil)
		resp, derr := client.Do(req)
		if derr != nil {
			return PushInstanceResult{}, fmt.Errorf("google delete instance: %w: %v", ErrTransport, derr)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
			return PushInstanceResult{OverrideProviderEventID: instanceID}, nil
		case http.StatusPreconditionFailed:
			return PushInstanceResult{}, ErrConflict
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("google delete instance %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	// Update: build PATCH payload from the override blob the API layer
	// already produced in payload.In (single VEVENT, same shape as
	// create), and PATCH the specific instance.
	overrideICS, oerr := serializeVEVENT(payload.Master.UID, payload.In)
	if oerr != nil {
		return PushInstanceResult{}, fmt.Errorf("serialize override: %w", oerr)
	}
	body, terr := translateICSToGoogleJSON(overrideICS)
	if terr != nil {
		return PushInstanceResult{}, fmt.Errorf("translate override: %w", terr)
	}
	payloadJSON, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, instanceURL, bytes.NewReader(payloadJSON))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, perr := client.Do(req)
	if perr != nil {
		return PushInstanceResult{}, fmt.Errorf("google patch instance: %w: %v", ErrTransport, perr)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out googleEvent
		if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
			return PushInstanceResult{}, fmt.Errorf("decode google response: %w", derr)
		}
		return PushInstanceResult{
			OverrideProviderEventID: out.ID,
			OverrideETag:            out.ETag,
		}, nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		return PushInstanceResult{}, ErrConflict
	}
	bodyRaw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return PushInstanceResult{}, fmt.Errorf("google patch instance %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(bodyRaw)))
}

// pushThisAndFuture clamps the master's RRULE and (for update) POSTs a
// new event for the future series.
func (p googleProvider) pushThisAndFuture(ctx context.Context, client *http.Client, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	// PATCH master with clamped RRULE.
	clampedRRULE := clampRRuleUntil(payload.Master.RRuleText, payload.InstanceTimeUnix-1)
	masterPatch := googleEvent{
		Recurrence: []string{"RRULE:" + clampedRRULE},
	}
	masterURL := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) +
		"/events/" + url.PathEscape(payload.Master.ProviderEventID)
	masterPayload, _ := json.Marshal(masterPatch)
	mreq, _ := http.NewRequestWithContext(ctx, http.MethodPatch, masterURL, bytes.NewReader(masterPayload))
	mreq.Header.Set("Content-Type", "application/json; charset=utf-8")
	if payload.Master.ETag != "" {
		mreq.Header.Set("If-Match", payload.Master.ETag)
	}
	mresp, merr := client.Do(mreq)
	if merr != nil {
		return PushInstanceResult{}, fmt.Errorf("google patch master: %w: %v", ErrTransport, merr)
	}
	defer mresp.Body.Close()
	switch mresp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusPreconditionFailed:
		return PushInstanceResult{}, ErrConflict
	default:
		body, _ := io.ReadAll(io.LimitReader(mresp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("google patch master %d %s: %s",
			mresp.StatusCode, mresp.Status, strings.TrimSpace(string(body)))
	}
	var mout googleEvent
	if derr := json.NewDecoder(mresp.Body).Decode(&mout); derr != nil {
		return PushInstanceResult{}, fmt.Errorf("decode master patch response: %w", derr)
	}
	result := PushInstanceResult{MasterNewETag: mout.ETag}

	if payload.Kind == InstanceOpDelete {
		return result, nil
	}

	// POST new series.
	newUID := uuid.NewString() + "@aerion-google"
	newICS, serr := serializeVEVENT(newUID, payload.In)
	if serr != nil {
		return PushInstanceResult{}, fmt.Errorf("serialize new series: %w", serr)
	}
	newJSON, terr := translateICSToGoogleJSON(newICS)
	if terr != nil {
		return PushInstanceResult{}, fmt.Errorf("translate new series: %w", terr)
	}
	newPayload, _ := json.Marshal(newJSON)

	newURL := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) + "/events"
	nreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, newURL, bytes.NewReader(newPayload))
	nreq.Header.Set("Content-Type", "application/json; charset=utf-8")
	nresp, nerr := client.Do(nreq)
	if nerr != nil {
		return PushInstanceResult{}, fmt.Errorf("google post new series: %w: %v", ErrTransport, nerr)
	}
	defer nresp.Body.Close()
	if nresp.StatusCode != http.StatusOK && nresp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(nresp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("google post new series %d %s: %s",
			nresp.StatusCode, nresp.Status, strings.TrimSpace(string(body)))
	}
	var nout googleEvent
	if derr := json.NewDecoder(nresp.Body).Decode(&nout); derr != nil {
		return PushInstanceResult{}, fmt.Errorf("decode new series response: %w", derr)
	}
	result.NewSeries = &NewSeriesIdentifiers{
		UID:             nout.ICalUID,
		ETag:            nout.ETag,
		ProviderEventID: nout.ID,
	}
	return result, nil
}

// findInstanceID resolves the instanceEventID for a given originalStartTime.
func (p googleProvider) findInstanceID(ctx context.Context, client *http.Client, cal Calendar, masterEventID string, instanceTimeUnix int64) (string, error) {
	instanceTime := time.Unix(instanceTimeUnix, 0).UTC()
	timeMin := instanceTime.Add(-25 * time.Hour).Format(time.RFC3339)
	timeMax := instanceTime.Add(25 * time.Hour).Format(time.RFC3339)

	q := url.Values{}
	q.Set("timeMin", timeMin)
	q.Set("timeMax", timeMax)
	q.Set("showDeleted", "true")
	q.Set("maxResults", "50")
	u := googleAPIBase + "/calendars/" + url.PathEscape(cal.URL) +
		"/events/" + url.PathEscape(masterEventID) + "/instances?" + q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("google list instances: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("google list instances %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	var page googleEventsListResponse
	if derr := json.NewDecoder(resp.Body).Decode(&page); derr != nil {
		return "", fmt.Errorf("decode instances: %w", derr)
	}
	for _, ev := range page.Items {
		if ev.OriginalStartTime == nil {
			continue
		}
		t, perr := parseGoogleTime(ev.OriginalStartTime)
		if perr != nil {
			continue
		}
		if t.Unix() == instanceTimeUnix {
			return ev.ID, nil
		}
	}
	return "", fmt.Errorf("google: no instance found at unix %d", instanceTimeUnix)
}

// parseGoogleTime is a small helper that handles both Date and DateTime
// forms of googleTimePoint.
func parseGoogleTime(tp *googleTimePoint) (time.Time, error) {
	if tp.Date != "" {
		return time.Parse("2006-01-02", tp.Date)
	}
	return time.Parse(time.RFC3339, tp.DateTime)
}

func (p googleProvider) ListGoogleCalendars(ctx context.Context, src Source) ([]googleCalendarListEntry, error) {
	client, err := p.httpClient(src)
	if err != nil {
		return nil, err
	}
	var out []googleCalendarListEntry
	pageToken := ""
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		u := googleAPIBase + "/users/me/calendarList?maxResults=250"
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, derr := client.Do(req)
		if derr != nil {
			return nil, fmt.Errorf("google calendarList: %w", derr)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("google calendarList %d %s: %s",
				resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
		}

		var page googleCalendarListResponse
		decErr := json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode calendarList: %w", decErr)
		}
		out = append(out, page.Items...)
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}
