package oauth2

import (
	"fmt"
	"strings"
	"sync"
)

// ClientCredentials is the OAuth2 client_id + secret pair for one client
// configuration. Each first-party extension owns its own client configuration
// so it can be verified, deployed, and revoked independently from Mail.
type ClientCredentials struct {
	ClientID     string
	ClientSecret string
}

// CredentialsProvider is the source-of-credentials interface that Aerion core
// and each extension implement. ClientConfigForID walks the registered chain
// at lookup time; first provider that knows the requested configID wins.
//
// Each extension owns its OWN credential injection at build time (per-extension
// .env / shim + a small creds.go in the extension package that registers a
// CredentialsProvider during Extension.Register()). Aerion core compiles in
// only its own *-mail creds via the built-in core provider.
type CredentialsProvider interface {
	// Lookup returns the credentials for the given client config id, or
	// (zero, false) if this provider doesn't know that id (or the value is
	// not yet provisioned, e.g., empty build-time var).
	Lookup(configID string) (ClientCredentials, bool)
}

// UserOverrideLookup is an optional pluggable hook for user-supplied creds
// (Settings → OAuth Credentials). If non-nil, it's checked BEFORE the provider
// chain — user values always win. Set during App.Startup by the credentials
// store package; can be nil during tests or if user-overrides are unused.
var UserOverrideLookup func(configID string) (ClientCredentials, bool)

// SlotAliasLookup is an optional pluggable hook that maps one slot id onto
// another (Settings → OAuth Credentials → pick "Aerion mail client"). When
// non-nil and the user has set an alias for the given configID, the lookup
// resolves to the aliased target instead of the slot's own creds. Checked
// AFTER UserOverrideLookup (custom creds win over an alias) and BEFORE the
// provider chain. Set during App.Startup by the credentials store package.
var SlotAliasLookup func(configID string) (target string, ok bool)

// ActiveChoiceLookup is an optional pluggable hook that returns the user's
// explicit picker selection for the slot ("custom" / "aerion-shipped" /
// "aerion-mail"). When set AND the user has recorded a choice, the resolver
// routes by choice — independent of which underlying rows exist — so picker
// switches no longer have to destroy stored values to take effect. When the
// hook is nil OR returns ok=false (no choice recorded), the resolver falls
// back to inferring the active choice from row presence (the pre-marker
// behavior), preserving compatibility with installs that upgraded from a
// pre-marker version. Set during App.Startup by the credentials store
// package.
var ActiveChoiceLookup func(configID string) (choice string, ok bool)

var (
	providersMu sync.RWMutex
	providers   []CredentialsProvider
)

// RegisterCredentialsProvider appends a provider to the resolution chain.
// Safe to call from package init() functions or from Extension.Register().
// Order matters: providers are queried in registration order, first-hit wins.
// Aerion core registers itself early (init); extensions register at their
// Register() time, after core. Result: core's *-mail slots always resolve
// before any extension's slots — but since slot names don't collide between
// core and extensions, the order is purely a performance hint.
func RegisterCredentialsProvider(p CredentialsProvider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers = append(providers, p)
}

// ClientConfigForID returns the credentials registered for the given client
// config id. Resolution order:
//
//  1. ActiveChoiceLookup — the user's explicit picker selection. When
//     present, routes:
//       - "custom"          → UserOverrideLookup only
//       - "aerion-mail"     → SlotAliasLookup only (recursive)
//       - "aerion-shipped"  → straight to provider chain
//     If the routed source returns nothing (e.g., choice="custom" but no
//     creds saved yet), falls through to the provider chain so OAuth
//     doesn't error out — the UI still shows "Custom" so the user knows
//     they need to save creds.
//
//  2. (No active choice recorded) → backward-compat inference using row
//     presence, preserving pre-marker behavior for upgraded installs:
//       UserOverrideLookup → SlotAliasLookup → providers.
//     First time the user touches the picker post-upgrade, an explicit
//     choice is recorded and the resolver stops using this branch for
//     that slot.
//
//  3. (zero, false) if nothing matches.
//
// Known config ids today: 'google-mail' / 'microsoft-mail' (Aerion core
// owns both, plus microsoft-contacts + microsoft-calendar which are
// registered as core aliases of microsoft-mail), 'google-contacts'
// (Contacts extension), 'google-calendar' (Calendar extension).
func ClientConfigForID(id string) (ClientCredentials, bool) {
	return clientConfigForIDDepth(id, 0)
}

// clientConfigForIDDepth caps recursive alias resolution at one hop so a
// misconfigured cycle (e.g., A→B→A) terminates safely. Depth 0 is the
// initial call; depth ≥1 means we're already resolving an alias and
// further aliases are ignored.
//
// The package-level hook variables (ActiveChoiceLookup,
// UserOverrideLookup, SlotAliasLookup) are captured into locals before
// nil-checking + calling. Without that capture the compiler is free to
// re-read the global between the nil check and the call, and a concurrent
// writer to the variable (settings UI probing shipped creds) could
// surface a nil dereference here. Today nothing else writes to these
// globals at runtime, but the local capture also makes intent explicit
// and survives future refactors.
func clientConfigForIDDepth(id string, depth int) (ClientCredentials, bool) {
	// 1. Explicit active-choice routing. Bypasses the row-presence
	//    inference entirely — if the user has picked "aerion-shipped",
	//    their saved Custom row is still in storage but the resolver
	//    deliberately skips it.
	if choiceHook := ActiveChoiceLookup; choiceHook != nil {
		if choice, ok := choiceHook(id); ok {
			switch choice {
			case "custom":
				if override := UserOverrideLookup; override != nil {
					if creds, found := override(id); found {
						return creds, true
					}
				}
				// Fall through to provider chain — Custom selected but
				// no creds saved yet.
				return providerChainLookup(id)
			case "aerion-mail":
				if depth == 0 {
					if alias := SlotAliasLookup; alias != nil {
						if target, found := alias(id); found && target != "" && target != id {
							return clientConfigForIDDepth(target, depth+1)
						}
					}
				}
				// Fall through to provider chain — alias row missing.
				return providerChainLookup(id)
			case "aerion-shipped":
				// Skip override + alias entirely.
				return providerChainLookup(id)
			}
			// Unknown choice value — fall through to inference for safety.
		}
	}

	// 2. Backward-compat inference (pre-marker behavior). Same order as
	//    before the active-choice marker was introduced.
	if override := UserOverrideLookup; override != nil {
		if creds, ok := override(id); ok {
			return creds, true
		}
	}
	if depth == 0 {
		if alias := SlotAliasLookup; alias != nil {
			if target, ok := alias(id); ok && target != "" && target != id {
				return clientConfigForIDDepth(target, depth+1)
			}
		}
	}
	return providerChainLookup(id)
}

// providerChainLookup walks the registered CredentialsProvider chain and
// returns the first hit. Factored out so the active-choice routing and
// the backward-compat inference path can share the same terminal step.
func providerChainLookup(id string) (ClientCredentials, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	for _, p := range providers {
		if creds, ok := p.Lookup(id); ok {
			return creds, true
		}
	}
	return ClientCredentials{}, false
}

// ShippedClientConfigForID returns whatever the registered provider chain
// resolves for the given id, bypassing any user override and any user-set
// slot alias. Used by the settings UI when it needs to know whether the
// slot's own shipped creds exist independent of the user's current pick.
//
// This exists so probing for "is there a shipped option?" doesn't have to
// mutate the global hook variables — that pattern was racy against
// concurrent ClientConfigForID readers (every OAuth refresh).
func ShippedClientConfigForID(id string) (ClientCredentials, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	for _, p := range providers {
		if creds, ok := p.Lookup(id); ok {
			return creds, true
		}
	}
	return ClientCredentials{}, false
}

// ClientConfigIDForProvider maps a provider name (as used by the existing
// GetProvider API and stored in the oauth_tokens.provider column) to its
// default mail-flavored client_config_id. Used by the credentials store to
// route legacy queries to the right client config.
//
//	"google", "google-contacts"       → "google-mail"
//	"microsoft", "microsoft-contacts" → "microsoft-mail"
func ClientConfigIDForProvider(name string) string {
	switch name {
	case "google", "google-contacts":
		return "google-mail"
	case "microsoft", "microsoft-contacts":
		return "microsoft-mail"
	default:
		return ""
	}
}

// GetProviderForClientConfig returns the OAuth2 provider configuration for
// the given client_config_id. The returned ProviderConfig carries the scopes
// and URLs appropriate to the provider (Google vs Microsoft) along with the
// client credentials registered for that specific client config.
//
// Used by the Auth Broker (internal/extensions/auth) when an extension needs
// to reach external services. Scopes in the returned config are the default
// for the underlying provider; callers may override Scopes for extension-
// specific scope subsets (e.g., calendar-only).
func GetProviderForClientConfig(clientConfigID string) (ProviderConfig, error) {
	creds, ok := ClientConfigForID(clientConfigID)
	if !ok {
		return ProviderConfig{}, fmt.Errorf("client config not configured: %s", clientConfigID)
	}
	switch {
	case strings.HasPrefix(clientConfigID, "google-"):
		cfg := GoogleProvider()
		cfg.ClientID = creds.ClientID
		cfg.ClientSecret = creds.ClientSecret
		return cfg, nil
	case strings.HasPrefix(clientConfigID, "microsoft-"):
		cfg := MicrosoftProvider()
		cfg.ClientID = creds.ClientID
		cfg.ClientSecret = creds.ClientSecret
		return cfg, nil
	default:
		return ProviderConfig{}, fmt.Errorf("cannot determine provider for client config: %s", clientConfigID)
	}
}
