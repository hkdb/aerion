package backend

// Tests for microsoftProvider: translation round-trip + PushEvent /
// DeleteRemote + retry-on-429. Mirrors the Google test structure with a
// rewriteTransport that targets microsoftGraphBase instead of
// googleAPIBase.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// fakeMSAuth — same shape as fakeAuth in provider_google_test.go but
// rewrites microsoftGraphBase URLs.
type fakeMSAuth struct{ target string }

func (f fakeMSAuth) HTTPClient(_ string, _ []coreapi.AuthScope) (*http.Client, error) {
	return &http.Client{Transport: msRewriteTransport(f)}, nil
}

func (fakeMSAuth) IMAPClient(_ string, _ []string) (coreapi.IMAPClient, error) {
	return nil, errors.New("fakeMSAuth: IMAPClient not implemented")
}

func (fakeMSAuth) SMTPClient(_ string) (coreapi.SMTPClient, error) {
	return nil, errors.New("fakeMSAuth: SMTPClient not implemented")
}

func (fakeMSAuth) StartIncrementalConsent(_ coreapi.StartIncrementalConsentRequest) error {
	return errors.New("fakeMSAuth: StartIncrementalConsent not implemented")
}

var _ coreapi.Auth = fakeMSAuth{}

type msRewriteTransport struct{ target string }

func (r msRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), microsoftGraphBase) {
		newURL := r.target + strings.TrimPrefix(req.URL.String(), microsoftGraphBase)
		u, err := url.Parse(newURL)
		if err != nil {
			return nil, err
		}
		req.URL = u
		req.Host = u.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

func newTestMicrosoftProvider(serverURL string) microsoftProvider {
	return microsoftProvider{
		store: nil,
		auth:  fakeMSAuth{target: serverURL},
	}
}

// --- Translation: round-trip ------------------------------------------------

func TestMicrosoftTranslate_NonRecurringTimedRoundTrip(t *testing.T) {
	src := graphEvent{
		ICalUID: "evt-uid-1@aerion-microsoft",
		Subject: "Project sync",
		Body:    &graphBody{ContentType: "text", Content: "Weekly project status"},
		Location: &graphLocation{DisplayName: "Room 4B"},
		Start: &graphTimePoint{
			DateTime: "2026-06-10T21:00:00.0000000",
			TimeZone: "UTC",
		},
		End: &graphTimePoint{
			DateTime: "2026-06-10T22:00:00.0000000",
			TimeZone: "UTC",
		},
	}
	blob, err := translateGraphEventToICS(src)
	if err != nil {
		t.Fatalf("translateGraphEventToICS: %v", err)
	}
	if !strings.Contains(blob, "UID:evt-uid-1@aerion-microsoft") {
		t.Errorf("blob missing UID:\n%s", blob)
	}
	if !strings.Contains(blob, "SUMMARY:Project sync") {
		t.Errorf("blob missing SUMMARY:\n%s", blob)
	}
	if !strings.Contains(blob, "LOCATION:Room 4B") {
		t.Errorf("blob missing LOCATION:\n%s", blob)
	}

	back, err := translateICSToGraphEvent(blob)
	if err != nil {
		t.Fatalf("translateICSToGraphEvent: %v", err)
	}
	if back.ICalUID != src.ICalUID {
		t.Errorf("UID: got %q, want %q", back.ICalUID, src.ICalUID)
	}
	if back.Subject != src.Subject {
		t.Errorf("Subject: got %q", back.Subject)
	}
	if back.Body == nil || back.Body.Content != src.Body.Content {
		t.Errorf("Body: got %+v", back.Body)
	}
	if back.Location == nil || back.Location.DisplayName != src.Location.DisplayName {
		t.Errorf("Location: got %+v", back.Location)
	}
	if back.Start == nil || back.End == nil {
		t.Fatalf("Start/End missing on round-trip")
	}
	if back.Start.TimeZone != "UTC" {
		t.Errorf("Start TZ: got %q, want UTC", back.Start.TimeZone)
	}
}

func TestMicrosoftTranslate_AllDay(t *testing.T) {
	allDay := true
	src := graphEvent{
		ICalUID:  "alld@aerion-microsoft",
		Subject:  "Holiday",
		Start:    &graphTimePoint{DateTime: "2026-07-04T00:00:00.0000000", TimeZone: "UTC"},
		End:      &graphTimePoint{DateTime: "2026-07-05T00:00:00.0000000", TimeZone: "UTC"},
		IsAllDay: &allDay,
	}
	blob, err := translateGraphEventToICS(src)
	if err != nil {
		t.Fatalf("translateGraphEventToICS: %v", err)
	}
	if !strings.Contains(blob, "DTSTART;VALUE=DATE:20260704") {
		t.Errorf("blob missing all-day DTSTART:\n%s", blob)
	}

	back, err := translateICSToGraphEvent(blob)
	if err != nil {
		t.Fatalf("translateICSToGraphEvent: %v", err)
	}
	if back.IsAllDay == nil || !*back.IsAllDay {
		t.Errorf("IsAllDay should be true on round-trip")
	}
}

func TestMicrosoftTranslate_RecurringWeeklyWithReminder(t *testing.T) {
	reminder := 15
	reminderOn := true
	src := graphEvent{
		ICalUID: "rec@aerion-microsoft",
		Subject: "Standup",
		Start:   &graphTimePoint{DateTime: "2026-06-08T09:00:00.0000000", TimeZone: "UTC"}, // Monday
		End:     &graphTimePoint{DateTime: "2026-06-08T09:30:00.0000000", TimeZone: "UTC"},
		Recurrence: &graphRecurrence{
			Pattern: graphPattern{
				Type:       "weekly",
				Interval:   1,
				DaysOfWeek: []string{"monday"},
			},
			Range: graphRange{Type: "noEnd", StartDate: "2026-06-08"},
		},
		ReminderMinutesBeforeStart: &reminder,
		IsReminderOn:               &reminderOn,
	}
	blob, err := translateGraphEventToICS(src)
	if err != nil {
		t.Fatalf("translateGraphEventToICS: %v", err)
	}
	if !strings.Contains(blob, "FREQ=WEEKLY") {
		t.Errorf("blob missing FREQ=WEEKLY:\n%s", blob)
	}
	if !strings.Contains(blob, "BYDAY=MO") {
		t.Errorf("blob missing BYDAY=MO:\n%s", blob)
	}
	if !strings.Contains(blob, "TRIGGER:-PT15M") {
		t.Errorf("blob missing TRIGGER -PT15M:\n%s", blob)
	}

	back, err := translateICSToGraphEvent(blob)
	if err != nil {
		t.Fatalf("translateICSToGraphEvent: %v", err)
	}
	if back.Recurrence == nil {
		t.Fatalf("Recurrence missing on round-trip")
	}
	if back.Recurrence.Pattern.Type != "weekly" {
		t.Errorf("Pattern.Type = %q, want weekly", back.Recurrence.Pattern.Type)
	}
	if len(back.Recurrence.Pattern.DaysOfWeek) != 1 || back.Recurrence.Pattern.DaysOfWeek[0] != "monday" {
		t.Errorf("DaysOfWeek = %v, want [monday]", back.Recurrence.Pattern.DaysOfWeek)
	}
	if back.ReminderMinutesBeforeStart == nil || *back.ReminderMinutesBeforeStart != 15 {
		t.Errorf("Reminder minutes: %v", back.ReminderMinutesBeforeStart)
	}
}

func TestMicrosoftTranslate_PatternMappings(t *testing.T) {
	tests := []struct {
		name        string
		rrule       string
		dtstart     time.Time
		wantType    string
		wantDays    []string
		wantDayOfM  int
		wantMonth   int
		wantRangeType string
		wantCount   int
	}{
		{
			name:     "DAILY",
			rrule:    "FREQ=DAILY",
			dtstart:  time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC),
			wantType: "daily",
			wantRangeType: "noEnd",
		},
		{
			name:     "WEEKLY with composer default (single day from DTSTART)",
			rrule:    "FREQ=WEEKLY",
			dtstart:  time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC), // Wed
			wantType: "weekly",
			wantDays: []string{"wednesday"},
			wantRangeType: "noEnd",
		},
		{
			name:     "WEEKLY with BYDAY",
			rrule:    "FREQ=WEEKLY;BYDAY=MO,WE,FR",
			dtstart:  time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC),
			wantType: "weekly",
			wantDays: []string{"monday", "wednesday", "friday"},
			wantRangeType: "noEnd",
		},
		{
			name:       "MONTHLY uses DTSTART day",
			rrule:      "FREQ=MONTHLY",
			dtstart:    time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
			wantType:   "absoluteMonthly",
			wantDayOfM: 15,
			wantRangeType: "noEnd",
		},
		{
			name:       "YEARLY from DTSTART month + day",
			rrule:      "FREQ=YEARLY",
			dtstart:    time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC),
			wantType:   "absoluteYearly",
			wantDayOfM: 4,
			wantMonth:  7,
			wantRangeType: "noEnd",
		},
		{
			name:     "COUNT",
			rrule:    "FREQ=WEEKLY;COUNT=10",
			dtstart:  time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC),
			wantType: "weekly",
			wantDays: []string{"wednesday"},
			wantRangeType: "numbered",
			wantCount: 10,
		},
		{
			name:     "UNTIL",
			rrule:    "FREQ=WEEKLY;UNTIL=20261231T235959Z",
			dtstart:  time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC),
			wantType: "weekly",
			wantDays: []string{"wednesday"},
			wantRangeType: "endDate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := rruleToGraphRecurrence(tt.rrule, tt.dtstart)
			if err != nil {
				t.Fatalf("rruleToGraphRecurrence: %v", err)
			}
			if rec.Pattern.Type != tt.wantType {
				t.Errorf("Pattern.Type = %q, want %q", rec.Pattern.Type, tt.wantType)
			}
			if tt.wantDays != nil {
				if !equalStrings(rec.Pattern.DaysOfWeek, tt.wantDays) {
					t.Errorf("DaysOfWeek = %v, want %v", rec.Pattern.DaysOfWeek, tt.wantDays)
				}
			}
			if tt.wantDayOfM != 0 && rec.Pattern.DayOfMonth != tt.wantDayOfM {
				t.Errorf("DayOfMonth = %d, want %d", rec.Pattern.DayOfMonth, tt.wantDayOfM)
			}
			if tt.wantMonth != 0 && rec.Pattern.Month != tt.wantMonth {
				t.Errorf("Month = %d, want %d", rec.Pattern.Month, tt.wantMonth)
			}
			if rec.Range.Type != tt.wantRangeType {
				t.Errorf("Range.Type = %q, want %q", rec.Range.Type, tt.wantRangeType)
			}
			if tt.wantCount != 0 && rec.Range.NumberOfOccurrences != tt.wantCount {
				t.Errorf("NumberOfOccurrences = %d, want %d", rec.Range.NumberOfOccurrences, tt.wantCount)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func TestMicrosoftTranslate_CancelledReturnsSentinel(t *testing.T) {
	_, err := translateGraphEventToICS(graphEvent{
		ICalUID: "x@aerion-microsoft",
		Status:  &graphEventStatus{Reason: "deleted"},
	})
	if !errors.Is(err, errMicrosoftEventCancelled) {
		t.Errorf("translateGraphEventToICS on cancelled = %v, want errMicrosoftEventCancelled", err)
	}
}

// --- PushEvent ---------------------------------------------------------------

type msCapturedReq struct {
	method      string
	path        string
	ifMatch     string
	prefer      string
	contentType string
	body        graphEvent
	bodyRaw     string
}

func newMSTestServer(t *testing.T, handler func(req msCapturedReq, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var parsed graphEvent
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &parsed)
		}
		handler(msCapturedReq{
			method:      r.Method,
			path:        r.URL.Path,
			ifMatch:     r.Header.Get("If-Match"),
			prefer:      r.Header.Get("Prefer"),
			contentType: r.Header.Get("Content-Type"),
			body:        parsed,
			bodyRaw:     string(bodyBytes),
		}, w)
	}))
}

func msMinimalICSBlob(t *testing.T, uid string) string {
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

func TestMicrosoftProvider_PushEvent_CreateSuccess(t *testing.T) {
	var got msCapturedReq
	srv := newMSTestServer(t, func(req msCapturedReq, w http.ResponseWriter) {
		got = req
		_ = json.NewEncoder(w).Encode(graphEvent{
			ID:      "server-event-id",
			ICalUID: req.body.ICalUID,
			ETag:    `W/"server-etag-1"`,
			Subject: req.body.Subject,
			Start:   req.body.Start,
			End:     req.body.End,
		})
	})
	defer srv.Close()

	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{UID: "evt@aerion-microsoft", ICSBlob: msMinimalICSBlob(t, "evt@aerion-microsoft")}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if result.ProviderEventID != "server-event-id" {
		t.Errorf("ProviderEventID = %q", result.ProviderEventID)
	}
	if result.ETag != `W/"server-etag-1"` {
		t.Errorf("ETag = %q", result.ETag)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if !strings.HasSuffix(got.path, "/me/calendars/calendar-id-abc/events") {
		t.Errorf("path = %q", got.path)
	}
	if got.prefer != microsoftPreferTZ {
		t.Errorf("Prefer header = %q, want %q", got.prefer, microsoftPreferTZ)
	}
}

func TestMicrosoftProvider_PushEvent_UpdatePATCHWithIfMatch(t *testing.T) {
	// PushEvent now does a GET right before the PATCH to learn the FRESH
	// etag from the server, then sends that as If-Match. The caller's
	// cached etag is ignored — this eliminates stale-cache 412s that
	// otherwise poison every first edit (Graph mutates etags out-of-band
	// for background indexing).
	var patchReq msCapturedReq
	const freshServerETag = `W/"server-etag-2"`
	srv := newMSTestServer(t, func(req msCapturedReq, w http.ResponseWriter) {
		// Distinguish the etag-refresh GET from the PATCH.
		if req.method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(graphEvent{
				ID:   "existing-id",
				ETag: freshServerETag,
			})
			return
		}
		patchReq = req
		_ = json.NewEncoder(w).Encode(graphEvent{
			ID:      "existing-id",
			ICalUID: req.body.ICalUID,
			ETag:    `W/"server-etag-3"`,
		})
	})
	defer srv.Close()

	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{
		UID:             "evt@aerion-microsoft",
		ProviderEventID: "existing-id",
		ETag:            `W/"old-etag"`, // deliberately stale — must be overwritten by GET
		ICSBlob:         msMinimalICSBlob(t, "evt@aerion-microsoft"),
	}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if patchReq.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", patchReq.method)
	}
	if !strings.HasSuffix(patchReq.path, "/me/events/existing-id") {
		t.Errorf("path = %q, want suffix /me/events/existing-id", patchReq.path)
	}
	// The fresh etag from the GET must be what's sent on the PATCH, not
	// the caller's stale `W/"old-etag"`.
	if patchReq.ifMatch != freshServerETag {
		t.Errorf("If-Match = %q, want %q (fresh from GET, not caller's stale cache)", patchReq.ifMatch, freshServerETag)
	}
	if result.ETag != `W/"server-etag-3"` {
		t.Errorf("ETag = %q", result.ETag)
	}
}

func TestMicrosoftProvider_PushEvent_412Conflict(t *testing.T) {
	srv := newMSTestServer(t, func(_ msCapturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()
	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{
		UID:             "evt@aerion-microsoft",
		ProviderEventID: "stale-id",
		ETag:            `W/"stale-etag"`,
		ICSBlob:         msMinimalICSBlob(t, "evt@aerion-microsoft"),
	}
	_, err := p.PushEvent(t.Context(), src, cal, ev)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestMicrosoftProvider_PushEvent_429RetryAfterSucceeds(t *testing.T) {
	var hits int32
	srv := newMSTestServer(t, func(_ msCapturedReq, w http.ResponseWriter) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(graphEvent{
			ID:   "retried-id",
			ETag: `W/"after-retry-etag"`,
		})
	})
	defer srv.Close()

	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{UID: "evt@aerion-microsoft", ICSBlob: msMinimalICSBlob(t, "evt@aerion-microsoft")}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("server hits = %d, want 2 (one 429 + one success)", hits)
	}
	if result.ProviderEventID != "retried-id" {
		t.Errorf("ProviderEventID = %q", result.ProviderEventID)
	}
}

// --- DeleteRemote ------------------------------------------------------------

func TestMicrosoftProvider_DeleteRemote_Success(t *testing.T) {
	var got msCapturedReq
	srv := newMSTestServer(t, func(req msCapturedReq, w http.ResponseWriter) {
		got = req
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{ProviderEventID: "evt-123", ETag: `W/"current"`}

	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}
	if !strings.HasSuffix(got.path, "/me/events/evt-123") {
		t.Errorf("path = %q", got.path)
	}
	if got.ifMatch != `W/"current"` {
		t.Errorf("If-Match = %q", got.ifMatch)
	}
}

func TestMicrosoftProvider_DeleteRemote_404Idempotent(t *testing.T) {
	srv := newMSTestServer(t, func(_ msCapturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()
	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{ProviderEventID: "missing"}
	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Errorf("DeleteRemote on 404 should be idempotent, got %v", err)
	}
}

func TestMicrosoftProvider_DeleteRemote_412Conflict(t *testing.T) {
	srv := newMSTestServer(t, func(_ msCapturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()
	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	ev := Event{ProviderEventID: "abc", ETag: `W/"stale"`}
	if err := p.DeleteRemote(t.Context(), src, cal, ev); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestMicrosoftProvider_DeleteRemote_NoProviderEventID(t *testing.T) {
	called := false
	srv := newMSTestServer(t, func(_ msCapturedReq, _ http.ResponseWriter) {
		called = true
	})
	defer srv.Close()
	p := newTestMicrosoftProvider(srv.URL)
	src := Source{ID: "src-m1", Type: SourceTypeMicrosoft, AccountID: "acct-1"}
	cal := Calendar{ID: "cal-m1", URL: "calendar-id-abc"}
	if err := p.DeleteRemote(t.Context(), src, cal, Event{}); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if called {
		t.Errorf("DeleteRemote should not contact server when ProviderEventID is empty")
	}
}

// --- parseGraphRetryAfter ----------------------------------------------------

func TestParseGraphRetryAfter(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"", 2 * time.Second},
		{"3", 3 * time.Second},
		{"0", 0},
		{"120", 60 * time.Second}, // clamped
		{"banana", 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseGraphRetryAfter(tt.input)
			if got != tt.want {
				t.Errorf("parseGraphRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Compile-time check that strconv import is exercised even when only used
// in the test pattern paths above.
var _ = strconv.Itoa

// planEventSync is the pure decision behind the M365 events-collection sync
// (#278): only new/etag-changed masters + single instances are processed;
// unchanged events are skipped (the win on large read-only calendars);
// exceptions (seriesMasterId set) are ignored (synced via their master); local
// events no longer present server-side are deleted; and an empty listing
// suppresses deletes so a glitchy/empty pull can't wipe the calendar.
func TestPlanEventSync(t *testing.T) {
	rows := []graphEvent{
		{ICalUID: "s1", ETag: "e2new"},                        // single, changed
		{ICalUID: "x1", SeriesMasterID: "m1"},                 // exception — skip entirely
		{ICalUID: "n1", ETag: "e3"},                           // new single
		{ICalUID: "u1", ETag: "eu"},                           // single, unchanged
		{ICalUID: "mm", ETag: "em", Type: "seriesMaster"},     // master, unchanged etag
		{ICalUID: ""},                                         // no UID — skip
		{ICalUID: "s1", ETag: "e2new"},                        // duplicate — dedupe
	}
	local := map[string]string{
		"s1":   "e2old", // changed → processed
		"u1":   "eu",    // unchanged → not processed, but seen (kept)
		"mm":   "em",    // unchanged etag, but it's a master → processed anyway
		"old1": "eOld",  // gone from server → delete
	}

	plan := planEventSync(rows, local)

	if !plan.seenAny {
		t.Fatal("seenAny should be true (server returned events)")
	}
	gotProcess := map[string]bool{}
	for _, r := range plan.process {
		gotProcess[r.ICalUID] = true
	}
	for _, want := range []string{"s1", "n1", "mm"} { // changed + new + master(always)
		if !gotProcess[want] {
			t.Errorf("process missing %q", want)
		}
	}
	for _, notWant := range []string{"u1", "x1", ""} { // unchanged single / exception / empty
		if gotProcess[notWant] {
			t.Errorf("process should not contain %q", notWant)
		}
	}
	if len(plan.process) != 3 {
		t.Errorf("got %d process, want 3 (s1 changed, n1 new, mm master)", len(plan.process))
	}

	gotDel := map[string]bool{}
	for _, uid := range plan.deletes {
		gotDel[uid] = true
	}
	if !gotDel["old1"] {
		t.Errorf("deletes should contain old1 (gone from server)")
	}
	for _, notDel := range []string{"m1", "s1"} {
		if gotDel[notDel] {
			t.Errorf("deletes should NOT contain %q (present/unchanged server-side)", notDel)
		}
	}
	if len(plan.deletes) != 1 {
		t.Errorf("got %d deletes, want 1 (old1)", len(plan.deletes))
	}
}

// An empty server listing must not produce a delete-everything plan.
func TestPlanEventSync_EmptyListSuppressesDeletes(t *testing.T) {
	local := map[string]string{"a": "1", "b": "2"}
	plan := planEventSync(nil, local)
	if plan.seenAny {
		t.Error("seenAny should be false for an empty listing")
	}
	// deletes may be computed, but seenAny=false tells the caller to skip them.
	if len(plan.process) != 0 {
		t.Errorf("got %d process, want 0", len(plan.process))
	}
}
