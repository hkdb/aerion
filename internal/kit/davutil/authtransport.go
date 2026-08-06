package davutil

import (
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/icholy/digest"
)

// basicDigestTransport authenticates password-based WebDAV requests. It sends
// Basic preemptively (the historical behavior — zero extra round trips for the
// common Basic servers), and when a server rejects with a 401 carrying a
// Digest challenge (RFC 7616; sabre/dav servers like Baïkal default to it,
// #313/#315) it answers the challenge and retries once. Challenges are cached
// per host+user so subsequent requests authenticate on the first try; a stale
// nonce (401 with a fresh challenge) refreshes the cache through the same
// retry path.
type basicDigestTransport struct {
	username string
	password string
	base     http.RoundTripper
}

// challengeState is a cached digest challenge plus the nonce-count of the
// last request made against it.
type challengeState struct {
	chal  *digest.Challenge
	count int
}

// digestChallenges caches challenges per scheme://host + username. Package
// level because davutil clients are constructed per operation — cache state on
// the transport itself would be lost between calls.
var (
	digestMu         sync.Mutex
	digestChallenges = map[string]*challengeState{}
)

func challengeKey(u *url.URL, username string) string {
	return u.Scheme + "://" + u.Host + "\x00" + username
}

// digestAuthorization builds an Authorization header from the cached challenge
// for key, incrementing its nonce count. Returns "" when no challenge is
// cached or the credentials cannot be computed.
func digestAuthorization(key, method, uri, username, password string) string {
	digestMu.Lock()
	defer digestMu.Unlock()
	st, ok := digestChallenges[key]
	if !ok {
		return ""
	}
	st.count++
	cred, err := digest.Digest(st.chal, digest.Options{
		Method:   method,
		URI:      uri,
		Count:    st.count,
		Username: username,
		Password: password,
	})
	if err != nil {
		return ""
	}
	return cred.String()
}

func storeChallenge(key string, chal *digest.Challenge) {
	digestMu.Lock()
	defer digestMu.Unlock()
	digestChallenges[key] = &challengeState{chal: chal}
}

// RoundTrip implements http.RoundTripper.
func (t *basicDigestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = defaultBaseTransport()
	}
	// Respect an Authorization header the caller set themselves (mirrors
	// bearerTransport).
	if req.Header.Get("Authorization") != "" {
		return base.RoundTrip(req)
	}

	key := challengeKey(req.URL, t.username)
	first := req.Clone(req.Context())
	auth := digestAuthorization(key, req.Method, req.URL.RequestURI(), t.username, t.password)
	if auth == "" {
		first.SetBasicAuth(t.username, t.password)
	}
	if auth != "" {
		first.Header.Set("Authorization", auth)
	}

	resp, err := base.RoundTrip(first)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	chal, err := digest.FindChallenge(resp.Header)
	if err != nil || !digest.CanDigest(chal) {
		// 401 without a usable Digest challenge (Basic-only server, bad
		// credentials, unsupported algorithm) — pass through unchanged.
		return resp, nil
	}
	// A body that was consumed by the first attempt must be replayable.
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}

	// Drain so the underlying connection can be reused, then retry once with
	// the fresh challenge. Covers both first contact and stale nonces.
	io.Copy(io.Discard, resp.Body) //nolint:errcheck // best-effort drain
	resp.Body.Close()
	storeChallenge(key, chal)

	retry := req.Clone(req.Context())
	auth = digestAuthorization(key, req.Method, req.URL.RequestURI(), t.username, t.password)
	if auth == "" {
		// Credential computation failed against a challenge CanDigest accepted
		// — should not happen; fall back to the original 401 semantics.
		retry.SetBasicAuth(t.username, t.password)
	}
	if auth != "" {
		retry.Header.Set("Authorization", auth)
	}
	if req.GetBody != nil {
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return nil, bodyErr
		}
		retry.Body = body
	}
	return base.RoundTrip(retry)
}

// NewBasicDigestHTTPClient returns a WebDAV-ready *http.Client that
// authenticates with Basic, upgrades to HTTP Digest when the server demands
// it, and applies the XML fixups. Drop-in replacement for
// webdav.HTTPClientWithBasicAuth wherever a go-webdav HTTPClient is expected.
func NewBasicDigestHTTPClient(username, password string, timeout time.Duration) *http.Client {
	return NewWebDAVClient(&basicDigestTransport{
		username: username,
		password: password,
		base:     defaultBaseTransport(),
	}, timeout)
}
