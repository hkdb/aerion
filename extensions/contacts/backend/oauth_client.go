package backend

import (
	"fmt"
	"net/http"

	"github.com/hkdb/aerion/internal/carddav"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// httpClientForSource returns an authenticated *http.Client for a Google /
// Microsoft contact source, dispatching on whether the source is linked to
// an email account or is a standalone contacts-only OAuth source.
//
// Mirrors `internal/carddav/sync.go::Syncer.getOAuthToken` — the sync side
// already handles both modes; the write side was account-linked-only before
// this, which produced "<provider> source has no linked account" for users
// who set up the source via the contacts-only OAuth flow rather than via
// link-to-existing-account.
//
// Account-linked path: delegates to the Auth Broker (core.Auth().HTTPClient),
// which captures per-(account, client_config) refresh state and routes scopes
// per the manifest's first_party_uses_core_for_scopes declaration.
//
// Standalone path: fetches a token via the host's getValidContactSourceOAuth
// token (proactively refreshed on expiry by the host), then wraps it in a
// minimal bearer transport. No 401-driven refresh here; if the token gets
// revoked mid-request the call surfaces the 401 to the caller — same
// behaviour as the sync layer.
func (a *API) httpClientForSource(source *carddav.Source, scope coreapi.AuthScope) (*http.Client, error) {
	if source == nil {
		return nil, fmt.Errorf("contacts: httpClientForSource: nil source")
	}

	if source.AccountID != nil && *source.AccountID != "" {
		if a.core == nil {
			return nil, fmt.Errorf("contacts: httpClientForSource: core not wired")
		}
		return a.core.Auth().HTTPClient(*source.AccountID, []coreapi.AuthScope{scope})
	}

	if a.getStandaloneSourceToken == nil {
		return nil, fmt.Errorf("contacts: httpClientForSource: standalone-source token getter not wired (source %s)", source.ID)
	}
	token, err := a.getStandaloneSourceToken(source.ID)
	if err != nil {
		return nil, fmt.Errorf("contacts: httpClientForSource: get standalone token for %s: %w", source.ID, err)
	}
	return &http.Client{
		Transport: &bearerTransport{token: token},
	}, nil
}

// bearerTransport stamps a static bearer token on every request. Used for
// standalone contact source HTTP clients where token refresh is handled
// proactively by the host before the client is built (see
// app.getValidContactSourceOAuthToken). No 401 retry — a stale token after
// build surfaces as a 401 to the caller, who returns the error up to the UI.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}
