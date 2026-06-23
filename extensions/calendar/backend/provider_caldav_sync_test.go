package backend

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCalDAVProvider_SyncCalendar_SkipsBadResponses locks in the issue #278
// fix: a calendar-query REPORT shaped like iCloud's — a leading self-
// referential collection <response> with empty <calendar-data/>, real events,
// and a malformed event — must yield the good events and skip the bad ones,
// instead of dropping everything the way go-webdav's QueryCalendar did.
//
// The fixture below is a faithful stand-in; a captured real iCloud response
// can be swapped in here later as a final check.
func TestCalDAVProvider_SyncCalendar_SkipsBadResponses(t *testing.T) {
	multistatus := `<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:response>
    <D:href>/cal/</D:href>
    <D:propstat><D:prop><D:getetag>"collection"</D:getetag><C:calendar-data></C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/cal/e1.ics</D:href>
    <D:propstat><D:prop><D:getetag>"e1"</D:getetag><C:calendar-data>` + sampleNonRecurringICS + `</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/cal/e2.ics</D:href>
    <D:propstat><D:prop><D:getetag>"e2"</D:getetag><C:calendar-data>` + sampleAllDayICS + `</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/cal/bad.ics</D:href>
    <D:propstat><D:prop><D:getetag>"bad"</D:getetag><C:calendar-data>BEGIN:VCALENDAR
GARBAGE</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
</D:multistatus>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(multistatus))
	}))
	defer srv.Close()

	store := newTestStore(t)
	now := time.Now().Unix()
	const srcID, calID = "src-cd", "cal-cd"
	if err := store.WithTx(func(tx *sql.Tx) error {
		if err := store.CreateSourceTx(tx, Source{
			ID: srcID, Type: SourceTypeCalDAV, Name: "iCloud",
			URL: srv.URL, Username: "u", Enabled: true, Writable: true, CreatedAt: now,
		}); err != nil {
			return err
		}
		// Relative href (as real CalDAV servers like Nextcloud/iCloud store
		// it) — SyncCalendar must resolve it against src.URL before the
		// REPORT. Regression guard for the "unsupported protocol scheme" bug.
		return store.CreateCalendarTx(tx, Calendar{
			ID: calID, SourceID: srcID, URL: "/cal/",
			DisplayName: "Personal", Visible: true, CreatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p := caldavProvider{store: store, secrets: fakeSecrets{password: "x"}}
	src, err := store.GetSource(srcID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	cals, err := store.ListCalendars(srcID)
	if err != nil || len(cals) != 1 {
		t.Fatalf("ListCalendars: %v (got %d)", err, len(cals))
	}

	if err := p.SyncCalendar(context.Background(), *src, cals[0]); err != nil {
		t.Fatalf("SyncCalendar returned error (should tolerate the bad responses): %v", err)
	}

	etags, err := store.ListEventETags(calID)
	if err != nil {
		t.Fatalf("ListEventETags: %v", err)
	}
	if len(etags) != 2 {
		t.Fatalf("expected 2 stored events (collection + malformed skipped), got %d: %v", len(etags), etags)
	}
	for _, uid := range []string{"non-recurring-1@example.com", "allday-1@example.com"} {
		if _, ok := etags[uid]; !ok {
			t.Errorf("expected event %q to be stored", uid)
		}
	}
}
