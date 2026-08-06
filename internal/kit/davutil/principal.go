package davutil

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpDoer is the minimal client surface the principal probe needs — both
// *http.Client and go-webdav's HTTPClient satisfy it.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

const principalPropfindBody = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:"><D:prop><D:current-user-principal/></D:prop></D:propfind>`

// FindCurrentUserPrincipalRaw issues a Depth:0 PROPFIND for
// DAV:current-user-principal against rawURL EXACTLY as given. go-webdav's own
// principal probe resolves its request path with path.Join, which strips a
// trailing slash — on servers that serve the collection only at "/dav/"
// (SabreDAV/Davis, #363) that turns into a 404. This probe never rewrites the
// path; when rawURL lacks a trailing slash and the exact attempt fails, it
// retries once with the slash appended. Returns the principal href.
func FindCurrentUserPrincipalRaw(ctx context.Context, client httpDoer, rawURL string) (string, error) {
	principal, err := principalPropfind(ctx, client, rawURL)
	if err == nil {
		return principal, nil
	}
	if strings.HasSuffix(rawURL, "/") {
		return "", err
	}
	principal, retryErr := principalPropfind(ctx, client, rawURL+"/")
	if retryErr != nil {
		// The exact-URL error is the more representative one.
		return "", err
	}
	return principal, nil
}

func principalPropfind(ctx context.Context, client httpDoer, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", rawURL, strings.NewReader(principalPropfindBody))
	if err != nil {
		return "", fmt.Errorf("build principal PROPFIND: %w", err)
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("principal PROPFIND: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck // best-effort drain
		return "", fmt.Errorf("principal PROPFIND at %s: %s", rawURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("read principal PROPFIND response: %w", err)
	}

	var ms struct {
		XMLName  xml.Name `xml:"DAV: multistatus"`
		Response []struct {
			Propstat []struct {
				Prop struct {
					CurrentUserPrincipal struct {
						Href string `xml:"href"`
					} `xml:"current-user-principal"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(body, &ms); err != nil {
		return "", fmt.Errorf("parse principal PROPFIND response: %w", err)
	}
	for _, r := range ms.Response {
		for _, ps := range r.Propstat {
			href := strings.TrimSpace(ps.Prop.CurrentUserPrincipal.Href)
			if href != "" {
				return href, nil
			}
		}
	}
	return "", fmt.Errorf("no current-user-principal in PROPFIND response from %s", rawURL)
}
