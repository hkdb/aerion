package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDCDiscovery is the subset of an OpenID Connect / OAuth 2.0 Authorization Server
// Metadata document that Aerion needs to build a custom ("bring your own app") flow.
// Field names use the document's snake_case keys (OIDC Discovery / RFC 8414).
type OIDCDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// discoveryPaths are tried in order: OIDC Discovery first, then RFC 8414's OAuth
// Authorization Server Metadata. Most modern servers (Stalwart, corporate IdPs)
// publish the former.
var discoveryPaths = []string{
	"/.well-known/openid-configuration",
	"/.well-known/oauth-authorization-server",
}

// DiscoverOIDC fetches OAuth/OIDC server metadata for the given issuer URL and returns
// the endpoints Aerion needs. The issuer must be https (loopback hosts are allowed for
// self-hosted testing). Returns an error if neither well-known path yields a document
// with both an authorization and a token endpoint.
func DiscoverOIDC(ctx context.Context, issuer string) (OIDCDiscovery, error) {
	base := strings.TrimRight(strings.TrimSpace(issuer), "/")
	if base == "" {
		return OIDCDiscovery{}, fmt.Errorf("issuer URL is required")
	}

	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return OIDCDiscovery{}, fmt.Errorf("invalid issuer URL")
	}
	host := u.Hostname()
	isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback) {
		return OIDCDiscovery{}, fmt.Errorf("issuer URL must use https")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for _, p := range discoveryPaths {
		doc, derr := fetchDiscovery(ctx, client, base+p)
		if derr != nil {
			lastErr = derr
			continue
		}
		if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
			lastErr = fmt.Errorf("discovery document is missing an authorization or token endpoint")
			continue
		}
		return doc, nil
	}
	return OIDCDiscovery{}, fmt.Errorf("OIDC discovery failed: %w", lastErr)
}

func fetchDiscovery(ctx context.Context, client *http.Client, endpoint string) (OIDCDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OIDCDiscovery{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return OIDCDiscovery{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return OIDCDiscovery{}, fmt.Errorf("discovery request to %s returned %d", endpoint, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return OIDCDiscovery{}, err
	}

	var doc OIDCDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return OIDCDiscovery{}, fmt.Errorf("failed to parse discovery document: %w", err)
	}
	return doc, nil
}
