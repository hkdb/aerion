package backend

// Microsoft Graph Calendar provider — Phase 2 Chunk 4.
//
// Implements the Provider interface for Microsoft Graph Calendar
// (Outlook.com + Microsoft 365) using coreapi.Auth's OAuth-vended
// *http.Client. Translation between Graph's event JSON and Aerion's
// ICS blob lives in provider_microsoft_translate.go.
//
// Storage model unchanged from Google: events.ics_blob holds a
// single-VEVENT VCALENDAR per row, event_recurrence_overrides holds
// per-instance overrides. `calendars.url` stores Graph's calendar id;
// `calendars.ctag` stores the incremental @odata.deltaLink.
//
// Chunk 4 scope:
//   - SyncCalendar: delta-based incremental via @odata.deltaLink;
//     paginated via @odata.nextLink. Master events fully supported;
//     events with seriesMasterId (exceptions/occurrences) are skipped
//     with a log line — per-instance override sync is a follow-up.
//   - PushEvent: POST for create, PATCH for update with If-Match.
//   - DeleteRemote: DELETE with If-Match; 404 idempotent; 412 → ErrConflict.
//   - scope=this / scope=this-and-future deferred (parity with CalDAV
//     Chunk 2 + Google Chunk 3).
//   - Single-reminder caveat: Graph supports one
//     reminderMinutesBeforeStart; multiple VALARMs send the first only.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

const (
	microsoftGraphBase = "https://graph.microsoft.com/v1.0"
	microsoftRWScope   = "https://graph.microsoft.com/Calendars.ReadWrite"
	microsoftRWReason  = "Sync and edit your Outlook Calendar events"
	microsoftPreferTZ  = `outlook.timezone="UTC"`
	microsoftSyncLimit = 250
)

type microsoftProvider struct {
	store *Store
	auth  coreapi.Auth
	log   coreapi.Logger // optional; nil = silent
}

// debugf emits a debug line via the host logger when one is wired.
func (p microsoftProvider) debugf(format string, args ...any) {
	if p.log != nil {
		p.log.Debug(fmt.Sprintf(format, args...))
	}
}

func (microsoftProvider) Capabilities() Capabilities {
	return Capabilities{
		CanWrite:        true,
		CanDeleteSeries: true,
		CanSetReminders: true,
	}
}

// --- HTTP client + helpers -------------------------------------------------

func (p microsoftProvider) httpClient(src Source) (*http.Client, error) {
	if p.auth == nil {
		return nil, fmt.Errorf("microsoftProvider: no Auth handle (extension built without coreapi.Core)")
	}
	if src.AccountID == "" {
		return nil, fmt.Errorf("microsoftProvider: source %q has no account ID", src.ID)
	}
	return p.auth.HTTPClient(src.AccountID, []coreapi.AuthScope{
		{Resource: microsoftRWScope, Reason: microsoftRWReason},
	})
}

// doGraphRequest executes req with the Microsoft-required Prefer header and
// JSON Accept header, with a single retry on 429 / 503 (honoring
// Retry-After). Mirrors the contacts extension's retry shape
// (extensions/contacts/backend/microsoft_write.go).
func doGraphRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	req.Header.Set("Accept", "application/json")
	// Combine the timezone preference with any Prefer the caller already set
	// (e.g. odata.maxpagesize on the events delta). RFC 7240 allows multiple
	// comma-separated preference tokens; a bare Set would clobber the caller's.
	prefer := microsoftPreferTZ
	if existing := req.Header.Get("Prefer"); existing != "" {
		prefer = existing + ", " + microsoftPreferTZ
	}
	req.Header.Set("Prefer", prefer)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
		return resp, nil
	}

	retryAfter := parseGraphRetryAfter(resp.Header.Get("Retry-After"))
	_ = resp.Body.Close()

	// One retry, with context cancellation honored.
	timer := time.NewTimer(retryAfter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}

	// Body has been consumed; rebuild request from saved body if needed.
	// For the methods Chunk 4 ships, requests are small (DELETE has no
	// body; PUT/POST/PATCH bodies live in bytes.Reader/strings.Reader and
	// won't seek; the caller in doJSONRequest re-creates the request body
	// before retrying via the wrapper that captures the payload).
	retryReq := req.Clone(ctx)
	if req.Body != nil {
		// Caller should pre-buffer the body so retry is safe; in practice
		// our PushEvent uses bytes.NewReader which auto-seeks.
		if seeker, ok := req.Body.(interface {
			Seek(int64, int) (int64, error)
		}); ok {
			_, _ = seeker.Seek(0, 0)
		}
		retryReq.Body = req.Body
	}
	retryResp, retryErr := client.Do(retryReq)
	if retryErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, retryErr)
	}
	return retryResp, nil
}

// parseGraphRetryAfter parses Retry-After: either integer seconds or an
// HTTP-date. Defaults to 2 seconds on unparseable values.
func parseGraphRetryAfter(v string) time.Duration {
	if v == "" {
		return 2 * time.Second
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n > 60 {
			n = 60
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 2 * time.Second
		}
		if d > 60*time.Second {
			return 60 * time.Second
		}
		return d
	}
	return 2 * time.Second
}

// --- Sync ------------------------------------------------------------------

// SyncCalendar syncs a Microsoft 365 calendar using the events collection +
// client-side recurrence expansion — the model mature Exchange clients use.
//
// Microsoft Graph v1.0 has no un-windowed event delta: calendarView/delta is
// date-bounded (and returns server-expanded occurrences), and the unbounded
// /me/events/delta is beta-only (unsupported in production). So we enumerate the
// events collection, which returns "single instance meetings and series masters"
// (not expanded occurrences, not date-windowed); each master carries its
// recurrence rule, which we convert to RRULE and expand client-side.
//
// One paginated call returns full event objects — no per-event detail fetch (an
// N+1 that crawled on large read-only calendars). We re-convert every event each
// pass (no etag short-circuit) so a fix to the recurrence converter self-heals
// previously-mis-stored rows on the next sync. The reconcile is non-destructive:
// it keys on the stable master/single iCalUId and never deletes against an empty
// list. The ctag column is unused (NULL), as for CalDAV. (Modified/cancelled
// occurrences are layered on in a follow-up.)
func (p microsoftProvider) SyncCalendar(ctx context.Context, src Source, cal Calendar) error {
	client, err := p.httpClient(src)
	if err != nil {
		return err
	}

	// $top forces a large page size: Prefer:odata.maxpagesize isn't reliably
	// honored on the events collection (it paged at ~10), which made large
	// read-only calendars crawl. nextLink carries $top through the pages.
	pageURL := microsoftGraphBase + "/me/calendars/" + url.PathEscape(cal.URL) +
		fmt.Sprintf("/events?$top=%d", microsoftSyncLimit)
	var rows []graphEvent
	for pageURL != "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		page, err := p.fetchEventsPage(ctx, client, pageURL)
		if err != nil {
			return err
		}
		rows = append(rows, page.Value...)
		pageURL = page.NextLink
	}

	localETags, err := p.store.ListEventETags(cal.ID)
	if err != nil {
		return fmt.Errorf("list local etags: %w", err)
	}
	plan := planEventSync(rows, localETags)

	// Convert masters + singles. Series masters additionally get their modified
	// instances (RECURRENCE-ID overrides) and cancellations (EXDATE) from a
	// per-master fetch. Unparseable events are skipped (not fatal) and stay
	// "seen", so the reconcile won't delete them.
	type processedEvent struct {
		ev        Event
		overrides []EventOverride
		isMaster  bool
	}
	out := make([]processedEvent, 0, len(plan.process))
	translateFail := 0
	for _, r := range plan.process {
		ev, terr := p.graphEventToStored(cal, r)
		if terr != nil {
			translateFail++
			continue
		}
		pe := processedEvent{ev: ev, isMaster: strings.EqualFold(r.Type, "seriesMaster")}
		if pe.isMaster {
			pe.ev, pe.overrides = p.applyMasterExceptions(ctx, client, cal, r.ID, pe.ev)
		}
		out = append(out, pe)
	}
	p.debugf("ms-sync calendar=%q fetched=%d changed=%d upserts=%d deletes=%d translateFail=%d",
		cal.DisplayName, len(rows), len(plan.process), len(out), len(plan.deletes), translateFail)

	return p.store.WithTx(func(tx *sql.Tx) error {
		for _, pe := range out {
			if err := p.store.UpsertEventTx(tx, pe.ev); err != nil {
				return err
			}
			if !pe.isMaster {
				continue
			}
			// Clear + rewrite this master's overrides (mirrors CalDAV) so removed
			// exceptions don't linger.
			if _, err := tx.Exec(`DELETE FROM event_recurrence_overrides WHERE event_id = ?`, pe.ev.ID); err != nil {
				return fmt.Errorf("clear overrides: %w", err)
			}
			for _, ov := range pe.overrides {
				if err := p.store.UpsertOverrideTx(tx, pe.ev.ID, ov.RecurrenceIDUnix, ov.ICSBlob); err != nil {
					return err
				}
			}
		}
		// Non-destructive reconcile: delete only local events absent from the
		// full server list, and never when the list came back empty (treat a
		// zero-row pull as suspect rather than wiping the calendar).
		if plan.seenAny {
			for _, uid := range plan.deletes {
				if err := p.store.DeleteEventByUIDTx(tx, cal.ID, uid); err != nil {
					return err
				}
			}
		}
		return p.store.UpdateCalendarCtagTx(tx, cal.ID, "", time.Now().Unix())
	})
}

// applyMasterExceptions folds a series master's modified + cancelled instances
// into the stored master: cancellations become EXDATE on the blob, modified
// instances become RECURRENCE-ID overrides. Best-effort — on any fetch/parse
// error the master syncs unchanged (occurrences at their default times).
func (p microsoftProvider) applyMasterExceptions(ctx context.Context, client *http.Client, cal Calendar, eventID string, ev Event) (Event, []EventOverride) {
	md, err := p.fetchMasterDetail(ctx, client, eventID)
	if err != nil || md == nil {
		p.debugf("ms-sync master detail failed: calendar=%q: %v", cal.DisplayName, err)
		return ev, nil
	}

	// Cancellations → EXDATE on the master blob.
	for _, oid := range md.CancelledOccurrences {
		occUnix, ok := cancelledOccurrenceUnix(oid, ev.DTStartUnix)
		if !ok {
			continue
		}
		if nb, e := addEXDATE(ev.ICSBlob, occUnix); e == nil {
			ev.ICSBlob = nb
		}
	}

	// Modified instances → RECURRENCE-ID overrides keyed by the original start.
	var overrides []EventOverride
	for _, ex := range md.ExceptionOccurrences {
		recID, perr := parseGraphDateTime(ex.OriginalStart)
		if perr != nil || ex.Start == nil || ex.End == nil {
			continue
		}
		if ex.ICalUID == "" {
			ex.ICalUID = ev.UID // override-blob UID is irrelevant to applyOverride
		}
		blob, terr := translateGraphEventToICS(ex)
		if terr != nil {
			continue
		}
		overrides = append(overrides, EventOverride{
			RecurrenceIDUnix: recID.UTC().Unix(),
			ICSBlob:          blob,
		})
	}
	return ev, overrides
}

// fetchMasterDetail GETs a series master with its modified instances expanded and
// its cancelled-instance ids selected.
func (p microsoftProvider) fetchMasterDetail(ctx context.Context, client *http.Client, eventID string) (*graphEvent, error) {
	u := microsoftGraphBase + "/me/events/" + url.PathEscape(eventID) +
		"?$select=id,cancelledOccurrences" +
		"&$expand=exceptionOccurrences($select=originalStart,start,end,subject,body,location,isAllDay)"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build master detail request: %w", err)
	}
	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("graph master detail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("graph master detail %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	var md graphEvent
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return nil, fmt.Errorf("decode master detail: %w", err)
	}
	return &md, nil
}

// cancelledOccurrenceUnix derives a cancelled occurrence's instant from its
// occurrenceId ("OID.{masterId}.{yyyy-MM-dd}") combined with the master's start
// time-of-day (UTC) — matching how the RRULE expands occurrences. Returns false
// if the trailing date can't be parsed.
func cancelledOccurrenceUnix(occurrenceID string, masterStartUnix int64) (int64, bool) {
	i := strings.LastIndex(occurrenceID, ".")
	if i < 0 || i+1 >= len(occurrenceID) {
		return 0, false
	}
	d, err := time.Parse("2006-01-02", occurrenceID[i+1:])
	if err != nil {
		return 0, false
	}
	ms := time.Unix(masterStartUnix, 0).UTC()
	occ := time.Date(d.Year(), d.Month(), d.Day(), ms.Hour(), ms.Minute(), ms.Second(), 0, time.UTC)
	return occ.Unix(), true
}

type graphEventsResponse struct {
	Value    []graphEvent `json:"value"`
	NextLink string       `json:"@odata.nextLink,omitempty"`
}

// fetchEventsPage GETs one page of a Microsoft Graph events collection.
func (p microsoftProvider) fetchEventsPage(ctx context.Context, client *http.Client, pageURL string) (*graphEventsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build events request: %w", err)
	}
	// Request a large page size via Prefer. @odata.nextLink follow-ups already
	// carry their own page state.
	req.Header.Set("Prefer", "odata.maxpagesize="+fmt.Sprintf("%d", microsoftSyncLimit))
	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("graph events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("graph events %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	var out graphEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return &out, nil
}

// eventSyncPlan is the pure decision of an events-collection sync pass: which
// new/changed (master/single) rows to convert + upsert, and which local UIDs to
// delete.
type eventSyncPlan struct {
	process []graphEvent // new/changed masters + single instances (etag differs)
	deletes []string     // local UIDs no longer present server-side
	seenAny bool         // false when the server list was empty → suppress deletes
}

// planEventSync partitions a full events-collection listing against local state.
// Exceptions (seriesMasterId set) are ignored — they're synced via their master,
// not as standalone rows. A row is queued for processing when it's new/etag-
// changed OR a series master: masters always re-process because their modified/
// cancelled instances can change without bumping the master's own etag, while
// single instances (the bulk, e.g. read-only holidays) keep the etag skip and
// stay fast. EVERY master/single present is recorded as "seen" so a present-but-
// unchanged event is never deleted. seenAny gates the delete pass so an
// empty/failed listing can't wipe the calendar.
func planEventSync(rows []graphEvent, localETags map[string]string) eventSyncPlan {
	seen := make(map[string]struct{}, len(rows))
	var plan eventSyncPlan
	for _, r := range rows {
		if r.SeriesMasterID != "" || r.ICalUID == "" {
			continue
		}
		if _, dup := seen[r.ICalUID]; dup {
			continue
		}
		seen[r.ICalUID] = struct{}{}
		et, ok := localETags[r.ICalUID]
		changed := !ok || et == "" || et != r.ETag
		if strings.EqualFold(r.Type, "seriesMaster") || changed {
			plan.process = append(plan.process, r)
		}
	}
	plan.seenAny = len(seen) > 0
	for uid := range localETags {
		if _, ok := seen[uid]; !ok {
			plan.deletes = append(plan.deletes, uid)
		}
	}
	return plan
}

// graphEventToStored translates a Graph event into a stored Event, preserving
// the existing row's ID when the UID is already known.
func (p microsoftProvider) graphEventToStored(cal Calendar, item graphEvent) (Event, error) {
	blob, err := translateGraphEventToICS(item)
	if err != nil {
		return Event{}, err
	}

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
		Summary:         item.Subject,
		Description:     bodyContent(item.Body),
		Location:        locationDisplayName(item.Location),
		ICSBlob:         blob,
	}
	fillDenormalizedFieldsFromICS(&ev, blob)
	if item.Recurrence != nil {
		if rrule := graphRecurrenceToRRule(item.Recurrence); rrule != "" {
			ev.RRuleText = rrule
		}
	}
	return ev, nil
}

func (p microsoftProvider) lookupEventIDByUID(calendarID, uid string) (string, error) {
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

func bodyContent(b *graphBody) string {
	if b == nil {
		return ""
	}
	return b.Content
}

func locationDisplayName(l *graphLocation) string {
	if l == nil {
		return ""
	}
	return l.DisplayName
}

// --- Write (POST + PATCH) --------------------------------------------------

// PushEvent POSTs a new event or PATCHes an existing one. PATCH operates
// on /me/events/{id} (NOT nested under the calendar — Graph's model
// differs from Google's).
// fetchMicrosoftEventETag does a minimal `$select=id` GET on a Graph event
// and returns its current ETag, or "" on any error / non-200 response.
// Used right before a PATCH so the If-Match header reflects the server's
// current etag (Graph mutates event etags out-of-band; the locally-cached
// etag from a prior sync becomes stale and poisons subsequent PATCHes
// with FAILED_PRECONDITION 412s).
func fetchMicrosoftEventETag(ctx context.Context, client *http.Client, providerEventID string) string {
	if providerEventID == "" {
		return ""
	}
	u := microsoftGraphBase + "/me/events/" + url.PathEscape(providerEventID) + "?$select=id"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var probe graphEvent
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
		return ""
	}
	return probe.ETag
}

func (p microsoftProvider) PushEvent(ctx context.Context, src Source, cal Calendar, ev Event) (PushResult, error) {
	client, err := p.httpClient(src)
	if err != nil {
		return PushResult{}, err
	}

	body, err := translateICSToGraphEvent(ev.ICSBlob)
	if err != nil {
		return PushResult{}, fmt.Errorf("translate ICS to graph event: %w", err)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return PushResult{}, fmt.Errorf("marshal graph event: %w", err)
	}

	method := http.MethodPost
	endpoint := microsoftGraphBase + "/me/calendars/" + url.PathEscape(cal.URL) + "/events"
	if ev.ProviderEventID != "" {
		method = http.MethodPatch
		endpoint = microsoftGraphBase + "/me/events/" + url.PathEscape(ev.ProviderEventID)
		// Refresh the etag from a single-event GET right before PATCH.
		// Graph mutates event etags out-of-band (background indexing,
		// category propagation, etc.) — the locally-cached etag from a
		// prior sync becomes stale and the next PATCH gets 412
		// FAILED_PRECONDITION. Using the freshly-fetched etag eliminates
		// that class of conflict-on-first-edit failures.
		//
		// Trade-off: optimistic locking is now scoped to the GET→PATCH
		// roundtrip (~ms), not the user's Edit-dialog-open→Save window.
		// Matches the behavior of Outlook's own client.
		if freshETag := fetchMicrosoftEventETag(ctx, client, ev.ProviderEventID); freshETag != "" {
			ev.ETag = freshETag
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return PushResult{}, fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ev.ProviderEventID != "" && ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return PushResult{}, fmt.Errorf("graph %s event: %w", strings.ToLower(method), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out graphEvent
		if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
			return PushResult{}, fmt.Errorf("decode graph response: %w", derr)
		}
		// Extract authoritative attendees/organizer from the response so
		// updateAllAndPush persists Graph's view (e.g., attendee.status
		// reset to notResponded after a time change). Reuses the
		// sync-time ICS round-trip so the parser stays in one place.
		atts, org := graphEventToAttendees(out)
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
	return PushResult{}, fmt.Errorf("graph %s event %d %s: %s",
		strings.ToLower(method), resp.StatusCode, resp.Status, strings.TrimSpace(string(body2)))
}

// graphEventToAttendees converts the Graph response's attendees +
// organizer fields into Aerion's shape. Mirrors googleEventToAttendees
// in provider_google.go — round-trips through ICS so the parser is the
// single source of truth.
func graphEventToAttendees(out graphEvent) ([]Attendee, *Organizer) {
	if len(out.Attendees) == 0 && out.Organizer == nil {
		return nil, nil
	}
	blob, err := translateGraphEventToICS(out)
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

func (p microsoftProvider) DeleteRemote(ctx context.Context, src Source, cal Calendar, ev Event) error {
	if ev.ProviderEventID == "" {
		// Event was never on the server (or sync hadn't run). Local
		// delete still proceeds; nothing to do here.
		return nil
	}
	client, err := p.httpClient(src)
	if err != nil {
		return err
	}

	endpoint := microsoftGraphBase + "/me/events/" + url.PathEscape(ev.ProviderEventID)

	// Refresh etag before DELETE to avoid stale-cache 412 (see
	// fetchMicrosoftEventETag comment in PushEvent).
	if freshETag := fetchMicrosoftEventETag(ctx, client, ev.ProviderEventID); freshETag != "" {
		ev.ETag = freshETag
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build DELETE request: %w", err)
	}
	if ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return fmt.Errorf("graph delete event: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
		return nil
	case http.StatusPreconditionFailed:
		return ErrConflict
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("graph delete event %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// --- Calendar list (for the add-calendar picker) ---------------------------

type microsoftCalendarListResponse struct {
	Value    []microsoftCalendarListEntry `json:"value"`
	NextLink string                       `json:"@odata.nextLink,omitempty"`
}

// PushInstance for Microsoft — Graph's instances endpoint to find the
// target instance + PATCH/DELETE on the instance event id.
func (p microsoftProvider) PushInstance(ctx context.Context, src Source, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	if payload.Master.ProviderEventID == "" {
		return PushInstanceResult{}, fmt.Errorf("microsoft PushInstance: master has no ProviderEventID")
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
	return PushInstanceResult{}, fmt.Errorf("microsoft PushInstance: unsupported scope %q", payload.Op)
}

func (p microsoftProvider) pushThis(ctx context.Context, client *http.Client, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	instanceID, err := p.findInstanceID(ctx, client, payload.Master.ProviderEventID, payload.InstanceTimeUnix)
	if err != nil {
		return PushInstanceResult{}, err
	}

	instanceURL := microsoftGraphBase + "/me/events/" + url.PathEscape(instanceID)

	if payload.Kind == InstanceOpDelete {
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, instanceURL, nil)
		resp, derr := doGraphRequest(ctx, client, req)
		if derr != nil {
			return PushInstanceResult{}, fmt.Errorf("graph delete instance: %w", derr)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
			return PushInstanceResult{OverrideProviderEventID: instanceID}, nil
		case http.StatusPreconditionFailed:
			return PushInstanceResult{}, ErrConflict
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("graph delete instance %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	// Build the PATCH body from a serialized VEVENT.
	overrideICS, oerr := serializeVEVENT(payload.Master.UID, payload.In)
	if oerr != nil {
		return PushInstanceResult{}, fmt.Errorf("serialize override: %w", oerr)
	}
	body, terr := translateICSToGraphEvent(overrideICS)
	if terr != nil {
		return PushInstanceResult{}, fmt.Errorf("translate override: %w", terr)
	}
	payloadJSON, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, instanceURL, bytes.NewReader(payloadJSON))
	req.Header.Set("Content-Type", "application/json")
	resp, perr := doGraphRequest(ctx, client, req)
	if perr != nil {
		return PushInstanceResult{}, fmt.Errorf("graph patch instance: %w", perr)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out graphEvent
		if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
			return PushInstanceResult{}, fmt.Errorf("decode graph response: %w", derr)
		}
		return PushInstanceResult{
			OverrideProviderEventID: out.ID,
			OverrideETag:            out.ETag,
		}, nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		return PushInstanceResult{}, ErrConflict
	}
	bodyRaw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return PushInstanceResult{}, fmt.Errorf("graph patch instance %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(bodyRaw)))
}

func (p microsoftProvider) pushThisAndFuture(ctx context.Context, client *http.Client, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	// PATCH master with recurrence.range.endDate clamped.
	endDate := time.Unix(payload.InstanceTimeUnix-86400, 0).UTC().Format("2006-01-02")
	masterPatch := graphEvent{
		Recurrence: &graphRecurrence{
			Range: graphRange{
				Type:    "endDate",
				EndDate: endDate,
			},
		},
	}
	masterURL := microsoftGraphBase + "/me/events/" + url.PathEscape(payload.Master.ProviderEventID)
	// Refresh master's etag before PATCH to avoid stale-cache 412 (see
	// fetchMicrosoftEventETag comment in PushEvent).
	if freshETag := fetchMicrosoftEventETag(ctx, client, payload.Master.ProviderEventID); freshETag != "" {
		payload.Master.ETag = freshETag
	}
	masterPayload, _ := json.Marshal(masterPatch)
	mreq, _ := http.NewRequestWithContext(ctx, http.MethodPatch, masterURL, bytes.NewReader(masterPayload))
	mreq.Header.Set("Content-Type", "application/json")
	if payload.Master.ETag != "" {
		mreq.Header.Set("If-Match", payload.Master.ETag)
	}
	mresp, merr := doGraphRequest(ctx, client, mreq)
	if merr != nil {
		return PushInstanceResult{}, fmt.Errorf("graph patch master: %w", merr)
	}
	defer mresp.Body.Close()
	switch mresp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusPreconditionFailed:
		return PushInstanceResult{}, ErrConflict
	default:
		body, _ := io.ReadAll(io.LimitReader(mresp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("graph patch master %d %s: %s",
			mresp.StatusCode, mresp.Status, strings.TrimSpace(string(body)))
	}
	var mout graphEvent
	if derr := json.NewDecoder(mresp.Body).Decode(&mout); derr != nil {
		return PushInstanceResult{}, fmt.Errorf("decode master patch response: %w", derr)
	}
	result := PushInstanceResult{MasterNewETag: mout.ETag}

	if payload.Kind == InstanceOpDelete {
		return result, nil
	}

	// POST new event with the new series.
	newUID := uuid.NewString() + "@aerion-microsoft"
	newICS, serr := serializeVEVENT(newUID, payload.In)
	if serr != nil {
		return PushInstanceResult{}, fmt.Errorf("serialize new series: %w", serr)
	}
	newBody, terr := translateICSToGraphEvent(newICS)
	if terr != nil {
		return PushInstanceResult{}, fmt.Errorf("translate new series: %w", terr)
	}
	newPayload, _ := json.Marshal(newBody)

	newURL := microsoftGraphBase + "/me/calendars/" + url.PathEscape(cal.URL) + "/events"
	nreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, newURL, bytes.NewReader(newPayload))
	nreq.Header.Set("Content-Type", "application/json")
	nresp, nerr := doGraphRequest(ctx, client, nreq)
	if nerr != nil {
		return PushInstanceResult{}, fmt.Errorf("graph post new series: %w", nerr)
	}
	defer nresp.Body.Close()
	if nresp.StatusCode != http.StatusOK && nresp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(nresp.Body, 4096))
		return PushInstanceResult{}, fmt.Errorf("graph post new series %d %s: %s",
			nresp.StatusCode, nresp.Status, strings.TrimSpace(string(body)))
	}
	var nout graphEvent
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

func (p microsoftProvider) findInstanceID(ctx context.Context, client *http.Client, masterEventID string, instanceTimeUnix int64) (string, error) {
	instanceTime := time.Unix(instanceTimeUnix, 0).UTC()
	start := instanceTime.Add(-25 * time.Hour).Format("2006-01-02T15:04:05")
	end := instanceTime.Add(25 * time.Hour).Format("2006-01-02T15:04:05")

	q := url.Values{}
	q.Set("startDateTime", start)
	q.Set("endDateTime", end)
	u := microsoftGraphBase + "/me/events/" + url.PathEscape(masterEventID) + "/instances?" + q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return "", fmt.Errorf("graph list instances: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("graph list instances %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	var page graphEventsResponse // {value: [...]}
	if derr := json.NewDecoder(resp.Body).Decode(&page); derr != nil {
		return "", fmt.Errorf("decode instances: %w", derr)
	}
	for _, ev := range page.Value {
		if ev.Start == nil {
			continue
		}
		t, perr := parseGraphDateTime(ev.Start.DateTime)
		if perr != nil {
			continue
		}
		// Match by Start (Graph instances are surfaced at their actual
		// instance start time; for an unmodified occurrence this matches
		// the master series' expansion).
		if t.UTC().Unix() == instanceTimeUnix {
			return ev.ID, nil
		}
	}
	return "", fmt.Errorf("microsoft: no instance found at unix %d", instanceTimeUnix)
}

func (p microsoftProvider) ListMicrosoftCalendars(ctx context.Context, src Source) ([]microsoftCalendarListEntry, error) {
	client, err := p.httpClient(src)
	if err != nil {
		return nil, err
	}
	var out []microsoftCalendarListEntry
	pageURL := microsoftGraphBase + "/me/calendars?$top=250&$select=id,name,canEdit,isDefaultCalendar"
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		resp, derr := doGraphRequest(ctx, client, req)
		if derr != nil {
			return nil, fmt.Errorf("graph calendars: %w", derr)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("graph calendars %d %s: %s",
				resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
		}

		var page microsoftCalendarListResponse
		decErr := json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode calendars: %w", decErr)
		}
		out = append(out, page.Value...)
		if page.NextLink == "" {
			return out, nil
		}
		pageURL = page.NextLink
	}
}
