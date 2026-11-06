package unsubscribe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// oneClickTimeout bounds the whole one-click request, redirects included.
const oneClickTimeout = 20 * time.Second

// maxRedirects bounds redirect chains on the one-click POST.
const maxRedirects = 3

// errBlockedAddress is returned when the unsubscribe URL resolves to a
// loopback/private/link-local address. The URL comes from an untrusted
// e-mail header, so we refuse to let it reach the user's local network.
var errBlockedAddress = errors.New("unsubscribe URL points to a non-public address")

// blockNonPublic is a net.Dialer Control hook that rejects connections to
// non-public IPs. It runs after DNS resolution, so it also covers DNS
// rebinding tricks.
func blockNonPublic(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errBlockedAddress
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return errBlockedAddress
	}
	return nil
}

// newOneClickClient returns an HTTP client suited to RFC 8058 requests:
// no cookie jar, bounded redirects (https only), public addresses only.
func newOneClickClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: blockNonPublic,
	}
	transport := &http.Transport{
		// No proxy: the dial guard must see the real destination address.
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        1,
		IdleConnTimeout:     5 * time.Second,
	}
	return &http.Client{
		Timeout:   oneClickTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("refusing non-https redirect")
			}
			return nil
		},
	}
}

// PostOneClick performs an RFC 8058 one-click unsubscribe: a POST of
// "List-Unsubscribe=One-Click" to the https URI from List-Unsubscribe.
// Per the RFC no cookies or credentials are sent.
func PostOneClick(ctx context.Context, rawURL, userAgent string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid unsubscribe URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("one-click unsubscribe requires an https URL")
	}

	ctx, cancel := context.WithTimeout(ctx, oneClickTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(),
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := newOneClickClient().Do(req)
	if err != nil {
		return fmt.Errorf("one-click unsubscribe request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("one-click unsubscribe returned HTTP %d", resp.StatusCode)
	}
	return nil
}
