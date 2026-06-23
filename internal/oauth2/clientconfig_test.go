package oauth2

import (
	"testing"
)

// testProvider is a CredentialsProvider that knows about a single config id.
type testProvider struct {
	id    string
	creds ClientCredentials
}

func (t testProvider) Lookup(configID string) (ClientCredentials, bool) {
	if configID == t.id {
		return t.creds, true
	}
	return ClientCredentials{}, false
}

func TestClientConfigForID_ProviderChain(t *testing.T) {
	// Drop any user-override hook the production code may have set, isolate
	// the test to the provider chain.
	saved := UserOverrideLookup
	UserOverrideLookup = nil
	t.Cleanup(func() { UserOverrideLookup = saved })

	// Register two test providers for synthetic config ids.
	RegisterCredentialsProvider(testProvider{id: "test-alpha", creds: ClientCredentials{ClientID: "A"}})
	RegisterCredentialsProvider(testProvider{id: "test-beta", creds: ClientCredentials{ClientID: "B"}})

	// alpha resolves to A.
	creds, ok := ClientConfigForID("test-alpha")
	if !ok || creds.ClientID != "A" {
		t.Fatalf("alpha: got ok=%v id=%q", ok, creds.ClientID)
	}

	// beta resolves to B.
	creds, ok = ClientConfigForID("test-beta")
	if !ok || creds.ClientID != "B" {
		t.Fatalf("beta: got ok=%v id=%q", ok, creds.ClientID)
	}

	// Unknown config id returns (zero, false).
	creds, ok = ClientConfigForID("test-unknown-config")
	if ok || creds.ClientID != "" {
		t.Fatalf("unknown: got ok=%v id=%q", ok, creds.ClientID)
	}
}

func TestClientConfigForID_UserOverrideWins(t *testing.T) {
	// Register a provider with a "shipped" value.
	RegisterCredentialsProvider(testProvider{id: "test-override-target", creds: ClientCredentials{ClientID: "SHIPPED"}})

	// Without override, provider value wins.
	saved := UserOverrideLookup
	UserOverrideLookup = nil
	creds, _ := ClientConfigForID("test-override-target")
	if creds.ClientID != "SHIPPED" {
		t.Fatalf("before override: got %q, want SHIPPED", creds.ClientID)
	}

	// With override, override wins.
	UserOverrideLookup = func(configID string) (ClientCredentials, bool) {
		if configID == "test-override-target" {
			return ClientCredentials{ClientID: "USER", ClientSecret: "USER-SECRET"}, true
		}
		return ClientCredentials{}, false
	}
	t.Cleanup(func() { UserOverrideLookup = saved })

	creds, ok := ClientConfigForID("test-override-target")
	if !ok {
		t.Fatal("expected ok=true with override")
	}
	if creds.ClientID != "USER" || creds.ClientSecret != "USER-SECRET" {
		t.Fatalf("override: got id=%q secret=%q, want USER/USER-SECRET", creds.ClientID, creds.ClientSecret)
	}

	// Other config ids aren't affected by the override.
	creds, _ = ClientConfigForID("test-alpha")
	if creds.ClientID != "A" {
		t.Fatalf("unrelated config: got %q, want A", creds.ClientID)
	}
}

func TestClientConfigForID_OverrideReturningFalseFallsThrough(t *testing.T) {
	// An override that explicitly returns (zero, false) for an id should NOT
	// short-circuit — the provider chain still runs.
	saved := UserOverrideLookup
	UserOverrideLookup = func(configID string) (ClientCredentials, bool) {
		return ClientCredentials{}, false
	}
	t.Cleanup(func() { UserOverrideLookup = saved })

	creds, ok := ClientConfigForID("test-alpha")
	if !ok || creds.ClientID != "A" {
		t.Fatalf("fallthrough: got ok=%v id=%q, want true/A", ok, creds.ClientID)
	}
}

