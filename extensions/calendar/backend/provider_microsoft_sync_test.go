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

// TestMicrosoftProvider_SyncCalendar_NoTopUsesPreferHeader guards the Graph
// events-delta fix: Graph rejects $top on the SyncEvents resource ("The
// '$top' parameter is not supported with change tracking…"), so page size must
// be requested via the Prefer: odata.maxpagesize header instead.
func TestMicrosoftProvider_SyncCalendar_NoTopUsesPreferHeader(t *testing.T) {
	var gotQuery, gotPrefer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotPrefer = r.Header.Get("Prefer")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"value":[],"@odata.deltaLink":"https://example.test/delta?$deltatoken=abc"}`))
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

	if strings.Contains(gotQuery, "$top") {
		t.Errorf("events-delta URL must not contain $top (Graph rejects it on SyncEvents); got query %q", gotQuery)
	}
	if !strings.HasPrefix(gotPrefer, "odata.maxpagesize=") {
		t.Errorf("expected a Prefer: odata.maxpagesize header on the delta request; got %q", gotPrefer)
	}
}
