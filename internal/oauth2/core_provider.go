package oauth2

// coreProvider is Aerion core's CredentialsProvider. It owns every slot:
//
//   - `google-mail` — mail's verified Google client (GoogleClientID).
//   - `google-contacts` / `google-calendar` — both resolve to the shared
//     `GoogleTestingClientID` (the un-Google-verified test project). One
//     client backs every extension that needs broader Google scopes.
//   - `microsoft-mail` / `microsoft-contacts` / `microsoft-calendar` — all
//     three resolve to `MicrosoftClientID`. Microsoft Graph doesn't gate
//     scopes behind verification the way Google does, so a single Azure AD
//     app registration covers Mail + Contacts + Calendar.
//
// No per-extension OAuth credentials live in the extension packages — the
// vars + ldflags + .env all consolidate here. Extensions stay focused on
// domain logic.
//
// Registered automatically at package init.
type coreProvider struct{}

func (coreProvider) Lookup(configID string) (ClientCredentials, bool) {
	switch configID {
	case "google-mail":
		if GoogleClientID == "" {
			return ClientCredentials{}, false
		}
		return ClientCredentials{ClientID: GoogleClientID, ClientSecret: GoogleClientSecret}, true
	case "google-contacts", "google-calendar":
		if GoogleTestingClientID == "" {
			return ClientCredentials{}, false
		}
		return ClientCredentials{ClientID: GoogleTestingClientID, ClientSecret: GoogleTestingClientSecret}, true
	case "microsoft-mail", "microsoft-contacts", "microsoft-calendar":
		if MicrosoftClientID == "" {
			return ClientCredentials{}, false
		}
		// Microsoft desktop apps omit the client secret (uses PKCE).
		return ClientCredentials{ClientID: MicrosoftClientID, ClientSecret: ""}, true
	default:
		return ClientCredentials{}, false
	}
}

func init() {
	RegisterCredentialsProvider(coreProvider{})
}
