package oauth2

import "fmt"

// ProviderConfig defines OAuth2 endpoints and settings for a provider
type ProviderConfig struct {
	Name         string   // Provider identifier: "google", "microsoft"
	DisplayName  string   // Human-readable name
	AuthURL      string   // Authorization endpoint
	TokenURL     string   // Token exchange endpoint
	Scopes       []string // Required OAuth scopes
	ClientID     string   // OAuth client ID
	ClientSecret string   // OAuth client secret (may be empty for public clients)
	LoginHint    string   // Optional: pre-fill the account picker (`login_hint`).
	//                       Used by the Contacts extension's write-access flow
	//                       to constrain the user to a specific email address
	//                       matching an existing read account. Empty = no hint.
}

// GoogleProvider returns the OAuth2 configuration for Google/Gmail
func GoogleProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "google",
		DisplayName: "Google",
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		Scopes: []string{
			"https://mail.google.com/",                                // Full Gmail access (IMAP/SMTP)
			"https://www.googleapis.com/auth/contacts.other.readonly", // Other contacts (for autocomplete)
			"https://www.googleapis.com/auth/contacts.readonly",       // Full contacts read access (for sync)
			"https://www.googleapis.com/auth/userinfo.email",          // Get user's email address
			"openid", // OpenID Connect
		},
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
	}
}

// MicrosoftProvider returns the OAuth2 configuration for Microsoft/Outlook
func MicrosoftProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "microsoft",
		DisplayName: "Microsoft",
		// Use "common" tenant for both personal and work/school accounts
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes: []string{
			"https://outlook.office.com/IMAP.AccessAsUser.All", // IMAP access
			"https://outlook.office.com/SMTP.Send",             // SMTP send
			// Note: Contacts.Read cannot be combined with Outlook scopes (different audience)
			// Use standalone contact source for Microsoft contacts
			"offline_access", // Refresh tokens
			"openid",         // OpenID Connect
			"email",          // Get user's email address
		},
		ClientID:     MicrosoftClientID,
		ClientSecret: "", // Public client, no secret needed
	}
}

// GoogleContactsOnlyProvider returns OAuth2 config for contacts-only access (standalone contact sources)
func GoogleContactsOnlyProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "google-contacts",
		DisplayName: "Google Contacts",
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		Scopes: []string{
			"https://www.googleapis.com/auth/contacts.readonly", // Full contacts read access
			"https://www.googleapis.com/auth/userinfo.email",    // Get user's email address
			"openid", // OpenID Connect
		},
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
	}
}

// GoogleCalendarProvider returns OAuth2 config for the Calendar extension's
// per-extension OAuth slot. Scope covers full Calendar API read+write.
// Mirrors GoogleContactsOnlyProvider's shape — separate ClientConfigID so
// extension and mail OAuth grants stay isolated.
func GoogleCalendarProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "google-calendar",
		DisplayName: "Google Calendar",
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		Scopes: []string{
			"https://www.googleapis.com/auth/calendar",       // Full Calendar API access (read + write)
			"https://www.googleapis.com/auth/userinfo.email", // Get user's email address
			"openid",                                         // OpenID Connect
		},
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
	}
}

// MicrosoftCalendarProvider returns OAuth2 config for the Calendar
// extension's Microsoft Graph slot. Scope covers Calendars.ReadWrite on
// the Graph audience (separate from mail's Outlook IMAP audience).
func MicrosoftCalendarProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "microsoft-calendar",
		DisplayName: "Outlook Calendar",
		AuthURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:    "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes: []string{
			"https://graph.microsoft.com/Calendars.ReadWrite", // Calendar CRUD via Graph
			"offline_access", // Refresh tokens
			"openid",         // OpenID Connect
			"email",          // Get user's email address
		},
		ClientID:     MicrosoftClientID,
		ClientSecret: "", // Public client, no secret needed
	}
}

// MicrosoftContactsOnlyProvider returns OAuth2 config for contacts-only access (standalone contact sources)
func MicrosoftContactsOnlyProvider() ProviderConfig {
	return ProviderConfig{
		Name:        "microsoft-contacts",
		DisplayName: "Microsoft Contacts",
		AuthURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:    "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes: []string{
			"https://graph.microsoft.com/Contacts.Read", // Contacts read access
			"offline_access",                            // Refresh tokens
			"openid",                                    // OpenID Connect
			"email",                                     // Get user's email address
		},
		ClientID:     MicrosoftClientID,
		ClientSecret: "", // Public client, no secret needed
	}
}

// GetProvider returns the OAuth2 configuration for the specified provider.
//
// For the mail entry-point names ("google", "microsoft") the returned
// config's ClientID/ClientSecret reflect the full resolver chain
// (UserOverrideLookup → SlotAliasLookup → registered providers), so a
// user-supplied client_id saved via Settings → OAuth Credentials wins
// over the shipped build-time defaults. Without this overlay the
// mail-add flow (App.StartOAuthFlow → Manager.StartAuthFlow → GetProvider)
// would silently use the embedded ClientID even after the user saved
// their own override — issue #138.
//
// Other names (extension / standalone-contacts variants) return their
// static ProviderConfig unchanged. Their callers (app/coreimpl.go and
// GetProviderForClientConfig) already overlay slot-resolved creds on top
// of the returned config, so retrofitting them here would be redundant
// and would change the URL/scope semantics of standalone callers.
func GetProvider(name string) (ProviderConfig, error) {
	switch name {
	case "google":
		return overlayResolvedCreds(GoogleProvider(), "google-mail"), nil
	case "microsoft":
		return overlayResolvedCreds(MicrosoftProvider(), "microsoft-mail"), nil
	case "google-contacts":
		return GoogleContactsOnlyProvider(), nil
	case "microsoft-contacts":
		return MicrosoftContactsOnlyProvider(), nil
	case "google-calendar":
		return GoogleCalendarProvider(), nil
	case "microsoft-calendar":
		return MicrosoftCalendarProvider(), nil
	default:
		return ProviderConfig{}, fmt.Errorf("unknown OAuth provider: %s", name)
	}
}

// overlayResolvedCreds replaces base.ClientID / base.ClientSecret with
// the values the resolver chain returns for slot, leaving the base
// config (URLs, scopes, name) untouched. When the resolver has no
// credentials for the slot — neither user override nor shipped — the
// base config is returned as-is, preserving stock behaviour for callers
// that haven't customised anything.
func overlayResolvedCreds(base ProviderConfig, slot string) ProviderConfig {
	creds, ok := ClientConfigForID(slot)
	if !ok {
		return base
	}
	base.ClientID = creds.ClientID
	base.ClientSecret = creds.ClientSecret
	return base
}

// SupportedProviders returns the list of supported OAuth provider names for email accounts
func SupportedProviders() []string {
	return []string{"google", "microsoft"}
}

// SupportedContactProviders returns the list of supported OAuth provider names for contacts-only sources
func SupportedContactProviders() []string {
	return []string{"google-contacts", "microsoft-contacts"}
}
