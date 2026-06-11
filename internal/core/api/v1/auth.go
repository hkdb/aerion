package v1

import (
	"net/http"
)

// ClientConfigID identifies an OAuth client configuration (e.g., a specific
// Google Cloud project or Azure AD app registration). Each extension owns its
// own ClientConfigID, distinct from Mail's. See plan: each extension uses its
// own creds, with the same ClientConfigID potentially shared by future
// consolidation if combined-scope verification lands.
type ClientConfigID string

// OAuthProviderRegistration declares a single OAuth client config that an
// extension contributes at startup. Extensions export a slice of these (e.g.,
// `contacts.OAuthClients()`) so the host can wire them into the global
// `internal/oauth2.ClientConfigForID` resolver without the extension having
// to import `internal/oauth2` directly — the closure-injection pattern used
// elsewhere is awkward for slice-of-providers registration. The host
// translates each registration into an `oauth2.CredentialsProvider` and
// appends it to the resolver chain at `App.Startup`.
//
// Empty ClientID entries are ignored by the host — extensions can list all
// their configs unconditionally and rely on the build-time ldflags injection
// to fill in only the ones they have credentials for.
//
// Microsoft desktop apps with PKCE omit the secret; ClientSecret stays
// empty for those.
type OAuthProviderRegistration struct {
	ConfigID     string `json:"configId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// AuthScope is a single OAuth scope an extension needs against a provider,
// paired with a human-readable reason shown to the user at consent time.
type AuthScope struct {
	Resource string `json:"resource"` // e.g., "https://www.googleapis.com/auth/calendar"
	Reason   string `json:"reason"`   // shown to user at consent
}

// IMAPClient and SMTPClient are interface{} here to avoid leaking go-imap/v2
// types into the public API surface. Concrete implementations type-assert to
// the appropriate client in the host package. We keep these typed as any so
// extensions can pass them to provider-specific code that imports the same
// client library directly (which is acceptable for first-party extensions
// living in the same Go module).
//
// If/when community extensions land, these become Aerion-defined facades.
type IMAPClient = any
type SMTPClient = any

// Auth is the only path extensions reach external services. They never see
// access tokens, refresh tokens, or passwords. Token refresh is transparent.
//
// Routing: the broker resolves the right ClientConfigID for the requested
// scopes (e.g., contacts-write scopes route to "google-contacts"; calendar
// scopes to "google-calendar"). If the account lacks tokens covering those
// scopes under the target ClientConfigID, the broker returns
// ErrAdditionalConsentRequired and the extension drives the user-facing
// grant flow (e.g., WriteAccessAccountPicker for contacts).
type Auth interface {
	// HTTPClient returns an *http.Client with bearer token injection and
	// transparent refresh-on-401. The extension calls the client normally.
	HTTPClient(accountID string, scopes []AuthScope) (*http.Client, error)

	// IMAPClient returns an authenticated IMAP client. Used for Sieve
	// (PUTSCRIPT), custom X-* commands, or any extension needing direct IMAP.
	IMAPClient(accountID string, requiredCaps []string) (IMAPClient, error)

	// SMTPClient returns an authenticated SMTP client. For outbound sends
	// not handled by the standard Compose API (e.g., delayed-send queues).
	SMTPClient(accountID string) (SMTPClient, error)

	// StartIncrementalConsent runs an interactive OAuth consent flow that
	// asks the user to grant additional scopes against an existing identity
	// (mail account OR standalone contact source), then persists the
	// resulting tokens. Synchronous: blocks until the flow completes,
	// returning nil on success or an error if the user cancels or the
	// callback fails. The host opens the user's browser to the consent URL.
	//
	// Existing scopes already granted to this identity under the same
	// clientConfigID are preserved — the host requests the union of
	// (existing grants ∪ requested scopes) so the issued token covers both
	// reads (existing) and the newly-requested writes.
	//
	// Exactly ONE of AccountID / SourceID in the request must be set, and
	// determines where tokens are persisted (account-keyed or
	// source-keyed). ExpectedEmail enforces an email-match safeguard: the
	// flow rejects if the user grants from a different account than the
	// picked identity. LoginHint pre-fills the OAuth account picker (helpful
	// UX; not a security boundary — it's just a hint to the IdP).
	//
	// Used by extensions whose write paths hit ErrAdditionalConsentRequired
	// from HTTPClient.
	StartIncrementalConsent(req StartIncrementalConsentRequest) error
}

// StartIncrementalConsentRequest packages the inputs for an incremental-
// consent run. Exactly one of AccountID / SourceID must be non-empty; the
// chosen field determines where tokens are persisted after the OAuth
// callback completes (account-keyed via SetOAuthTokensForClientConfig vs
// source-keyed via SetSourceOAuthTokens).
type StartIncrementalConsentRequest struct {
	// ClientConfigID is the slot the OAuth flow runs as (e.g.,
	// "google-contacts"). The host resolves its client_id/secret through
	// the standard credential chain (user override → shipped).
	ClientConfigID ClientConfigID
	// Scopes carries the additional scope(s) the extension needs beyond
	// what the existing grant covers.
	Scopes []AuthScope
	// AccountID is the mail account to incrementally consent against.
	// Mutually exclusive with SourceID.
	AccountID string
	// SourceID is the standalone contact source to incrementally consent
	// against. Mutually exclusive with AccountID.
	SourceID string
	// ExpectedEmail is the email the OAuth callback must report. Empty
	// disables the check (not recommended). Mismatch → flow returns an
	// error and discards the granted tokens.
	ExpectedEmail string
	// LoginHint is forwarded to the IdP as the `login_hint` parameter so
	// the account picker pre-fills with this email. Optional; the IdP
	// may ignore it.
	LoginHint string
}
