package oauth2

import (
	"strings"
	"testing"
)

// TestGetProvider_OverrideMatrix verifies that GetProvider("google") and
// GetProvider("microsoft") honour the resolver chain across all four
// credential states: neither, shipped only, override only, and both
// (override wins). Pre-fix, the mail-add OAuth flow always used the
// shipped (build-time) ClientID even after the user saved a custom
// override — see issue #138.
//
// The test mutates package-level globals (GoogleClientID,
// MicrosoftClientID, UserOverrideLookup) so it must NOT run with
// t.Parallel(). Each subtest snapshots and restores those globals via
// t.Cleanup so the matrix rows stay isolated from each other and from
// any other test in the package.
func TestGetProvider_OverrideMatrix(t *testing.T) {
	cases := []struct {
		name           string
		providerName   string
		slot           string
		shippedID      string
		shippedSecret  string
		overrideID     string
		overrideSecret string
		wantID         string
		wantSecret     string
		wantConfigured bool
	}{
		// Microsoft — public client, secret stays empty in all cases except
		// when the user supplies one via override.
		{"microsoft/neither", "microsoft", "microsoft-mail", "", "", "", "", "", "", false},
		{"microsoft/shipped only", "microsoft", "microsoft-mail", "SHIPPED-MS", "", "", "", "SHIPPED-MS", "", true},
		{"microsoft/override only", "microsoft", "microsoft-mail", "", "", "USER-MS", "USER-MS-SECRET", "USER-MS", "USER-MS-SECRET", true},
		{"microsoft/override wins over shipped", "microsoft", "microsoft-mail", "SHIPPED-MS", "", "USER-MS", "USER-MS-SECRET", "USER-MS", "USER-MS-SECRET", true},

		// Google — both ID and secret are configurable.
		{"google/neither", "google", "google-mail", "", "", "", "", "", "", false},
		{"google/shipped only", "google", "google-mail", "SHIPPED-GOOG", "SHIPPED-GOOG-SECRET", "", "", "SHIPPED-GOOG", "SHIPPED-GOOG-SECRET", true},
		{"google/override only", "google", "google-mail", "", "", "USER-GOOG", "USER-GOOG-SECRET", "USER-GOOG", "USER-GOOG-SECRET", true},
		{"google/override wins over shipped", "google", "google-mail", "SHIPPED-GOOG", "SHIPPED-GOOG-SECRET", "USER-GOOG", "USER-GOOG-SECRET", "USER-GOOG", "USER-GOOG-SECRET", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Snapshot + restore globals for isolation between subtests.
			origGoogleID, origGoogleSecret := GoogleClientID, GoogleClientSecret
			origMSID := MicrosoftClientID
			origOverride := UserOverrideLookup
			t.Cleanup(func() {
				GoogleClientID, GoogleClientSecret = origGoogleID, origGoogleSecret
				MicrosoftClientID = origMSID
				UserOverrideLookup = origOverride
			})

			// Wire shipped state via the build-time vars — coreProvider
			// reads these when no override is in play. Clear the other
			// provider's shipped vars so they can't sneak into the lookup.
			GoogleClientID, GoogleClientSecret = "", ""
			MicrosoftClientID = ""
			switch tc.providerName {
			case "google":
				GoogleClientID, GoogleClientSecret = tc.shippedID, tc.shippedSecret
			case "microsoft":
				MicrosoftClientID = tc.shippedID
			}

			// Wire override state. nil hook = "no UI override saved" — the
			// stock production path before the user configures anything.
			UserOverrideLookup = nil
			if tc.overrideID != "" {
				UserOverrideLookup = func(slot string) (ClientCredentials, bool) {
					if slot == tc.slot {
						return ClientCredentials{ClientID: tc.overrideID, ClientSecret: tc.overrideSecret}, true
					}
					return ClientCredentials{}, false
				}
			}

			p, err := GetProvider(tc.providerName)
			if err != nil {
				t.Fatalf("GetProvider(%q): %v", tc.providerName, err)
			}
			if p.ClientID != tc.wantID {
				t.Errorf("ClientID = %q, want %q", p.ClientID, tc.wantID)
			}
			if p.ClientSecret != tc.wantSecret {
				t.Errorf("ClientSecret = %q, want %q", p.ClientSecret, tc.wantSecret)
			}

			// URLs and scopes identify the provider, not the credentials —
			// they must survive the overlay unchanged.
			urlFragment := map[string]string{
				"google":    "accounts.google.com",
				"microsoft": "microsoftonline",
			}[tc.providerName]
			if !strings.Contains(p.AuthURL, urlFragment) {
				t.Errorf("AuthURL = %q lost provider URL fragment %q", p.AuthURL, urlFragment)
			}
			if len(p.Scopes) == 0 {
				t.Error("Scopes empty after overlay — overlay should leave them untouched")
			}

			// IsProviderConfigured tracks the union of shipped + override —
			// shipped-only, override-only, and both all report true.
			if got := IsProviderConfigured(tc.providerName); got != tc.wantConfigured {
				t.Errorf("IsProviderConfigured(%q) = %v, want %v", tc.providerName, got, tc.wantConfigured)
			}
		})
	}
}

// TestGetProvider_OverrideForOtherSlotFallsThrough verifies that an
// override returning false for the requested slot (e.g., the user only
// configured Google overrides) doesn't short-circuit the resolver chain
// — Microsoft's shipped creds must still be reachable. Companion to
// TestClientConfigForID_OverrideReturningFalseFallsThrough at the
// resolver level; this one asserts the same property propagates through
// GetProvider's overlay step.
func TestGetProvider_OverrideForOtherSlotFallsThrough(t *testing.T) {
	origMSID := MicrosoftClientID
	origOverride := UserOverrideLookup
	t.Cleanup(func() {
		MicrosoftClientID = origMSID
		UserOverrideLookup = origOverride
	})

	MicrosoftClientID = "SHIPPED-MS"

	// Override is installed but only knows about google-mail — for any
	// other slot it returns (zero, false). The resolver must fall through
	// to coreProvider for microsoft-mail.
	UserOverrideLookup = func(slot string) (ClientCredentials, bool) {
		if slot == "google-mail" {
			return ClientCredentials{ClientID: "USER-GOOG"}, true
		}
		return ClientCredentials{}, false
	}

	p, err := GetProvider("microsoft")
	if err != nil {
		t.Fatalf("GetProvider(microsoft): %v", err)
	}
	if p.ClientID != "SHIPPED-MS" {
		t.Errorf("ClientID = %q, want SHIPPED-MS (override returns false for microsoft-mail; shipped should win)", p.ClientID)
	}
}
