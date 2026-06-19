package backend

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMicrosoftProvider_SyncCalendar_ListAndStore exercises the events-collection
// sync (#278): SyncCalendar enumerates GET /me/calendars/{id}/events with $top
// (Prefer:odata.maxpagesize isn't honored there), translates single instances +
// series masters, and stores them — the master carrying a converted RRULE.
func TestMicrosoftProvider_SyncCalendar_ListAndStore(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// The seriesMaster triggers a per-master detail GET; answer it minimally
		// and only capture the list request's path/query.
		if strings.Contains(r.URL.Path, "/me/events/") {
			_, _ = w.Write([]byte(`{"id":"id-master"}`))
			return
		}
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"value":[
			{"id":"id-single","iCalUId":"uid-single","@odata.etag":"W/\"e1\"","subject":"One off","type":"singleInstance",
			 "start":{"dateTime":"2026-03-10T09:00:00.0000000","timeZone":"UTC"},
			 "end":{"dateTime":"2026-03-10T10:00:00.0000000","timeZone":"UTC"}},
			{"id":"id-master","iCalUId":"uid-master","@odata.etag":"W/\"e2\"","subject":"Weekly","type":"seriesMaster",
			 "start":{"dateTime":"2026-03-09T14:00:00.0000000","timeZone":"UTC"},
			 "end":{"dateTime":"2026-03-09T15:00:00.0000000","timeZone":"UTC"},
			 "recurrence":{"pattern":{"type":"weekly","interval":1,"daysOfWeek":["monday"],"firstDayOfWeek":"sunday"},
			               "range":{"type":"noEnd","startDate":"2026-03-09"}}}
		]}`))
	}))
	defer srv.Close()

	store := newTestStore(t)
	now := time.Now().Unix()
	const srcID, calID = "src-ms", "cal-ms"
	if err := store.WithTx(func(tx *sql.Tx) error {
		if err := store.CreateSourceTx(tx, Source{
			ID: srcID, Type: SourceTypeMicrosoft, Name: "TSC",
			AccountID: "acct-1", Enabled: true, CreatedAt: now,
		}); err != nil {
			return err
		}
		return store.CreateCalendarTx(tx, Calendar{
			ID: calID, SourceID: srcID, URL: "CALID",
			DisplayName: "Calendar", Visible: true, CreatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p := microsoftProvider{store: store, auth: fakeMSAuth{target: srv.URL}}
	src, err := store.GetSource(srcID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	cals, err := store.ListCalendars(srcID)
	if err != nil || len(cals) != 1 {
		t.Fatalf("ListCalendars: %v (%d)", err, len(cals))
	}

	if err := p.SyncCalendar(context.Background(), *src, cals[0]); err != nil {
		t.Fatalf("SyncCalendar: %v", err)
	}

	if !strings.Contains(gotPath, "/events") {
		t.Errorf("should hit the events collection; got path %q", gotPath)
	}
	if !strings.Contains(gotQuery, "%24top") && !strings.Contains(gotQuery, "$top") {
		t.Errorf("events list URL should request a page size via $top; got query %q", gotQuery)
	}

	etags, err := store.ListEventETags(calID)
	if err != nil {
		t.Fatalf("ListEventETags: %v", err)
	}
	if len(etags) != 2 {
		t.Fatalf("want 2 stored events, got %d", len(etags))
	}

	evs, err := store.ListEventsForExpansion([]string{calID})
	if err != nil {
		t.Fatalf("ListEventsForExpansion: %v", err)
	}
	var masterRRule string
	for _, e := range evs {
		if e.UID == "uid-master" {
			masterRRule = e.RRuleText
		}
	}
	if !strings.Contains(masterRRule, "FREQ=WEEKLY") || !strings.Contains(masterRRule, "BYDAY=MO") {
		t.Errorf("master should carry a weekly RRULE; got %q", masterRRule)
	}
}

// TestMicrosoftProvider_SyncCalendar_ExceptionsAndCancellations exercises Phase 2:
// a weekly master whose per-master GET returns one modified instance (moved 2pm→4pm)
// and one cancelled instance. After sync, expanding the series must place the
// modified instance at its new time and omit the cancelled date.
func TestMicrosoftProvider_SyncCalendar_ExceptionsAndCancellations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Per-master detail GET (/me/events/{id}) vs the list GET.
		if strings.Contains(r.URL.Path, "/me/events/") {
			_, _ = w.Write([]byte(`{
				"id":"id-master",
				"cancelledOccurrences":["OID.id-master.2026-03-23"],
				"exceptionOccurrences":[
					{"originalStart":"2026-03-16T14:00:00Z","subject":"Weekly (moved)",
					 "start":{"dateTime":"2026-03-16T16:00:00.0000000","timeZone":"UTC"},
					 "end":{"dateTime":"2026-03-16T17:00:00.0000000","timeZone":"UTC"}}
				]}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[
			{"id":"id-master","iCalUId":"uid-master","@odata.etag":"W/\"e2\"","subject":"Weekly","type":"seriesMaster",
			 "start":{"dateTime":"2026-03-09T14:00:00.0000000","timeZone":"UTC"},
			 "end":{"dateTime":"2026-03-09T15:00:00.0000000","timeZone":"UTC"},
			 "recurrence":{"pattern":{"type":"weekly","interval":1,"daysOfWeek":["monday"],"firstDayOfWeek":"sunday"},
			               "range":{"type":"noEnd","startDate":"2026-03-09"}}}
		]}`))
	}))
	defer srv.Close()

	store := newTestStore(t)
	now := time.Now().Unix()
	const srcID, calID = "src-ms", "cal-ms"
	if err := store.WithTx(func(tx *sql.Tx) error {
		if err := store.CreateSourceTx(tx, Source{
			ID: srcID, Type: SourceTypeMicrosoft, Name: "TSC",
			AccountID: "acct-1", Enabled: true, CreatedAt: now,
		}); err != nil {
			return err
		}
		return store.CreateCalendarTx(tx, Calendar{
			ID: calID, SourceID: srcID, URL: "CALID",
			DisplayName: "Calendar", Visible: true, CreatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p := microsoftProvider{store: store, auth: fakeMSAuth{target: srv.URL}}
	src, _ := store.GetSource(srcID)
	cals, _ := store.ListCalendars(srcID)
	if err := p.SyncCalendar(context.Background(), *src, cals[0]); err != nil {
		t.Fatalf("SyncCalendar: %v", err)
	}

	evs, err := store.ListEventsForExpansion([]string{calID})
	if err != nil || len(evs) != 1 {
		t.Fatalf("want 1 master stored, got %d (%v)", len(evs), err)
	}
	master := evs[0]

	overrides, err := store.ListOverrides(master.ID)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	wantRecID := time.Date(2026, 3, 16, 14, 0, 0, 0, time.UTC).Unix()
	if len(overrides) != 1 || overrides[0].RecurrenceIDUnix != wantRecID {
		t.Fatalf("want 1 override at %d, got %+v", wantRecID, overrides)
	}

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	inst, err := ExpandInRange(master, overrides, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// Mondays Mar 9/16/23/30: Mar 23 cancelled, Mar 16 moved to 16:00 → 3 instances.
	if len(inst) != 3 {
		t.Fatalf("want 3 instances (Mar 9, 16-moved, 30; Mar 23 cancelled), got %d", len(inst))
	}
	cancelled := time.Date(2026, 3, 23, 14, 0, 0, 0, time.UTC).Unix()
	movedTo := time.Date(2026, 3, 16, 16, 0, 0, 0, time.UTC).Unix()
	var sawMoved bool
	for _, in := range inst {
		if in.InstanceStartUnix == cancelled {
			t.Errorf("cancelled Mar 23 occurrence should be absent")
		}
		if in.InstanceStartUnix == movedTo {
			sawMoved = true
		}
	}
	if !sawMoved {
		t.Errorf("modified instance should appear at its new time (Mar 16 16:00)")
	}
}
