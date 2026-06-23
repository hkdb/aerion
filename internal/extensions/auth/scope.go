package auth

import (
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// missingScopes returns the subset of requested scopes that aren't already
// granted. Scope coverage is by exact-string match on the Resource field;
// hierarchical scope handling (e.g., a parent scope covering child scopes) is
// left to the OAuth provider — Aerion treats scopes as opaque strings.
func missingScopes(granted []string, requested []coreapi.AuthScope) []coreapi.AuthScope {
	if len(requested) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		have[s] = struct{}{}
	}
	var missing []coreapi.AuthScope
	for _, req := range requested {
		if _, ok := have[req.Resource]; !ok {
			missing = append(missing, req)
		}
	}
	return missing
}

// resolveClientConfigID picks which ClientConfigID an extension's scope
// request should route to. Phase 1 rule: any extension-side request uses the
// "*-extensions" client config; Mail is reserved for the host. When the
// extensions config isn't provisioned yet, we fall back to the mail config so
// extensions can still be developed against a single Google project locally.
func resolveClientConfigID(provider string, extConfigProvisioned bool) coreapi.ClientConfigID {
	switch provider {
	case "google":
		if extConfigProvisioned {
			return "google-extensions"
		}
		return "google-mail"
	case "microsoft":
		if extConfigProvisioned {
			return "microsoft-extensions"
		}
		return "microsoft-mail"
	default:
		return ""
	}
}

// extConfigForProvider maps a provider name to the corresponding extension
// client config id used in the ClientConfigForID registry. Used by the broker
// for the "is extension config provisioned?" probe (LEGACY — the old
// shared-`*-extensions` model from Phase 1; phase 2b uses per-extension slots
// via extClientConfigForProvider instead).
func extConfigForProvider(provider string) string {
	switch provider {
	case "google":
		return "google-extensions"
	case "microsoft":
		return "microsoft-extensions"
	default:
		return ""
	}
}

// mailClientConfigForProvider returns Aerion core's mail client config id for
// the given provider. Used by Path 1 routing (extension's manifest declares
// some scopes should reuse mail OAuth via first_party_uses_core_for_scopes).
func mailClientConfigForProvider(provider string) string {
	switch provider {
	case "google":
		return "google-mail"
	case "microsoft":
		return "microsoft-mail"
	default:
		return ""
	}
}

// extClientConfigForProvider returns the per-extension client config id for
// the given provider + extension. Used by Path 2 routing (the extension's own
// OAuth project). Examples: "google-contacts", "microsoft-calendar".
func extClientConfigForProvider(provider, extensionID string) string {
	switch provider {
	case "google":
		return "google-" + extensionID
	case "microsoft":
		return "microsoft-" + extensionID
	default:
		return ""
	}
}
