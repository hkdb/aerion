package backend

// Tests for googleProvider: translation round-trip + PushEvent/DeleteRemote
// HTTP-level behavior. The translation tests verify ICS ↔ Google JSON
// preserves the load-bearing fields (UID, start/end, RRULE, VALARM); the
// HTTP tests use httptest.Server with a URL-rewriting transport so the
// production code's googleAPIBase const stays untouched.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// --- Fake Auth ---------------------------------------------------------------

// fakeAuth implements coreapi.Auth by returning an http.Client whose
// transport rewrites googleAPIBase URLs to the given httptest.Server URL.
// All other methods are unused stubs.
type fakeAuth struct{ target string }

func (f fakeAuth) HTTPClient(_ string, _ []coreapi.AuthScope) (*http.Client, error) {
	return &http.Client{Transport: rewriteTransport(f)}, nil
}

func (fakeAuth) IMAPClient(_ string, _ []string) (coreapi.IMAPClient, error) {
	return nil, errors.New("fakeAuth: IMAPClient not implemented")
}

func (fakeAuth) SMTPClient(_ string) (coreapi.SMTPClient, error) {
	return nil, errors.New("fakeAuth: SMTPClient not implemented")
}

func (fakeAuth) StartIncrementalConsent(_ coreapi.StartIncrementalConsentRequest) error {
	return errors.New("fakeAuth: StartIncrementalConsent not implemented")
}

var _ coreapi.Auth = fakeAuth{}

// rewriteTransport rewrites googleAPIBase to a test target.
type rewriteTransport struct{ target string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), googleAPIBase) {
		newURL := r.target + strings.TrimPrefix(req.URL.String(), googleAPIBase)
		u, err := url.Parse(newURL)
		if err != nil {
			return nil, err
		}
		req.URL = u
		req.Host = u.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

func newTestGoogleProvider(serverURL string) googleProvider {
	return googleProvider{
		store: nil, // PushEvent + DeleteRemote don't touch store
		auth:  fakeAuth{target: serverURL},
	}
}

// --- Translation: round-trip ------------------------------------------------

func TestGoogleTranslate_NonRecurringTimedRoundTrip(t *testing.T) {
	src := googleEvent{
		ICalUID: "evt-uid-1@aerion-google",
		Status:  "confirmed",
		Summary: "Project sync",
		Description: "Weekly project status",
		Location: "Room 4B",
		Start: &googleTimePoint{
			DateTime: "2026-06-10T14:00:00-07:00",
			TimeZone: "America/Los_Angeles",
		},
		End: &googleTimePoint{
			DateTime: "2026-06-10T15:00:00-07:00",
			TimeZone: "America/Los_Angeles",
		},
	}

	blob, err := translateGoogleEventToICS(src)
	if err != nil {
		t.Fatalf("translateGoogleEventToICS: %v", err)
	}
	if !strings.Contains(blob, "UID:evt-uid-1@aerion-google") {
		t.Errorf("blob missing UID:\n%s", blob)
	}
	if !strings.Contains(blob, "SUMMARY:Project sync") {
		t.Errorf("blob missing SUMMARY")
	}
	if !strings.Contains(blob, "TZID=America/Los_Angeles") {
		t.Errorf("blob missing TZID:\n%s", blob)
	}

	back, err := translateICSToGoogleJSON(blob)
	if err != nil {
		t.Fatalf("translateICSToGoogleJSON: %v", err)
	}
	if back.ICalUID != src.ICalUID {
		t.Errorf("UID round-trip: got %q, want %q", back.ICalUID, src.ICalUID)
	}
	if back.Summary != src.Summary {
		t.Errorf("Summary round-trip: got %q, want %q", back.Summary, src.Summary)
	}
	if back.Description != src.Description {
		t.Errorf("Description round-trip: got %q, want %q", back.Description, src.Description)
	}
	if back.Location != src.Location {
		t.Errorf("Location round-trip: got %q, want %q", back.Location, src.Location)
	}
	if back.Start == nil || back.End == nil {
		t.Fatalf("Start/End missing on round-trip")
	}
	if back.Start.TimeZone != src.Start.TimeZone {
		t.Errorf("Start TZ round-trip: got %q, want %q", back.Start.TimeZone, src.Start.TimeZone)
	}
	// Parse both DateTimes to compare instants (formats may differ slightly).
	srcStart, _ := time.Parse(time.RFC3339, src.Start.DateTime)
	backStart, _ := time.Parse(time.RFC3339, back.Start.DateTime)
	if !srcStart.Equal(backStart) {
		t.Errorf("Start instant: got %v, want %v", backStart, srcStart)
	}
}

func TestGoogleTranslate_AllDay(t *testing.T) {
	src := googleEvent{
		ICalUID: "alld@aerion-google",
		Status:  "confirmed",
		Summary: "Holiday",
		Start:   &googleTimePoint{Date: "2026-07-04"},
		End:     &googleTimePoint{Date: "2026-07-05"},
	}
	blob, err := translateGoogleEventToICS(src)
	if err != nil {
		t.Fatalf("translateGoogleEventToICS: %v", err)
	}
	if !strings.Contains(blob, "DTSTART;VALUE=DATE:20260704") {
		t.Errorf("blob missing all-day DTSTART:\n%s", blob)
	}

	back, err := translateICSToGoogleJSON(blob)
	if err != nil {
		t.Fatalf("translateICSToGoogleJSON: %v", err)
	}
	if back.Start.Date != "2026-07-04" {
		t.Errorf("Start.Date: got %q, want 2026-07-04", back.Start.Date)
	}
	if back.End.Date != "2026-07-05" {
		t.Errorf("End.Date: got %q, want 2026-07-05", back.End.Date)
	}
	if back.Start.DateTime != "" {
		t.Errorf("Start.DateTime should be empty for all-day, got %q", back.Start.DateTime)
	}
}

func TestGoogleTranslate_RecurringWithReminder(t *testing.T) {
	src := googleEvent{
		ICalUID: "rec@aerion-google",
		Status:  "confirmed",
		Summary: "Standup",
		Start: &googleTimePoint{
			DateTime: "2026-06-08T09:00:00Z",
			TimeZone: "UTC",
		},
		End: &googleTimePoint{
			DateTime: "2026-06-08T09:30:00Z",
			TimeZone: "UTC",
		},
		Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO"},
		Reminders: &googleReminders{
			UseDefault: false,
			Overrides:  []googleReminderOverride{{Method: "popup", Minutes: 15}},
		},
	}
	blob, err := translateGoogleEventToICS(src)
	if err != nil {
		t.Fatalf("translateGoogleEventToICS: %v", err)
	}
	if !strings.Contains(blob, "RRULE:FREQ=WEEKLY;BYDAY=MO") {
		t.Errorf("blob missing RRULE:\n%s", blob)
	}
	if !strings.Contains(blob, "BEGIN:VALARM") {
		t.Errorf("blob missing VALARM:\n%s", blob)
	}
	if !strings.Contains(blob, "TRIGGER:-PT15M") {
		t.Errorf("blob missing TRIGGER -PT15M:\n%s", blob)
	}

	back, err := translateICSToGoogleJSON(blob)
	if err != nil {
		t.Fatalf("translateICSToGoogleJSON: %v", err)
	}
	if len(back.Recurrence) != 1 || back.Recurrence[0] != "RRULE:FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("Recurrence round-trip: got %v", back.Recurrence)
	}
	if back.Reminders == nil || len(back.Reminders.Overrides) != 1 {
		t.Fatalf("Reminders not round-tripped: %+v", back.Reminders)
	}
	if back.Reminders.Overrides[0].Minutes != 15 {
		t.Errorf("Reminder minutes: got %d, want 15", back.Reminders.Overrides[0].Minutes)
	}
}

func TestGoogleTranslate_CancelledReturnsSentinel(t *testing.T) {
	_, err := translateGoogleEventToICS(googleEvent{
		ICalUID: "x@aerion-google",
		Status:  "cancelled",
	})
	if !errors.Is(err, errGoogleEventCancelled) {
		t.Errorf("translateGoogleEventToICS on cancelled = %v, want errGoogleEventCancelled", err)
	}
}

// --- PushEvent ---------------------------------------------------------------

type capturedReq struct {
	method      string
	path        string
	ifMatch     string
	body        googleEvent
	bodyRaw     string
}

func newTestServer(t *testing.T, handler func(req capturedReq, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var parsed googleEvent
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &parsed)
		}
		handler(capturedReq{
			method:  r.Method,
			path:    r.URL.Path,
			ifMatch: r.Header.Get("If-Match"),
			body:    parsed,
			bodyRaw: string(bodyBytes),
		}, w)
	}))
}

// helper: build a minimal ICS blob for an event with a known UID.
func minimalICSBlob(t *testing.T, uid string) string {
	t.Helper()
	blob, err := serializeVEVENT(uid, EventInput{
		Summary:     "Test",
		DTStartUnix: time.Date(2026, 6, 10, 14, 0, 0, 0, time.UTC).Unix(),
		DTEndUnix:   time.Date(2026, 6, 10, 15, 0, 0, 0, time.UTC).Unix(),
	})
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	return blob
}

func TestGoogleProvider_PushEvent_CreateSuccess(t *testing.T) {
	var got capturedReq
	srv := newTestServer(t, func(req capturedReq, w http.ResponseWriter) {
		got = req
		_ = json.NewEncoder(w).Encode(googleEvent{
			ID:      "server-event-id",
			ICalUID: req.body.ICalUID,
			ETag:    `"server-etag-1"`,
			Status:  "confirmed",
			Summary: req.body.Summary,
			Start:   req.body.Start,
			End:     req.body.End,
		})
	})
	defer srv.Close()

	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{UID: "evt@aerion-google", ICSBlob: minimalICSBlob(t, "evt@aerion-google")}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if result.ProviderEventID != "server-event-id" {
		t.Errorf("ProviderEventID = %q, want server-event-id", result.ProviderEventID)
	}
	if result.ETag != `"server-etag-1"` {
		t.Errorf("ETag = %q", result.ETag)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if !strings.HasSuffix(got.path, "/calendars/primary/events") {
		t.Errorf("path = %q, want suffix /calendars/primary/events", got.path)
	}
	if got.ifMatch != "" {
		t.Errorf("If-Match should be empty on create, got %q", got.ifMatch)
	}
	if got.body.ICalUID != "evt@aerion-google" {
		t.Errorf("payload iCalUID = %q", got.body.ICalUID)
	}
}

func TestGoogleProvider_PushEvent_UpdatePATCHWithIfMatch(t *testing.T) {
	var got capturedReq
	srv := newTestServer(t, func(req capturedReq, w http.ResponseWriter) {
		got = req
		_ = json.NewEncoder(w).Encode(googleEvent{
			ID:      "existing-id",
			ICalUID: req.body.ICalUID,
			ETag:    `"server-etag-2"`,
			Status:  "confirmed",
		})
	})
	defer srv.Close()

	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{
		UID:             "evt@aerion-google",
		ProviderEventID: "existing-id",
		ETag:            `"old-etag"`,
		ICSBlob:         minimalICSBlob(t, "evt@aerion-google"),
	}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if got.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", got.method)
	}
	if got.ifMatch != `"old-etag"` {
		t.Errorf("If-Match = %q, want \"old-etag\"", got.ifMatch)
	}
	if !strings.HasSuffix(got.path, "/calendars/primary/events/existing-id") {
		t.Errorf("path = %q", got.path)
	}
	if result.ETag != `"server-etag-2"` {
		t.Errorf("ETag = %q", result.ETag)
	}
}

func TestGoogleProvider_PushEvent_412Conflict(t *testing.T) {
	srv := newTestServer(t, func(_ capturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()

	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{
		UID:             "evt@aerion-google",
		ProviderEventID: "stale-id",
		ETag:            `"stale-etag"`,
		ICSBlob:         minimalICSBlob(t, "evt@aerion-google"),
	}
	_, err := p.PushEvent(t.Context(), src, cal, ev)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

// --- DeleteRemote ------------------------------------------------------------

func TestGoogleProvider_DeleteRemote_Success(t *testing.T) {
	var got capturedReq
	srv := newTestServer(t, func(req capturedReq, w http.ResponseWriter) {
		got = req
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{ProviderEventID: "abc-123", ETag: `"current"`}

	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}
	if got.ifMatch != `"current"` {
		t.Errorf("If-Match = %q", got.ifMatch)
	}
	if !strings.HasSuffix(got.path, "/events/abc-123") {
		t.Errorf("path = %q", got.path)
	}
}

func TestGoogleProvider_DeleteRemote_404Idempotent(t *testing.T) {
	srv := newTestServer(t, func(_ capturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()
	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{ProviderEventID: "missing"}
	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Errorf("DeleteRemote on 404 should be idempotent, got %v", err)
	}
}

func TestGoogleProvider_DeleteRemote_412Conflict(t *testing.T) {
	srv := newTestServer(t, func(_ capturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()
	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	ev := Event{ProviderEventID: "abc", ETag: `"stale"`}
	if err := p.DeleteRemote(t.Context(), src, cal, ev); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestGoogleProvider_DeleteRemote_NoProviderEventID(t *testing.T) {
	// If the event was never on the server (ProviderEventID empty),
	// DeleteRemote is a no-op so local delete proceeds.
	called := false
	srv := newTestServer(t, func(_ capturedReq, _ http.ResponseWriter) {
		called = true
	})
	defer srv.Close()
	p := newTestGoogleProvider(srv.URL)
	src := Source{ID: "src-g1", Type: SourceTypeGoogle, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-g1", URL: "primary"}
	if err := p.DeleteRemote(t.Context(), src, cal, Event{}); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if called {
		t.Errorf("DeleteRemote should not contact server when ProviderEventID is empty")
	}
}
