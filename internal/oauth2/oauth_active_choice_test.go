package oauth2

import (
	"testing"
)

// Each test in this file uses a unique synthetic slot id ("test-ac-*")
// because the registered-provider chain is global + append-only (see
// RegisterCredentialsProvider). Reusing real slot ids (google-mail,
// microsoft-mail, etc.) across subtests would let earlier registrations
// bleed into later tests' resolutions. The convention matches
// clientconfig_test.go's test-alpha / test-beta naming.

// activeChoiceTestProvider lets a test register a shipped client for a
// specific synthetic slot id.
type activeChoiceTestProvider struct {
	id    string
	creds ClientCredentials
}

func (t activeChoiceTestProvider) Lookup(id string) (ClientCredentials, bool) {
	if id == t.id {
		return t.creds, true
	}
	return ClientCredentials{}, false
}

// snapshotHooks captures + restores the three resolver-level hooks for
// the duration of the calling test. Hooks start nil — tests opt in by
// assigning specific stub closures.
func snapshotHooks(t *testing.T) {
	t.Helper()
	savedActive := ActiveChoiceLookup
	savedOverride := UserOverrideLookup
	savedAlias := SlotAliasLookup
	t.Cleanup(func() {
		ActiveChoiceLookup = savedActive
		UserOverrideLookup = savedOverride
		SlotAliasLookup = savedAlias
	})
	ActiveChoiceLookup = nil
	UserOverrideLookup = nil
	SlotAliasLookup = nil
}

// TestClientConfigForID_ActiveChoiceMatrix covers explicit-marker
// routing — each combination of (active choice × override row × alias
// row × shipped). Asserts:
//
//   - choice="custom" routes to UserOverrideLookup, falls through to
//     provider chain when no override exists
//   - choice="aerion-mail" routes to SlotAliasLookup (recursive), falls
//     through to provider chain when alias is missing
//   - choice="aerion-shipped" skips override + alias, goes straight to
//     the provider chain
//   - the routed source must not silently substitute another source's
//     answer (e.g., choice="aerion-shipped" must NOT return the stored
//     override even when one exists — that's the data-preservation
//     property)
func TestClientConfigForID_ActiveChoiceMatrix(t *testing.T) {
	cases := []struct {
		name           string
		slot           string
		choice         string
		choiceRecorded bool
		overrideID     string
		aliasTarget    string
		shipped        map[string]ClientCredentials
		wantID         string
		wantOK         bool
	}{
		{
			name:           "custom + override present → override wins",
			slot:           "test-ac-custom-with-override",
			choice:         "custom",
			choiceRecorded: true,
			overrideID:     "USER-1",
			wantID:         "USER-1", wantOK: true,
		},
		{
			name:           "custom + no override → falls through to shipped",
			slot:           "test-ac-custom-no-override",
			choice:         "custom",
			choiceRecorded: true,
			shipped:        map[string]ClientCredentials{"test-ac-custom-no-override": {ClientID: "SHIPPED-2"}},
			wantID:         "SHIPPED-2", wantOK: true,
		},
		{
			name:           "aerion-mail + alias present → recurses on target",
			slot:           "test-ac-amail-with-alias",
			choice:         "aerion-mail",
			choiceRecorded: true,
			aliasTarget:    "test-ac-amail-target",
			shipped:        map[string]ClientCredentials{"test-ac-amail-target": {ClientID: "MAIL-TARGET-3"}},
			wantID:         "MAIL-TARGET-3", wantOK: true,
		},
		{
			name:           "aerion-mail + no alias → falls through to slot's own shipped",
			slot:           "test-ac-amail-no-alias",
			choice:         "aerion-mail",
			choiceRecorded: true,
			shipped:        map[string]ClientCredentials{"test-ac-amail-no-alias": {ClientID: "OWN-SHIPPED-4"}},
			wantID:         "OWN-SHIPPED-4", wantOK: true,
		},
		{
			name:           "aerion-shipped + override present → override IGNORED, shipped wins",
			slot:           "test-ac-ashipped-with-override",
			choice:         "aerion-shipped",
			choiceRecorded: true,
			overrideID:     "USER-IGNORED-5",
			shipped:        map[string]ClientCredentials{"test-ac-ashipped-with-override": {ClientID: "SHIPPED-5"}},
			wantID:         "SHIPPED-5", wantOK: true,
		},
		{
			name:           "aerion-shipped + no shipped + no override → not configured",
			slot:           "test-ac-ashipped-empty",
			choice:         "aerion-shipped",
			choiceRecorded: true,
			wantID:         "", wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshotHooks(t)

			if tc.choiceRecorded {
				ActiveChoiceLookup = func(id string) (string, bool) {
					if id == tc.slot {
						return tc.choice, true
					}
					return "", false
				}
			}
			if tc.overrideID != "" {
				UserOverrideLookup = func(id string) (ClientCredentials, bool) {
					if id == tc.slot {
						return ClientCredentials{ClientID: tc.overrideID}, true
					}
					return ClientCredentials{}, false
				}
			}
			if tc.aliasTarget != "" {
				SlotAliasLookup = func(id string) (string, bool) {
					if id == tc.slot {
						return tc.aliasTarget, true
					}
					return "", false
				}
			}
			for shippedSlot, shippedCreds := range tc.shipped {
				RegisterCredentialsProvider(activeChoiceTestProvider{id: shippedSlot, creds: shippedCreds})
			}

			creds, ok := ClientConfigForID(tc.slot)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if creds.ClientID != tc.wantID {
				t.Errorf("ClientID = %q, want %q", creds.ClientID, tc.wantID)
			}
		})
	}
}

// TestClientConfigForID_RoundTripPreservesOverride encodes the
// data-preservation guarantee: a stored override survives a Custom →
// Aerion → Custom round trip via the picker. Pre-fix, the picker
// destroyed the override row on switch-away; here, only the active
// choice marker changes, so the override stays alive and is reachable
// again when the user switches back.
func TestClientConfigForID_RoundTripPreservesOverride(t *testing.T) {
	snapshotHooks(t)

	const slot = "test-ac-roundtrip"

	var activeChoice string                       // marker storage
	overrideStore := ClientCredentials{}          // override "row" payload
	var overrideStored bool                       // override "row exists" flag

	ActiveChoiceLookup = func(id string) (string, bool) {
		if id != slot || activeChoice == "" {
			return "", false
		}
		return activeChoice, true
	}
	UserOverrideLookup = func(id string) (ClientCredentials, bool) {
		if id != slot || !overrideStored {
			return ClientCredentials{}, false
		}
		return overrideStore, true
	}
	RegisterCredentialsProvider(activeChoiceTestProvider{
		id:    slot,
		creds: ClientCredentials{ClientID: "ROUNDTRIP-SHIPPED"},
	})

	// 1. User pastes Custom, picker records choice="custom".
	overrideStore = ClientCredentials{ClientID: "ROUNDTRIP-USER", ClientSecret: "ROUNDTRIP-SECRET"}
	overrideStored = true
	activeChoice = "custom"
	if creds, _ := ClientConfigForID(slot); creds.ClientID != "ROUNDTRIP-USER" {
		t.Fatalf("step 1: got %q, want ROUNDTRIP-USER", creds.ClientID)
	}

	// 2. User switches to Aerion - X. Marker flips to aerion-shipped;
	//    override row is INTENTIONALLY left in place.
	activeChoice = "aerion-shipped"
	if creds, _ := ClientConfigForID(slot); creds.ClientID != "ROUNDTRIP-SHIPPED" {
		t.Fatalf("step 2: got %q, want ROUNDTRIP-SHIPPED", creds.ClientID)
	}
	if !overrideStored {
		t.Fatal("step 2: override row was destroyed by the switch — this is the regression we are guarding against")
	}

	// 3. User switches back to Custom. Marker flips back to custom; the
	//    stored override is found again and used.
	activeChoice = "custom"
	if creds, _ := ClientConfigForID(slot); creds.ClientID != "ROUNDTRIP-USER" {
		t.Fatalf("step 3: got %q, want ROUNDTRIP-USER (override was lost during round trip)", creds.ClientID)
	}
}

// TestClientConfigForID_BackwardCompatInference verifies that when
// ActiveChoiceLookup is unset OR returns ok=false (e.g., a slot whose
// picker hasn't been touched since upgrading from a pre-marker
// version), the resolver falls back to the original row-presence
// inference. Without this guarantee, upgraded installs would lose
// access to their previously-saved overrides until they re-touched the
// picker.
func TestClientConfigForID_BackwardCompatInference(t *testing.T) {
	t.Run("no marker, has override → override wins", func(t *testing.T) {
		snapshotHooks(t)
		const slot = "test-ac-bc-override"
		ActiveChoiceLookup = nil
		UserOverrideLookup = func(id string) (ClientCredentials, bool) {
			if id == slot {
				return ClientCredentials{ClientID: "BC-USER"}, true
			}
			return ClientCredentials{}, false
		}
		if creds, ok := ClientConfigForID(slot); !ok || creds.ClientID != "BC-USER" {
			t.Fatalf("got ok=%v id=%q, want true/BC-USER", ok, creds.ClientID)
		}
	})

	t.Run("no marker, alias only → alias resolved", func(t *testing.T) {
		snapshotHooks(t)
		const slot = "test-ac-bc-alias-source"
		const target = "test-ac-bc-alias-target"
		ActiveChoiceLookup = nil
		SlotAliasLookup = func(id string) (string, bool) {
			if id == slot {
				return target, true
			}
			return "", false
		}
		RegisterCredentialsProvider(activeChoiceTestProvider{
			id:    target,
			creds: ClientCredentials{ClientID: "BC-ALIAS-TARGET"},
		})
		if creds, ok := ClientConfigForID(slot); !ok || creds.ClientID != "BC-ALIAS-TARGET" {
			t.Fatalf("got ok=%v id=%q, want true/BC-ALIAS-TARGET", ok, creds.ClientID)
		}
	})

	t.Run("hook returns ok=false → inference still runs", func(t *testing.T) {
		snapshotHooks(t)
		const slot = "test-ac-bc-hook-false"
		ActiveChoiceLookup = func(id string) (string, bool) {
			return "", false
		}
		UserOverrideLookup = func(id string) (ClientCredentials, bool) {
			if id == slot {
				return ClientCredentials{ClientID: "BC-HOOK-FALSE"}, true
			}
			return ClientCredentials{}, false
		}
		if creds, ok := ClientConfigForID(slot); !ok || creds.ClientID != "BC-HOOK-FALSE" {
			t.Fatalf("got ok=%v id=%q, want true/BC-HOOK-FALSE", ok, creds.ClientID)
		}
	})
}
