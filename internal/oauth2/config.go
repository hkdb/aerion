// Package oauth2 provides OAuth2 authentication for email providers
package oauth2

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// Build-time variables injected via ldflags
// These are set during compilation using:
//
//	go build -ldflags "-X 'github.com/hkdb/aerion/internal/oauth2.GoogleClientID=xxx'"
//
// See Makefile for the complete build command.
// If ldflags are not set, credentials are loaded from the aerion-creds shim binary.
var (
	// GoogleClientID is the OAuth2 client ID for Google/Gmail (Mail-scoped project).
	// Same client also backs first-party extensions' Google flows for any scopes
	// listed in the extension manifest's first_party_uses_core_for_scopes (today:
	// contacts.readonly). When that's not enough (write scopes, full Calendar),
	// the picker UI offers GoogleTestingClientID instead — see below.
	GoogleClientID string

	// GoogleClientSecret is the OAuth2 client secret for Google/Gmail
	GoogleClientSecret string

	// MicrosoftClientID is the OAuth2 client ID for Microsoft/Outlook
	// (Mail-scoped registration). Also serves microsoft-contacts and
	// microsoft-calendar — Microsoft Graph doesn't gate scopes behind
	// verification, so one app registration covers all three surfaces.
	MicrosoftClientID string

	// GoogleTestingClientID is the shared OAuth2 client for first-party
	// extensions that need broader Google scopes than the mail project is
	// verified for (e.g., contacts.readwrite, full Calendar). Single un-
	// Google-verified test client backs both google-contacts and
	// google-calendar slots. Surfaced in the picker as
	// "Aerion - Google (Testing)" so users understand the verification
	// status before consenting. When the mail project eventually gets
	// verified with these scopes, the default in the picker UI switches
	// to "Aerion - Google" (which reuses GoogleClientID via a manifest-
	// declared scope route) and this slot becomes a fallback.
	GoogleTestingClientID string

	// GoogleTestingClientSecret pairs with GoogleTestingClientID.
	GoogleTestingClientSecret string
)


func init() {
	if GoogleClientID != "" {
		return
	}
	loadFromShim()
}

func loadFromShim() {
	// Search for the shim binary in known locations
	paths := []string{
		"/app/lib/aerion/aerion-creds", // Flatpak
	}

	// Also check next to the main binary
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "aerion-creds"))
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		out, err := exec.Command(p).Output()
		if err != nil {
			continue
		}
		var creds map[string]string
		if err := json.Unmarshal(out, &creds); err != nil {
			continue
		}
		GoogleClientID = creds["google_client_id"]
		GoogleClientSecret = creds["google_client_secret"]
		MicrosoftClientID = creds["microsoft_client_id"]
		// Optional shared testing client for the un-verified Google
		// project that backs extensions needing broader scopes. Empty
		// until provisioned — picker simply omits the "(Testing)" option.
		GoogleTestingClientID = creds["google_testing_client_id"]
		GoogleTestingClientSecret = creds["google_testing_client_secret"]
		return
	}
}

// IsGoogleConfigured returns true if Google OAuth credentials are
// available from ANY configured source — user override (Settings → OAuth
// Credentials), a user-set slot alias, or the shipped build-time vars.
// Routed through the resolver so a from-source build with empty
// build-time creds but a user override saved in the UI still passes the
// pre-flight check at the start of the OAuth flow.
func IsGoogleConfigured() bool {
	creds, ok := ClientConfigForID("google-mail")
	return ok && creds.ClientID != ""
}

// IsMicrosoftConfigured mirrors IsGoogleConfigured for Microsoft.
func IsMicrosoftConfigured() bool {
	creds, ok := ClientConfigForID("microsoft-mail")
	return ok && creds.ClientID != ""
}

// IsProviderConfigured returns true if the specified provider has OAuth credentials
func IsProviderConfigured(provider string) bool {
	switch provider {
	case "google":
		return IsGoogleConfigured()
	case "microsoft":
		return IsMicrosoftConfigured()
	default:
		return false
	}
}
