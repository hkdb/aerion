package backend

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/emersion/go-webdav"
	extcaldav "github.com/emersion/go-webdav/caldav"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/kit/davutil"
)

// caldavProvider — Provider impl for SourceTypeCalDAV.
//
// SyncCalendar lifts the PROPFIND/REPORT/diff/upsert logic from the old
// Syncer.syncCalendar verbatim. PushEvent + DeleteRemote are new for
// Chunk 2: raw *http.Client driven PUT/DELETE with conditional headers,
// because emersion/go-webdav v0.7.0's PutCalendarObject doesn't expose
// If-Match / If-None-Match (library TODO at client.go:399).

type caldavProvider struct {
	store   *Store
	secrets coreapi.Secrets
	events  coreapi.EventBus
}

func (caldavProvider) Capabilities() Capabilities {
	return Capabilities{
		CanWrite:        true,
		CanDeleteSeries: true,
		CanSetReminders: true,
	}
}

// --- Sync (lifted from Syncer.syncCalendar) --------------------------------

func (p caldavProvider) SyncCalendar(ctx context.Context, src Source, cal Calendar) error {
	password, err := p.secrets.Get(src.ID)
	if err != nil {
		return fmt.Errorf("load password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("no password stored for source — re-add it in settings")
	}

	httpClient := webdav.HTTPClientWithBasicAuth(
		davutil.NewHTTPClient(60*time.Second),
		src.Username, password,
	)
	client, err := extcaldav.NewClient(httpClient, src.URL)
	if err != nil {
		return fmt.Errorf("new caldav client: %w", err)
	}

	query := &extcaldav.CalendarQuery{
		CompRequest: extcaldav.CalendarCompRequest{
			Name:     "VCALENDAR",
			AllProps: true,
			AllComps: true,
		},
		CompFilter: extcaldav.CompFilter{
			Name: "VCALENDAR",
			Comps: []extcaldav.CompFilter{
				{Name: "VEVENT"},
			},
		},
	}

	objects, err := client.QueryCalendar(ctx, cal.URL, query)
	if err != nil {
		return fmt.Errorf("query calendar %q: %w", cal.DisplayName, err)
	}

	type serverEntry struct {
		etag   string
		href   string
		parsed *ParsedObject
		rawICS string
	}
	server := make(map[string]serverEntry, len(objects))
	for _, obj := range objects {
		if obj.Data == nil {
			continue
		}
		rawICS, encErr := encodeICS(obj.Data)
		if encErr != nil {
			continue
		}
		parsed, perr := ParseCalendarObject(rawICS)
		if perr != nil {
			continue
		}
		server[parsed.Master.UID] = serverEntry{
			etag:   obj.ETag,
			href:   obj.Path,
			parsed: parsed,
			rawICS: rawICS,
		}
	}

	localETags, err := p.store.ListEventETags(cal.ID)
	if err != nil {
		return fmt.Errorf("list local etags: %w", err)
	}

	return p.store.WithTx(func(tx *sql.Tx) error {
		// Upsert NEW + CHANGED.
		for uid, srv := range server {
			localETag, exists := localETags[uid]
			if exists && localETag == srv.etag && srv.etag != "" {
				continue
			}

			eventID := uuid.New().String()
			if exists {
				if existing, err := p.lookupEventIDByUID(cal.ID, uid); err == nil && existing != "" {
					eventID = existing
				}
			}

			ev := srv.parsed.Master
			ev.ID = eventID
			ev.CalendarID = cal.ID
			ev.ETag = srv.etag
			ev.Href = srv.href

			if err := p.store.UpsertEventTx(tx, ev); err != nil {
				return err
			}

			// Re-write overrides for this event. Inline DELETE because no
			// store helper exists; safe inside the tx.
			if _, err := tx.Exec(
				`DELETE FROM event_recurrence_overrides WHERE event_id = ?`,
				eventID,
			); err != nil {
				return fmt.Errorf("clear old overrides: %w", err)
			}
			for _, ov := range srv.parsed.Overrides {
				if err := p.store.UpsertOverrideTx(tx, eventID, ov.RecurrenceIDUnix, ov.ICSBlob); err != nil {
					return err
				}
			}

			// Compute VALARM instances for the next 7 days. INSERT OR IGNORE
			// in UpsertAlarmTx makes this idempotent across resyncs.
			now := time.Now()
			alarmWindow := now.Add(7 * 24 * time.Hour)
			instances, expErr := ExpandInRange(ev, srv.parsed.Overrides, now, alarmWindow)
			if expErr != nil {
				return fmt.Errorf("expand for alarms: %w", expErr)
			}
			alarms, aerr := ExtractAlarms(ev, srv.parsed.Overrides, instances)
			if aerr != nil {
				return fmt.Errorf("extract alarms: %w", aerr)
			}
			for _, a := range alarms {
				if err := p.store.UpsertAlarmTx(tx, a); err != nil {
					return err
				}
			}
		}

		// Delete events that disappeared from the server.
		for uid := range localETags {
			if _, stillOnServer := server[uid]; stillOnServer {
				continue
			}
			if err := p.store.DeleteEventByUIDTx(tx, cal.ID, uid); err != nil {
				return err
			}
		}

		return p.store.UpdateCalendarCtagTx(tx, cal.ID, "", time.Now().Unix())
	})
}

func (p caldavProvider) lookupEventIDByUID(calendarID, uid string) (string, error) {
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

// --- Write (PUT) -----------------------------------------------------------

// PushEvent writes ev to the server via HTTP PUT. ev.Href is set for
// updates; for creates the caller leaves it empty and we synthesize
// `{cal.URL}/{ev.UID}.ics`. Conditional headers ensure optimistic
// concurrency: If-Match for updates (412 on stale ETag), If-None-Match
// for creates (412 if the resource somehow already exists at our URL).
//
// Why raw *http.Client and not caldav.Client.PutCalendarObject:
// emersion/go-webdav v0.7.0's PutCalendarObject doesn't accept
// conditional headers (library TODO at client.go:399). Using raw
// http.Client + the same webdav.HTTPClientWithBasicAuth wrapper gives
// us auth + conditional headers + ETag-from-response in ~30 LOC. If
// the library adds support later, this method becomes the right place
// to swap.
func (p caldavProvider) PushEvent(ctx context.Context, src Source, cal Calendar, ev Event) (PushResult, error) {
	password, err := p.secrets.Get(src.ID)
	if err != nil {
		return PushResult{}, fmt.Errorf("load password: %w", err)
	}
	if password == "" {
		return PushResult{}, fmt.Errorf("no password stored for source — re-add it in settings")
	}

	// xmlfix-wrapped client: PUT responses on some servers (mailbox.org)
	// carry unquoted ETags; without the fix the new ETag fails to parse
	// and the retry-on-412 path breaks. Same builder as sync.
	httpClient := webdav.HTTPClientWithBasicAuth(
		davutil.NewHTTPClient(30*time.Second),
		src.Username, password,
	)

	// Discriminate create vs update by whether the caller passed an Href.
	// event_crud.go sets ev.Href ONLY after a successful create, so a
	// caller with empty Href is asking us to create a new resource;
	// non-empty Href is asking us to update an existing one. Using ETag
	// emptiness as the discriminator (the previous approach) breaks when
	// a CalDAV server doesn't return an ETag in its PUT response — the
	// local ETag stays empty after create, and a subsequent update would
	// incorrectly send `If-None-Match: *` and get 412 from the server.
	isCreate := ev.Href == ""
	href := ev.Href
	if isCreate {
		href = joinHref(cal.URL, ev.UID+".ics")
	}
	// Resolve relative paths against the source's base URL. CalDAV servers
	// (Nextcloud in particular) return calendar paths as server-relative
	// in PROPFIND responses, so cal.URL / ev.Href may lack scheme + host.
	// emersion/go-webdav handles this internally for its own methods;
	// raw http.Request needs an absolute URL or it errors with
	// "unsupported protocol scheme".
	href, err = absoluteHref(src.URL, href)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve href: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, href, strings.NewReader(ev.ICSBlob))
	if err != nil {
		return PushResult{}, fmt.Errorf("build PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	// Conditional headers — three cases:
	//   - create (Href empty)                 → If-None-Match: * (reject if exists)
	//   - update with known ETag              → If-Match: ETag    (optimistic concurrency)
	//   - update with no known local ETag     → no conditional    (unconditional PUT)
	if isCreate {
		req.Header.Set("If-None-Match", "*")
	}
	if !isCreate && ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return PushResult{}, fmt.Errorf("caldav PUT: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		// RFC 4791 says the response SHOULD carry the new ETag but
		// doesn't require it; Nextcloud often omits it. When absent,
		// HEAD the resource so the local copy has a fresh ETag for the
		// next conditional write — otherwise If-Match on the next edit
		// or delete 412s until the periodic sync catches up.
		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = fetchETagViaHEAD(ctx, httpClient, href)
		}
		return PushResult{ETag: etag}, nil
	case http.StatusPreconditionFailed:
		// 412 paths split by what we sent:
		//   - Update with If-Match (stale local ETag — most common cause):
		//     server-side state advanced since our last sync. The user
		//     just clicked Save / dragged the event — they have clear
		//     intent to update. Retry once unconditionally so the write
		//     lands; if there was a genuine concurrent edit we'll see it
		//     on the next sync.
		//   - Create with If-None-Match: * (resource already exists):
		//     genuine conflict, surface ErrConflict so the queue / UI can
		//     handle it.
		if !isCreate && ev.ETag != "" {
			return p.retryPutUnconditional(ctx, httpClient, href, ev.ICSBlob)
		}
		return PushResult{}, ErrConflict
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return PushResult{}, fmt.Errorf("caldav PUT %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// retryPutUnconditional re-sends a PUT without any If-Match / If-None-Match
// header so a stale local ETag (most common 412 cause on update) doesn't
// block the user's intent. Called only from the update path; creates
// surface their 412 as ErrConflict.
func (p caldavProvider) retryPutUnconditional(ctx context.Context, httpClient webdav.HTTPClient, href, blob string) (PushResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, href, strings.NewReader(blob))
	if err != nil {
		return PushResult{}, fmt.Errorf("build retry PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return PushResult{}, fmt.Errorf("caldav retry PUT: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = fetchETagViaHEAD(ctx, httpClient, href)
		}
		return PushResult{ETag: etag}, nil
	case http.StatusPreconditionFailed:
		// Unconditional PUT still rejected — something else is wrong
		// (rare; possibly server-side resource lock). Surface as conflict.
		return PushResult{}, ErrConflict
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return PushResult{}, fmt.Errorf("caldav retry PUT %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// fetchETagViaHEAD issues a HEAD against href and returns the ETag header
// value, or "" on any failure / non-2xx / missing header. Called after a
// successful PUT whose response had no ETag — common on Nextcloud, which
// RFC 4791 permits (SHOULD, not MUST) but breaks our If-Match path on the
// next write. One extra round trip per server-that-doesn't-return-ETag-on-PUT;
// servers that DO return ETag inline pay nothing. Failure is non-fatal:
// the retry-unconditional fallbacks on PUT/DELETE still catch the resulting
// 412 the next time the user writes to the resource.
func fetchETagViaHEAD(ctx context.Context, httpClient webdav.HTTPClient, href string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, href, nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	return resp.Header.Get("ETag")
}

// --- Delete ---------------------------------------------------------------

// DeleteRemote deletes ev's resource from the server. Honors If-Match
// for optimistic concurrency. Treats 404 as success (idempotent — if it's
// already gone, the local delete still needs to proceed).
func (p caldavProvider) DeleteRemote(ctx context.Context, src Source, cal Calendar, ev Event) error {
	if ev.Href == "" {
		// No href means it was never on the server (or sync hadn't run).
		// Local delete still proceeds; nothing to do here.
		return nil
	}

	password, err := p.secrets.Get(src.ID)
	if err != nil {
		return fmt.Errorf("load password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("no password stored for source — re-add it in settings")
	}

	// xmlfix-wrapped client — see PushEvent above for the rationale.
	httpClient := webdav.HTTPClientWithBasicAuth(
		davutil.NewHTTPClient(30*time.Second),
		src.Username, password,
	)

	href, err := absoluteHref(src.URL, ev.Href)
	if err != nil {
		return fmt.Errorf("resolve href: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, href, nil)
	if err != nil {
		return fmt.Errorf("build DELETE request: %w", err)
	}
	if ev.ETag != "" {
		req.Header.Set("If-Match", ev.ETag)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("caldav DELETE: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	case http.StatusPreconditionFailed:
		// 412 on a conditional DELETE almost always means our local ETag
		// is stale — Nextcloud (and others) don't return ETag on PUT
		// responses, so any earlier update left our local copy without
		// the new ETag. The user just clicked Delete; they have clear
		// intent. Mirror PushEvent's retry-unconditional pattern: drop
		// If-Match and try again. A genuine concurrent edit will show up
		// on next sync; we just unblock the delete in the common case.
		if ev.ETag != "" {
			return p.retryDeleteUnconditional(ctx, httpClient, href)
		}
		return ErrConflict
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("caldav DELETE %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// retryDeleteUnconditional re-sends a DELETE without If-Match so a stale
// local ETag (most common 412 cause on delete) doesn't block the user's
// intent. Mirror of retryPutUnconditional.
func (p caldavProvider) retryDeleteUnconditional(ctx context.Context, httpClient webdav.HTTPClient, href string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, href, nil)
	if err != nil {
		return fmt.Errorf("build retry DELETE request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("caldav retry DELETE: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	case http.StatusPreconditionFailed:
		// Unconditional DELETE still rejected — server-side lock or ACL
		// constraint. Surface as conflict.
		return ErrConflict
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("caldav retry DELETE %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}

// joinHref appends suffix to base preserving the URL shape. Uses path.Join
// after stripping/restoring any scheme://host prefix so the result keeps
// scheme + host (path.Join would mangle "https://" → "https:/").
func joinHref(base, suffix string) string {
	// Find the path part after scheme://host.
	if idx := strings.Index(base, "://"); idx != -1 {
		rest := base[idx+3:]
		slash := strings.Index(rest, "/")
		if slash == -1 {
			return base + "/" + suffix
		}
		hostPath := base[:idx+3+slash]
		urlPath := rest[slash:]
		return hostPath + path.Join(urlPath, suffix)
	}
	return path.Join(base, suffix)
}

// absoluteHref makes href absolute by resolving it against srcURL (the
// source's base URL) when href is a server-relative path. CalDAV servers
// like Nextcloud return calendar paths as "/remote.php/dav/calendars/..."
// in PROPFIND responses; emersion/go-webdav resolves these internally for
// its own methods, but raw http.Request needs a fully-qualified URL.
//
// Behavior:
//   - href already absolute (has scheme) → returned unchanged.
//   - href relative + srcURL absolute → resolved via url.ResolveReference.
//   - srcURL not parseable as absolute → error (we have nothing to anchor to).
func absoluteHref(srcURL, href string) (string, error) {
	if strings.Contains(href, "://") {
		return href, nil
	}
	base, err := url.Parse(srcURL)
	if err != nil {
		return "", fmt.Errorf("parse source URL %q: %w", srcURL, err)
	}
	if !base.IsAbs() {
		return "", fmt.Errorf("source URL %q is not absolute — cannot resolve %q", srcURL, href)
	}
	rel, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("parse href %q: %w", href, err)
	}
	return base.ResolveReference(rel).String(), nil
}
