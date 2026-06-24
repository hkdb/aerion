// Package davutil holds shared WebDAV / HTTP utilities that both host and
// extension code can import.
//
// This is the FIRST member of the backend `internal/kit/*` namespace — the
// Go-side analog of the frontend `lib/components/kit/` UI kit. Modules under
// `internal/kit/*` are generic, extension-facing building blocks (no
// extension-specific naming, no per-extension behavior). Extensions are
// allowed to import them; the rule is the same as the frontend kit's.
//
// See docs/EXTENSIONS.md and docs/EXT_RULES.md.
package davutil

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// defaultBase is the base RoundTripper every davutil client falls back to when
// no explicit base is supplied. It starts as http.DefaultTransport; the HOST may
// replace it once at startup (SetDefaultBaseTransport) with a cert-aware
// transport — e.g. one wired to Aerion's trust-on-first-use certificate store —
// so all WebDAV clients (host + extension, Basic + bearer) verify TLS the same
// way IMAP/SMTP do. davutil stays generic: it never imports the certificate
// package; the host assembles the transport and installs it here.
var defaultBase http.RoundTripper = http.DefaultTransport

// SetDefaultBaseTransport installs the process-wide base transport for all
// davutil-built WebDAV clients. Call once at startup, before any client is
// built. Passing nil resets to http.DefaultTransport.
func SetDefaultBaseTransport(rt http.RoundTripper) {
	if rt == nil {
		defaultBase = http.DefaultTransport
		return
	}
	defaultBase = rt
}

func defaultBaseTransport() http.RoundTripper {
	if defaultBase == nil {
		return http.DefaultTransport
	}
	return defaultBase
}

// XMLFixTransport normalizes WebDAV XML responses to work around server
// quirks the underlying go-webdav library trips on:
//
//  1. DAV:getlastmodified — converts numeric timezone offsets (e.g., +0000)
//     to GMT format. Some servers (Purelymail) return RFC 1123Z dates which
//     http.ParseTime() cannot parse.
//  2. DAV:getetag — adds quotes around unquoted ETag values. Some servers
//     (mailbox.org) return unquoted ETags which go-webdav's strconv.Unquote()
//     rejects.
//
// Wrap any http.RoundTripper (typically http.DefaultTransport). Use the
// NewHTTPClient helper if you don't need a custom base transport.
type XMLFixTransport struct {
	Base http.RoundTripper
}

// NewXMLFixTransport wraps base in an XMLFixTransport. If base is nil, the
// configurable default base (SetDefaultBaseTransport, else http.DefaultTransport)
// is used.
func NewXMLFixTransport(base http.RoundTripper) *XMLFixTransport {
	if base == nil {
		base = defaultBaseTransport()
	}
	return &XMLFixTransport{Base: base}
}

// NewHTTPClient returns an *http.Client wrapping the configurable default base
// transport in XMLFixTransport, with the given request timeout. Used by both
// internal/carddav and the calendar extension for any WebDAV operation
// whose responses may carry ETag / lastmodified headers — i.e., sync and
// per-resource PUT/DELETE.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return NewWebDAVClient(defaultBaseTransport(), timeout)
}

// NewWebDAVClient wraps base in XMLFixTransport and returns an *http.Client
// (which satisfies go-webdav's HTTPClient interface). base is the inner
// transport — http.DefaultTransport for unauthenticated/Basic use, or an
// auth-injecting transport (e.g. bearerTransport, or the auth broker's
// refreshing transport) when the caller supplies one. If base is nil,
// http.DefaultTransport is used.
func NewWebDAVClient(base http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewXMLFixTransport(base),
	}
}

// bearerTransport injects a static `Authorization: Bearer <token>` header on
// each request, leaving an existing Authorization header untouched. Generic
// HTTP bearer auth — no extension- or provider-specific knowledge.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = defaultBaseTransport()
	}
	if existing := req.Header.Get("Authorization"); strings.TrimSpace(existing) != "" {
		return base.RoundTrip(req)
	}
	// Clone before mutating — RoundTrippers must not modify the input request.
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	return base.RoundTrip(cloned)
}

// NewBearerHTTPClient returns a WebDAV-ready *http.Client that injects a static
// bearer token and applies the XML fixups. Pass the result anywhere a
// go-webdav HTTPClient is expected. For tokens that refresh, wrap the
// refreshing transport with NewWebDAVClient instead.
func NewBearerHTTPClient(token string, timeout time.Duration) *http.Client {
	return NewWebDAVClient(&bearerTransport{token: token, base: defaultBaseTransport()}, timeout)
}

var getlastmodifiedRe = regexp.MustCompile(
	`(<[^>]*getlastmodified[^>]*>)\s*([^<]+?)\s*(</[^>]*getlastmodified[^>]*>)`,
)

var getetagRe = regexp.MustCompile(
	`(<[^>]*getetag[^>]*>)\s*([^<]+?)\s*(</[^>]*getetag[^>]*>)`,
)

// RoundTrip implements http.RoundTripper. Reads the response body for XML
// content types, applies both fixups, and returns a new body the caller
// can consume normally.
func (t *XMLFixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "xml") && !strings.Contains(ct, "text/xml") {
		return resp, nil
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("davutil.XMLFixTransport: read body: %w", err)
	}

	// Fix 1: normalize getlastmodified date formats.
	fixed := getlastmodifiedRe.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := getlastmodifiedRe.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		dateStr := strings.TrimSpace(string(sub[2]))
		return fixDateValue(sub[1], dateStr, sub[3])
	})

	// Fix 2: quote unquoted getetag values.
	fixed = getetagRe.ReplaceAllFunc(fixed, func(match []byte) []byte {
		sub := getetagRe.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		etagStr := strings.TrimSpace(string(sub[2]))
		return fixETagValue(sub[1], etagStr, sub[3])
	})

	resp.Body = io.NopCloser(bytes.NewReader(fixed))
	resp.ContentLength = int64(len(fixed))
	return resp, nil
}

// fixETagValue normalizes an ETag for go-webdav's strconv.Unquote().
// Handles: literal quotes, XML-entity-encoded quotes (&quot;), weak ETags
// (W/), and unquoted values.
func fixETagValue(prefix []byte, etagStr string, suffix []byte) []byte {
	var buf bytes.Buffer
	buf.Write(prefix)

	cleaned := etagStr

	// Strip weak ETag prefix if present.
	if strings.HasPrefix(cleaned, "W/") || strings.HasPrefix(cleaned, "w/") {
		cleaned = cleaned[2:]
	}

	// Already quoted with literal quotes — leave as-is.
	if strings.HasPrefix(cleaned, `"`) && strings.HasSuffix(cleaned, `"`) && len(cleaned) >= 2 {
		buf.WriteString(cleaned)
		buf.Write(suffix)
		return buf.Bytes()
	}

	// Quoted with XML-entity-encoded quotes (&quot;...&quot;) — leave as-is.
	// The XML parser resolves them to literal quotes before go-webdav sees them.
	if strings.HasPrefix(cleaned, "&quot;") && strings.HasSuffix(cleaned, "&quot;") {
		buf.WriteString(cleaned)
		buf.Write(suffix)
		return buf.Bytes()
	}

	// Truly unquoted — wrap in literal quotes.
	cleaned = strings.Trim(cleaned, `"`)
	buf.WriteByte('"')
	buf.WriteString(cleaned)
	buf.WriteByte('"')
	buf.Write(suffix)
	return buf.Bytes()
}

// fixDateValue converts an RFC 1123Z date to RFC 1123 (GMT) format. If the
// value is not RFC 1123Z, it is returned unchanged.
func fixDateValue(prefix []byte, dateStr string, suffix []byte) []byte {
	t, err := time.Parse(time.RFC1123Z, dateStr)
	if err != nil {
		var buf bytes.Buffer
		buf.Write(prefix)
		buf.WriteString(dateStr)
		buf.Write(suffix)
		return buf.Bytes()
	}
	var buf bytes.Buffer
	buf.Write(prefix)
	buf.WriteString(t.UTC().Format(http.TimeFormat))
	buf.Write(suffix)
	return buf.Bytes()
}
