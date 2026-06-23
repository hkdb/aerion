package backend

// Tests for caldavProvider's PushEvent + DeleteRemote — the new transport
// surface introduced in Phase 2 Chunk 2. Uses httptest.NewServer to record
// requests sent by the provider, then asserts on method, URL, headers, and
// status-code handling (200/201/204 success, 412 → ErrConflict, 404 idempotent
// on DELETE, other → wrapped error).
//
// SyncCalendar isn't covered here — its body was lifted verbatim from the
// original Syncer.syncCalendar, and the existing calendar backend tests +
// manual sync coverage exercise it indirectly. A dedicated httptest-based
// SyncCalendar test is tracked as a follow-up.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// fakeSecrets satisfies coreapi.Secrets with a static password. Only Get is
// exercised by caldavProvider — the rest stub safely.
type fakeSecrets struct{ password string }

func (f fakeSecrets) Set(_, _ string) error       { return nil }
func (f fakeSecrets) Get(_ string) (string, error) { return f.password, nil }
func (f fakeSecrets) Delete(_ string) error       { return nil }
func (f fakeSecrets) DeleteAll() error            { return nil }

var _ coreapi.Secrets = fakeSecrets{}

// recordedRequest captures what the provider sent so tests can assert.
type recordedRequest struct {
	method      string
	path        string
	ifMatch     string
	ifNoneMatch string
	contentType string
	body        string
}

// newFakeCalDAVServer returns an httptest.Server that records each request and
// hands the response back through the supplied handler. The handler can read
// `req` and set headers + status on `w` to simulate any CalDAV server.
func newFakeCalDAVServer(t *testing.T, handler func(req recordedRequest, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		handler(recordedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			ifMatch:     r.Header.Get("If-Match"),
			ifNoneMatch: r.Header.Get("If-None-Match"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		}, w)
	}))
}

// newTestProvider builds a caldavProvider wired to fakeSecrets. store is nil
// because PushEvent + DeleteRemote don't touch the store.
func newTestProvider(password string) caldavProvider {
	return caldavProvider{
		store:   nil,
		secrets: fakeSecrets{password: password},
		events:  nil,
	}
}

func TestCalDAVProvider_PushEvent_CreateSuccess(t *testing.T) {
	var got recordedRequest
	srv := newFakeCalDAVServer(t, func(req recordedRequest, w http.ResponseWriter) {
		got = req
		w.Header().Set("ETag", `"server-etag-1"`)
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{UID: "abc@aerion-caldav", ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if result.ETag != `"server-etag-1"` {
		t.Errorf("ETag = %q, want %q", result.ETag, `"server-etag-1"`)
	}
	if got.method != http.MethodPut {
		t.Errorf("method = %q, want PUT", got.method)
	}
	if got.ifNoneMatch != "*" {
		t.Errorf("If-None-Match = %q, want *", got.ifNoneMatch)
	}
	if got.ifMatch != "" {
		t.Errorf("If-Match should be empty on create, got %q", got.ifMatch)
	}
	if !strings.HasSuffix(got.path, "/abc@aerion-caldav.ics") {
		t.Errorf("path = %q, want suffix /abc@aerion-caldav.ics", got.path)
	}
	if !strings.HasPrefix(got.contentType, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar prefix", got.contentType)
	}
	if got.body == "" {
		t.Errorf("body should not be empty")
	}
}

func TestCalDAVProvider_PushEvent_UpdateSuccess(t *testing.T) {
	var got recordedRequest
	srv := newFakeCalDAVServer(t, func(req recordedRequest, w http.ResponseWriter) {
		got = req
		w.Header().Set("ETag", `"server-etag-2"`)
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{
		UID:     "abc@aerion-caldav",
		Href:    srv.URL + "/calendars/user/personal/abc.ics",
		ETag:    `"old-etag"`,
		ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n",
	}

	result, err := p.PushEvent(t.Context(), src, cal, ev)
	if err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if result.ETag != `"server-etag-2"` {
		t.Errorf("ETag = %q, want %q", result.ETag, `"server-etag-2"`)
	}
	if got.ifMatch != `"old-etag"` {
		t.Errorf("If-Match = %q, want %q", got.ifMatch, `"old-etag"`)
	}
	if got.ifNoneMatch != "" {
		t.Errorf("If-None-Match should be empty on update, got %q", got.ifNoneMatch)
	}
	if got.path != "/calendars/user/personal/abc.ics" {
		t.Errorf("path = %q, want /calendars/user/personal/abc.ics", got.path)
	}
}

func TestCalDAVProvider_PushEvent_Conflict(t *testing.T) {
	srv := newFakeCalDAVServer(t, func(_ recordedRequest, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{
		UID:     "abc@aerion-caldav",
		Href:    srv.URL + "/calendars/user/personal/abc.ics",
		ETag:    `"stale-etag"`,
		ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n",
	}

	_, err := p.PushEvent(t.Context(), src, cal, ev)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("PushEvent err = %v, want ErrConflict", err)
	}
}

func TestCalDAVProvider_PushEvent_ServerError(t *testing.T) {
	srv := newFakeCalDAVServer(t, func(_ recordedRequest, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{UID: "abc@aerion-caldav", ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"}

	_, err := p.PushEvent(t.Context(), src, cal, ev)
	if err == nil {
		t.Fatal("PushEvent expected error on 500, got nil")
	}
	if errors.Is(err, ErrConflict) {
		t.Errorf("PushEvent returned ErrConflict on 500, want generic error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got: %v", err)
	}
}

func TestCalDAVProvider_DeleteRemote_Success(t *testing.T) {
	var got recordedRequest
	srv := newFakeCalDAVServer(t, func(req recordedRequest, w http.ResponseWriter) {
		got = req
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{
		UID:  "abc@aerion-caldav",
		Href: srv.URL + "/calendars/user/personal/abc.ics",
		ETag: `"current-etag"`,
	}

	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}
	if got.ifMatch != `"current-etag"` {
		t.Errorf("If-Match = %q, want %q", got.ifMatch, `"current-etag"`)
	}
	if got.path != "/calendars/user/personal/abc.ics" {
		t.Errorf("path = %q, want /calendars/user/personal/abc.ics", got.path)
	}
}

func TestCalDAVProvider_DeleteRemote_NotFoundIdempotent(t *testing.T) {
	srv := newFakeCalDAVServer(t, func(_ recordedRequest, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{Href: srv.URL + "/calendars/user/personal/gone.ics", ETag: `"x"`}

	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Errorf("DeleteRemote on 404 should be idempotent, got: %v", err)
	}
}

func TestCalDAVProvider_DeleteRemote_Conflict(t *testing.T) {
	srv := newFakeCalDAVServer(t, func(_ recordedRequest, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPreconditionFailed)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{Href: srv.URL + "/calendars/user/personal/abc.ics", ETag: `"stale"`}

	err := p.DeleteRemote(t.Context(), src, cal, ev)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteRemote err = %v, want ErrConflict", err)
	}
}

func TestCalDAVProvider_DeleteRemote_NoHrefSkips(t *testing.T) {
	// If ev.Href is empty (event was never on server, sync hadn't run),
	// DeleteRemote should be a no-op so the local delete still proceeds.
	called := false
	srv := newFakeCalDAVServer(t, func(_ recordedRequest, _ http.ResponseWriter) {
		called = true
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{ID: "src-1", Type: SourceTypeCalDAV, Username: "user", URL: srv.URL}
	cal := Calendar{ID: "cal-1", URL: srv.URL + "/calendars/user/personal/"}
	ev := Event{} // empty Href

	if err := p.DeleteRemote(t.Context(), src, cal, ev); err != nil {
		t.Errorf("DeleteRemote with empty Href should be nil, got: %v", err)
	}
	if called {
		t.Errorf("DeleteRemote with empty Href should not call the server")
	}
}

func TestCalDAVProvider_PushEvent_RelativeCalURLNextcloud(t *testing.T) {
	// Nextcloud (and other CalDAV servers) return calendar paths as
	// server-relative (no scheme/host). Regression test: PushEvent must
	// resolve the relative path against src.URL before issuing PUT, or
	// http.Request errors with "unsupported protocol scheme".
	var got recordedRequest
	srv := newFakeCalDAVServer(t, func(req recordedRequest, w http.ResponseWriter) {
		got = req
		w.Header().Set("ETag", `"nc-etag"`)
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	p := newTestProvider("secret")
	src := Source{
		ID:       "src-nc",
		Type:     SourceTypeCalDAV,
		Username: "user",
		// src.URL is absolute (server scheme+host) — typical of homePath
		// returned by Nextcloud's CalDAV discovery.
		URL: srv.URL + "/remote.php/dav/calendars/user/",
	}
	cal := Calendar{
		ID: "cal-nc",
		// cal.URL is server-relative — typical of Nextcloud's PROPFIND
		// multistatus responses.
		URL: "/remote.php/dav/calendars/user/personal/",
	}
	ev := Event{UID: "evt@aerion-caldav", ICSBlob: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"}

	if _, err := p.PushEvent(t.Context(), src, cal, ev); err != nil {
		t.Fatalf("PushEvent: %v", err)
	}
	if got.path != "/remote.php/dav/calendars/user/personal/evt@aerion-caldav.ics" {
		t.Errorf("path = %q, want /remote.php/dav/calendars/user/personal/evt@aerion-caldav.ics", got.path)
	}
}

func TestAbsoluteHref(t *testing.T) {
	tests := []struct {
		name    string
		srcURL  string
		href    string
		want    string
		wantErr bool
	}{
		{
			name:   "href already absolute",
			srcURL: "https://cloud.example.com/remote.php/dav/calendars/user/",
			href:   "https://cloud.example.com/remote.php/dav/calendars/user/personal/abc.ics",
			want:   "https://cloud.example.com/remote.php/dav/calendars/user/personal/abc.ics",
		},
		{
			name:   "relative href + absolute srcURL (Nextcloud)",
			srcURL: "https://cloud.example.com/remote.php/dav/calendars/user/",
			href:   "/remote.php/dav/calendars/user/personal/abc.ics",
			want:   "https://cloud.example.com/remote.php/dav/calendars/user/personal/abc.ics",
		},
		{
			name:    "relative href + relative srcURL (broken — should error)",
			srcURL:  "/calendars/user/",
			href:    "/calendars/user/personal/abc.ics",
			wantErr: true,
		},
		{
			name:   "srcURL with port",
			srcURL: "https://cloud.example.com:8443/remote.php/dav/",
			href:   "/remote.php/dav/calendars/user/personal/abc.ics",
			want:   "https://cloud.example.com:8443/remote.php/dav/calendars/user/personal/abc.ics",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := absoluteHref(tt.srcURL, tt.href)
			if tt.wantErr {
				if err == nil {
					t.Errorf("absoluteHref(%q, %q) = %q, want error", tt.srcURL, tt.href, got)
				}
				return
			}
			if err != nil {
				t.Errorf("absoluteHref(%q, %q) error = %v", tt.srcURL, tt.href, err)
				return
			}
			if got != tt.want {
				t.Errorf("absoluteHref(%q, %q) = %q, want %q", tt.srcURL, tt.href, got, tt.want)
			}
		})
	}
}

func TestJoinHref(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		suffix string
		want   string
	}{
		{
			name:   "with scheme + host + trailing slash",
			base:   "https://caldav.example.com/calendars/user/personal/",
			suffix: "event-1.ics",
			want:   "https://caldav.example.com/calendars/user/personal/event-1.ics",
		},
		{
			name:   "with scheme + host + no trailing slash",
			base:   "https://caldav.example.com/calendars/user/personal",
			suffix: "event-1.ics",
			want:   "https://caldav.example.com/calendars/user/personal/event-1.ics",
		},
		{
			name:   "host only",
			base:   "https://caldav.example.com",
			suffix: "event-1.ics",
			want:   "https://caldav.example.com/event-1.ics",
		},
		{
			name:   "no scheme",
			base:   "/calendars/user/personal/",
			suffix: "event-1.ics",
			want:   "/calendars/user/personal/event-1.ics",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinHref(tt.base, tt.suffix)
			if got != tt.want {
				t.Errorf("joinHref(%q, %q) = %q, want %q", tt.base, tt.suffix, got, tt.want)
			}
		})
	}
}
