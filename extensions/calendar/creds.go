package calendar

import (
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Build-time OAuth credentials for the Calendar extension. Injected at
// build time via ldflags from a per-extension source (typically
// extensions/calendar/.env). Empty values are valid — when a slot is empty,
// the Auth Broker returns ErrAdditionalConsentRequired and the host's
// write-access account picker fires.
//
// Phase 1 (read-only CalDAV) does NOT use these slots; they're declared
// up front so Phase 2 (Google + Microsoft providers) only needs to pop
// values in via the build pipeline, not change any code.
var (
	// GoogleClientID is the OAuth2 client ID for the Calendar extension's
	// Google Cloud project. Carries the Google Calendar API scopes for
	// both read AND write (per the RW-at-link-time decision in the plan).
	GoogleClientID string

	// GoogleClientSecret pairs with GoogleClientID.
	GoogleClientSecret string

	// MicrosoftClientID is the OAuth2 client ID for the Calendar extension's
	// Azure AD app registration. May share an app registration with Aerion
	// core's mail registration if the user has added the Calendars.ReadWrite
	// scope (Microsoft permits scope-adds without re-review).
	MicrosoftClientID string
)

// OAuthClients returns the per-extension OAuth client configurations the
// Calendar extension contributes. The host calls this at startup and
// registers each entry into the global oauth2.ClientConfigForID resolver
// chain. Entries with empty ClientID are ignored — extensions can declare
// all their slots unconditionally and rely on build-time ldflags to fill
// in only the ones they have credentials for.
func OAuthClients() []coreapi.OAuthProviderRegistration {
	return []coreapi.OAuthProviderRegistration{
		{
			ConfigID:     "google-calendar",
			ClientID:     GoogleClientID,
			ClientSecret: GoogleClientSecret,
		},
		{
			// Microsoft desktop apps with PKCE omit the client secret.
			ConfigID: "microsoft-calendar",
			ClientID: MicrosoftClientID,
		},
	}
}
