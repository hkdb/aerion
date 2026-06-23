package auth

import (
	"fmt"
	"net/http"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/oauth2"
)

// Broker is the concrete implementation of coreapi.Auth. It mediates between
// extensions and Aerion's credential store + OAuth manager. Extensions get
// pre-configured HTTP clients; tokens never leave the broker's boundary.
type Broker struct {
	credStore    *credentials.Store
	oauthManager *oauth2.Manager
}

// NewBroker constructs a Broker bound to the given credential store and
// OAuth manager. Both are required and must be non-nil.
func NewBroker(credStore *credentials.Store, oauthManager *oauth2.Manager) *Broker {
	return &Broker{
		credStore:    credStore,
		oauthManager: oauthManager,
	}
}

// HTTPClient returns an *http.Client that injects an OAuth2 bearer token on
// every request and refreshes it transparently on 401 responses.
//
// Routing: if the extension-scoped client config (google-extensions /
// microsoft-extensions) is provisioned, scope requests route to that client
// config. Otherwise they fall back to the mail client config (so extensions
// can be developed before the second OAuth project is in place).
//
// If the account lacks tokens covering all requested scopes under the resolved
// client config, returns *coreapi.ErrAdditionalConsentRequired. The host
// (NOT the extension) handles the consent flow and retries.
func (b *Broker) HTTPClient(accountID string, scopes []coreapi.AuthScope) (*http.Client, error) {
	// Discover the account's provider via its existing Mail tokens. Every
	// authenticated account has a Mail row (per migration v29 backfill).
	mailTokens, err := b.credStore.GetOAuthTokens(accountID)
	if err != nil {
		return nil, fmt.Errorf("auth broker: get account tokens: %w", err)
	}

	// Decide which client config the extension should use for this provider.
	_, extProvisioned := oauth2.ClientConfigForID(extConfigForProvider(mailTokens.Provider))
	clientConfigID := resolveClientConfigID(mailTokens.Provider, extProvisioned)
	if clientConfigID == "" {
		return nil, fmt.Errorf("auth broker: cannot resolve client config for provider %q", mailTokens.Provider)
	}

	// Check whether the account already has tokens under that client config
	// with sufficient scope coverage. If not, signal the host to run consent.
	existing, err := b.credStore.GetOAuthTokensForClientConfig(accountID, string(clientConfigID))
	if err == credentials.ErrCredentialNotFound {
		return nil, &coreapi.ErrAdditionalConsentRequired{
			AccountID:      accountID,
			ClientConfigID: clientConfigID,
			MissingScopes:  scopes,
		}
	}
	if err != nil {
		return nil, fmt.Errorf("auth broker: check tokens: %w", err)
	}

	if missing := missingScopes(existing.Scopes, scopes); len(missing) > 0 {
		return nil, &coreapi.ErrAdditionalConsentRequired{
			AccountID:      accountID,
			ClientConfigID: clientConfigID,
			MissingScopes:  missing,
		}
	}

	return &http.Client{
		Transport: &bearerRefreshTransport{
			base:           http.DefaultTransport,
			credStore:      b.credStore,
			oauthManager:   b.oauthManager,
			accountID:      accountID,
			clientConfigID: string(clientConfigID),
		},
	}, nil
}

// HTTPClientForExtension is the Phase 2b entry point that knows WHICH extension
// is making the request, and reads that extension's manifest to decide whether
// each requested scope routes through:
//
//   - Aerion core's mail OAuth (<provider>-mail client config) — when the
//     scope is listed in manifest.OAuth.FirstPartyUsesCoreForScopes; reuses
//     the user's existing mail consent.
//   - The extension's own client config (<provider>-<extensionID>) — when the
//     scope is NOT in the manifest list; triggers ErrAdditionalConsentRequired
//     if the account lacks the scope under that config.
//
// Mixed-scope calls (some core-routed, some own-routed) are REJECTED — the
// extension must split into separate HTTPClient calls per routing target.
//
// GATE: FirstPartyUsesCoreForScopes is honored ONLY for first-party extensions.
// Community extensions (v0.4+) declaring the field will fail manifest
// validation upstream. For Phase 2b every extension is first-party.
func (b *Broker) HTTPClientForExtension(
	extensionID string,
	manifest coreapi.Manifest,
	accountID string,
	scopes []coreapi.AuthScope,
) (*http.Client, error) {
	// Discover the account's provider via its existing Mail tokens. Every
	// authenticated OAuth account has a Mail row (per migration v29 backfill).
	mailTokens, err := b.credStore.GetOAuthTokens(accountID)
	if err != nil {
		return nil, fmt.Errorf("auth broker: get account tokens: %w", err)
	}

	// Classify each requested scope: does it use Aerion core's mail OAuth
	// (per the manifest's first_party_uses_core_for_scopes) or the extension's
	// own client config?
	useCoreSet := map[string]bool{}
	if manifest.OAuth != nil {
		for _, s := range manifest.OAuth.FirstPartyUsesCoreForScopes {
			useCoreSet[s] = true
		}
	}
	var coreScopes, ownScopes []coreapi.AuthScope
	for _, s := range scopes {
		if useCoreSet[s.Resource] {
			coreScopes = append(coreScopes, s)
			continue
		}
		ownScopes = append(ownScopes, s)
	}
	if len(coreScopes) > 0 && len(ownScopes) > 0 {
		return nil, fmt.Errorf(
			"auth broker: extension %q requested mixed routing scopes; split into separate HTTPClient calls (core-routed: %d, own-routed: %d)",
			extensionID, len(coreScopes), len(ownScopes),
		)
	}

	// Resolve the target client config ID.
	var clientConfigID string
	switch {
	case len(coreScopes) > 0:
		// Path 1: core mail OAuth.
		clientConfigID = mailClientConfigForProvider(mailTokens.Provider)
	default:
		// Path 2: extension's own creds. ownScopes carries everything (and may
		// be empty if scopes was empty — degenerate case).
		clientConfigID = extClientConfigForProvider(mailTokens.Provider, extensionID)
	}
	if clientConfigID == "" {
		return nil, fmt.Errorf("auth broker: cannot resolve client config for provider %q (extension %q)", mailTokens.Provider, extensionID)
	}

	// Check whether the account already has tokens under that client config
	// with sufficient scope coverage. If not, signal the host to run consent.
	existing, err := b.credStore.GetOAuthTokensForClientConfig(accountID, clientConfigID)
	if err == credentials.ErrCredentialNotFound {
		return nil, &coreapi.ErrAdditionalConsentRequired{
			AccountID:      accountID,
			ClientConfigID: coreapi.ClientConfigID(clientConfigID),
			MissingScopes:  scopes,
		}
	}
	if err != nil {
		return nil, fmt.Errorf("auth broker: check tokens: %w", err)
	}

	if missing := missingScopes(existing.Scopes, scopes); len(missing) > 0 {
		return nil, &coreapi.ErrAdditionalConsentRequired{
			AccountID:      accountID,
			ClientConfigID: coreapi.ClientConfigID(clientConfigID),
			MissingScopes:  missing,
		}
	}

	return &http.Client{
		Transport: &bearerRefreshTransport{
			base:           http.DefaultTransport,
			credStore:      b.credStore,
			oauthManager:   b.oauthManager,
			accountID:      accountID,
			clientConfigID: clientConfigID,
		},
	}, nil
}

// IMAPClient returns an authenticated IMAP client for the account. Phase 1
// scaffolds the interface; real IMAP wiring lands in Phase 2 when an
// extension needs it (Sieve, custom X-* commands, etc.). Mail itself
// continues to use the existing imap.Pool — it does NOT route through the
// broker.
func (b *Broker) IMAPClient(accountID string, requiredCaps []string) (coreapi.IMAPClient, error) {
	return nil, coreapi.ErrUnimplemented
}

// SMTPClient returns an authenticated SMTP client. Phase 1 stub; Mail uses
// the existing smtp.Client path directly. Extensions needing custom outbound
// (delayed-send queues, etc.) will wire this in Phase 2+.
func (b *Broker) SMTPClient(accountID string) (coreapi.SMTPClient, error) {
	return nil, coreapi.ErrUnimplemented
}
