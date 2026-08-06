package davutil

import (
	"io"
	"net/http"
)

// redirectTransport preserves the request method across same-host redirects
// for the read-only DAV discovery/query methods. Go's net/http downgrades any
// method to GET on 301/302/303 responses, so a server redirecting
// `.well-known/caldav` → `/dav/` never sees the follow-up PROPFIND and
// discovery dead-ends on a non-207 response (#363). Scope is deliberately
// narrow: only PROPFIND and REPORT are replayed, only to the same scheme+host
// (no credential leakage), with a bounded hop count. Everything else — other
// methods, 303 (spec-mandated GET), cross-host Locations — passes through
// untouched so stdlib redirect semantics stay byte-identical for existing
// flows.
type redirectTransport struct {
	base http.RoundTripper
}

const redirectMaxHops = 5

func methodPreservesRedirect(method string) bool {
	switch method {
	case "PROPFIND", "REPORT":
		return true
	}
	return false
}

func statusPreservesRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// RoundTrip implements http.RoundTripper.
func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = defaultBaseTransport()
	}
	if !methodPreservesRedirect(req.Method) {
		return base.RoundTrip(req)
	}

	current := req
	for hop := 0; hop < redirectMaxHops; hop++ {
		resp, err := base.RoundTrip(current)
		if err != nil {
			return resp, err
		}
		if !statusPreservesRedirect(resp.StatusCode) {
			return resp, nil
		}
		location, err := resp.Location()
		if err != nil {
			return resp, nil
		}
		if location.Scheme != current.URL.Scheme || location.Host != current.URL.Host {
			// Cross-host (or scheme-changing) redirect — hand it back so the
			// stdlib client applies its usual semantics.
			return resp, nil
		}
		// A consumed body must be replayable for the next hop.
		if current.Body != nil && current.GetBody == nil {
			return resp, nil
		}

		io.Copy(io.Discard, resp.Body) //nolint:errcheck // best-effort drain for connection reuse
		resp.Body.Close()

		next := current.Clone(current.Context())
		next.URL = location
		next.Host = ""
		if current.GetBody != nil {
			body, bodyErr := current.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			next.Body = body
			next.GetBody = current.GetBody
		}
		current = next
	}

	// Hop limit exhausted — issue the final request and return whatever comes
	// back (a persistent redirect loop surfaces as the redirect response).
	return base.RoundTrip(current)
}
