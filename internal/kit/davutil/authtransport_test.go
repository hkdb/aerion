package davutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/icholy/digest"
)

// digestHandler is a test server requiring HTTP Digest auth. It records the
// auth scheme of every request and validates digest responses against the
// current challenge, re-challenging with stale=true on nonce mismatch.
type digestHandler struct {
	mu        sync.Mutex
	chal      *digest.Challenge
	username  string
	password  string
	schemes   []string // per-request: "none", "basic", "digest"
	lastBody  string
	staleSent bool
}

func newDigestHandler(algorithm, username, password string) *digestHandler {
	return &digestHandler{
		chal: &digest.Challenge{
			Realm:     "TestDAV",
			Nonce:     "nonce-1",
			Opaque:    "opaque-1",
			QOP:       []string{"auth"},
			Algorithm: algorithm,
		},
		username: username,
		password: password,
	}
}

// rotateNonce simulates server-side nonce expiry.
func (h *digestHandler) rotateNonce(nonce string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.chal.Nonce = nonce
}

func (h *digestHandler) scheme(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	switch {
	case auth == "":
		return "none"
	case strings.HasPrefix(auth, "Basic "):
		return "basic"
	case digest.IsDigest(auth):
		return "digest"
	default:
		return "other"
	}
}

func (h *digestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.schemes = append(h.schemes, h.scheme(r))
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			h.lastBody = string(body)
		}
	}

	if h.validDigest(r) {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Re-challenge. Mark stale when the client presented a digest with an
	// outdated nonce (it should retry without re-prompting for credentials).
	chal := *h.chal
	if cred, err := digest.ParseCredentials(r.Header.Get("Authorization")); err == nil && cred.Nonce != h.chal.Nonce {
		chal.Stale = true
		h.staleSent = true
	}
	w.Header().Set("WWW-Authenticate", chal.String())
	w.WriteHeader(http.StatusUnauthorized)
}

func (h *digestHandler) validDigest(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !digest.IsDigest(auth) {
		return false
	}
	cred, err := digest.ParseCredentials(auth)
	if err != nil {
		return false
	}
	if cred.Nonce != h.chal.Nonce {
		return false
	}
	expected, err := digest.Digest(h.chal, digest.Options{
		Method:   r.Method,
		URI:      cred.URI,
		Username: h.username,
		Password: h.password,
		Cnonce:   cred.Cnonce,
		Count:    cred.Nc,
	})
	if err != nil {
		return false
	}
	return expected.Response == cred.Response
}

func TestBasicDigestTransport_DigestServer(t *testing.T) {
	h := newDigestHandler("", "alice", "secret") // no algorithm = MD5
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)

	// First request: preemptive Basic → 401 challenge → digest retry → 200.
	resp, err := client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", resp.StatusCode)
	}
	if want := []string{"basic", "digest"}; strings.Join(h.schemes, ",") != strings.Join(want, ",") {
		t.Fatalf("server saw schemes %v, want %v", h.schemes, want)
	}

	// Second request: cached challenge → digest first-try, single request.
	resp, err = client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", resp.StatusCode)
	}
	if len(h.schemes) != 3 || h.schemes[2] != "digest" {
		t.Fatalf("server saw schemes %v, want a single digest third request", h.schemes)
	}
}

func TestBasicDigestTransport_SHA256(t *testing.T) {
	h := newDigestHandler("SHA-256", "alice", "secret")
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)
	resp, err := client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBasicDigestTransport_BasicServerUnchanged(t *testing.T) {
	var schemes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		schemes = append(schemes, r.Header.Get("Authorization"))
		if !ok || user != "alice" || pass != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="TestDAV"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)
	resp, err := client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(schemes) != 1 || !strings.HasPrefix(schemes[0], "Basic ") {
		t.Fatalf("server saw %v, want exactly one Basic request", schemes)
	}
}

func TestBasicDigestTransport_WrongPassword(t *testing.T) {
	h := newDigestHandler("", "alice", "secret")
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "wrong", 5*time.Second)
	resp, err := client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 passthrough", resp.StatusCode)
	}
	// Exactly one digest attempt after the basic one — no retry loop.
	if want := []string{"basic", "digest"}; strings.Join(h.schemes, ",") != strings.Join(want, ",") {
		t.Fatalf("server saw schemes %v, want %v", h.schemes, want)
	}
}

func TestBasicDigestTransport_StaleNonce(t *testing.T) {
	h := newDigestHandler("", "alice", "secret")
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)
	resp, err := client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp.Body.Close()

	// Server expires the nonce; the cached challenge is now stale.
	h.rotateNonce("nonce-2")

	resp, err = client.Get(srv.URL + "/dav/")
	if err != nil {
		t.Fatalf("post-rotation request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-rotation status = %d, want 200 after stale re-challenge", resp.StatusCode)
	}
	if !h.staleSent {
		t.Fatal("server never sent a stale=true challenge — test setup broken")
	}
	// basic, digest (initial), digest (stale nonce-1), digest (fresh nonce-2)
	if want := []string{"basic", "digest", "digest", "digest"}; strings.Join(h.schemes, ",") != strings.Join(want, ",") {
		t.Fatalf("server saw schemes %v, want %v", h.schemes, want)
	}
}

func TestBasicDigestTransport_BodyReplay(t *testing.T) {
	h := newDigestHandler("", "alice", "secret")
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := NewBasicDigestHTTPClient("alice", "secret", 5*time.Second)
	const propfind = `<?xml version="1.0"?><propfind/>`
	req, err := http.NewRequest("PROPFIND", srv.URL+"/dav/", strings.NewReader(propfind))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if h.lastBody != propfind {
		t.Fatalf("digest retry body = %q, want %q", h.lastBody, propfind)
	}
}
