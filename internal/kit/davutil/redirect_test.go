package davutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// redirectDAVServer 302-redirects /.well-known/dav to /dav/ and answers
// PROPFIND /dav/ with 207. Records every (method, path, depth) it sees.
type redirectDAVServer struct {
	requests []string
	bodies   []string
}

func (h *redirectDAVServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.requests = append(h.requests, r.Method+" "+r.URL.Path+" depth="+r.Header.Get("Depth"))
	h.bodies = append(h.bodies, string(body))

	switch r.URL.Path {
	case "/.well-known/dav":
		http.Redirect(w, r, "/dav/", http.StatusFound)
	case "/see-other":
		http.Redirect(w, r, "/dav/", http.StatusSeeOther)
	case "/loop":
		http.Redirect(w, r, "/loop", http.StatusFound)
	case "/dav/":
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"></d:multistatus>`)) //nolint:errcheck
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func davRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(`<?xml version="1.0"?><propfind/>`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Depth", "0")
	return req
}

func TestRedirectTransport_ReplaysPropfind(t *testing.T) {
	h := &redirectDAVServer{}
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewWebDAVClient(nil, 5*time.Second)
	resp, err := client.Do(davRequest(t, "PROPFIND", srv.URL+"/.well-known/dav"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}
	want := []string{
		"PROPFIND /.well-known/dav depth=0",
		"PROPFIND /dav/ depth=0",
	}
	if strings.Join(h.requests, ",") != strings.Join(want, ",") {
		t.Fatalf("server saw %v, want %v", h.requests, want)
	}
	if h.bodies[1] == "" {
		t.Fatal("redirected PROPFIND arrived without its body")
	}
}

func TestRedirectTransport_SeeOtherUntouched(t *testing.T) {
	h := &redirectDAVServer{}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 303 must keep stdlib semantics (downgrade to GET) — our transport passes
	// it through, the stdlib client follows with GET, /dav/ answers 405.
	client := NewWebDAVClient(nil, 5*time.Second)
	resp, err := client.Do(davRequest(t, "PROPFIND", srv.URL+"/see-other"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (GET downgrade preserved for 303)", resp.StatusCode)
	}
	if !strings.HasPrefix(h.requests[len(h.requests)-1], "GET /dav/") {
		t.Fatalf("expected stdlib GET follow-up, server saw %v", h.requests)
	}
}

func TestRedirectTransport_CrossHostUntouched(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/dav/", http.StatusFound)
	}))
	defer srv.Close()

	// Cross-host: our transport must not replay; stdlib follows (as GET).
	client := NewWebDAVClient(nil, 5*time.Second)
	resp, err := client.Do(davRequest(t, "PROPFIND", srv.URL+"/dav"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 from cross-host follow", resp.StatusCode)
	}
}

func TestRedirectTransport_HopLimitTerminates(t *testing.T) {
	h := &redirectDAVServer{}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// /loop 302s to itself forever. Our transport gives up after its hop
	// budget and hands the redirect back; the stdlib client then applies its
	// own (GET-downgrading, also bounded) redirect handling. The request must
	// terminate with an error or response, not hang.
	client := NewWebDAVClient(nil, 5*time.Second)
	resp, err := client.Do(davRequest(t, "PROPFIND", srv.URL+"/loop"))
	if err == nil {
		resp.Body.Close()
	}
}

func TestRedirectTransport_WithDigestAuth(t *testing.T) {
	// Compose redirect + digest: /.well-known/dav 302s to /dav/, which then
	// digest-challenges. The replayed hop must re-enter the auth transport.
	dh := newDigestHandler("", "alice", "secret")
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/dav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dav/", http.StatusFound)
	})
	mux.Handle("/dav/", dh)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)
	resp, err := client.Do(davRequest(t, "PROPFIND", srv.URL+"/.well-known/dav"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after redirect + digest handshake", resp.StatusCode)
	}
}
