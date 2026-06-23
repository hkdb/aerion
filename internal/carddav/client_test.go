package carddav

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// XML-fix unit tests live alongside the implementation at
// internal/kit/davutil/xmlfix_test.go. This file covers the carddav-specific
// PUT/DELETE/ETag-quoting behavior.

// ============================================================================
// PUT / DELETE wrappers (Phase 2b.2.b.1)
// ============================================================================

// fakeCardDAVServer is a minimal httptest server that records the last request
// received and responds with the configured status + headers. Used by the
// Put/Delete tests to assert request shape and exercise error paths.
type fakeCardDAVServer struct {
	srv          *httptest.Server
	lastMethod   string
	lastPath     string
	lastIfMatch  string
	lastBody     []byte
	respStatus   int
	respETag     string
	respBody     string
}

func newFakeServer(status int, etag, body string) *fakeCardDAVServer {
	f := &fakeCardDAVServer{respStatus: status, respETag: etag, respBody: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastMethod = r.Method
		f.lastPath = r.URL.Path
		f.lastIfMatch = r.Header.Get("If-Match")
		f.lastBody, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if f.respETag != "" {
			w.Header().Set("ETag", f.respETag)
		}
		w.WriteHeader(f.respStatus)
		_, _ = w.Write([]byte(f.respBody))
	}))
	return f
}

func (f *fakeCardDAVServer) close() { f.srv.Close() }

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := NewClient(baseURL, "user", "pass")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestPutContact_HappyPath(t *testing.T) {
	fake := newFakeServer(201, `"etag-after"`, "")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	etag, err := c.PutContact("/addressbook/", "/addressbook/contact.vcf", "etag-before", false, []byte("BEGIN:VCARD\r\nEND:VCARD\r\n"))
	if err != nil {
		t.Fatalf("PutContact: %v", err)
	}
	if etag != "etag-after" {
		t.Errorf("returned etag = %q, want %q", etag, "etag-after")
	}
	if fake.lastMethod != http.MethodPut {
		t.Errorf("method = %q", fake.lastMethod)
	}
	if fake.lastPath != "/addressbook/contact.vcf" {
		t.Errorf("path = %q", fake.lastPath)
	}
	if fake.lastIfMatch != `"etag-before"` {
		t.Errorf("If-Match = %q, want %q (quoted exact-match)", fake.lastIfMatch, `"etag-before"`)
	}
	if !bytes.Contains(fake.lastBody, []byte("BEGIN:VCARD")) {
		t.Errorf("body wasn't sent: %q", fake.lastBody)
	}
}

func TestPutContact_PreconditionFailed(t *testing.T) {
	fake := newFakeServer(http.StatusPreconditionFailed, `"server-etag"`, "")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	_, err := c.PutContact("/addressbook/", "/addressbook/contact.vcf", "stale-etag", false, []byte("dummy"))
	var pre *ErrPreconditionFailed
	if !errors.As(err, &pre) {
		t.Fatalf("expected *ErrPreconditionFailed, got %T: %v", err, err)
	}
	if pre.Href != "/addressbook/contact.vcf" {
		t.Errorf("conflict href = %q", pre.Href)
	}
	if pre.ServerETag != `"server-etag"` {
		t.Errorf("conflict server etag = %q", pre.ServerETag)
	}
}

func TestPutContact_OtherStatusError(t *testing.T) {
	fake := newFakeServer(http.StatusForbidden, "", "denied")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	_, err := c.PutContact("/addressbook/", "/addressbook/contact.vcf", "etag", false, []byte("dummy"))
	if err == nil {
		t.Fatal("expected error on 403")
	}
	var pre *ErrPreconditionFailed
	if errors.As(err, &pre) {
		t.Fatalf("403 should not surface as ErrPreconditionFailed")
	}
}

func TestDeleteContact_HappyPath(t *testing.T) {
	fake := newFakeServer(http.StatusNoContent, "", "")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	if err := c.DeleteContact("/addressbook/", "/addressbook/contact.vcf", "etag-before"); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if fake.lastMethod != http.MethodDelete {
		t.Errorf("method = %q", fake.lastMethod)
	}
	if fake.lastIfMatch != `"etag-before"` {
		t.Errorf("If-Match = %q", fake.lastIfMatch)
	}
}

func TestDeleteContact_PreconditionFailed(t *testing.T) {
	fake := newFakeServer(http.StatusPreconditionFailed, `"server-etag"`, "")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	err := c.DeleteContact("/addressbook/", "/addressbook/contact.vcf", "stale-etag")
	var pre *ErrPreconditionFailed
	if !errors.As(err, &pre) {
		t.Fatalf("expected *ErrPreconditionFailed, got %T: %v", err, err)
	}
}

func TestDeleteContact_NotFoundIsIdempotent(t *testing.T) {
	fake := newFakeServer(http.StatusNotFound, "", "")
	defer fake.close()

	c := newTestClient(t, fake.srv.URL)
	if err := c.DeleteContact("/addressbook/", "/addressbook/contact.vcf", "etag"); err != nil {
		t.Errorf("404 should be idempotent success, got: %v", err)
	}
}

func TestQuotedETag(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"abc", `"abc"`},
		{`"abc"`, `"abc"`},
		{`  "abc"  `, `"abc"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := quotedETag(c.in); got != c.out {
			t.Errorf("quotedETag(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
