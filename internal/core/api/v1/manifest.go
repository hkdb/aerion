package v1

// Manifest is the metadata for one extension. Every first-party extension
// ships a manifest.json at its repo root (e.g., extensions/contacts/manifest.json)
// embedded into the binary via go:embed. Community extensions (v0.4+) will
// ship the same manifest.json at the root of their distribution tarball.
//
// Field choices favor subprocess+IPC distribution (v0.4+ commitment): no Go
// import paths, no compiled-type references, no host-coupled fields. The
// host reads the manifest before deciding whether to load an extension.
type Manifest struct {
	ID               string         `json:"id"`               // canonical extension id (matches settings.AllExtensionKeys)
	Name             string         `json:"name"`             // user-facing display name
	Version          string         `json:"version"`          // semver
	Description      string         `json:"description"`      // 1-2 sentence summary shown in Settings
	Author           string         `json:"author"`
	MinAerionVersion string         `json:"minAerionVersion"` // semver — host refuses to load if lower
	Capabilities     []string       `json:"capabilities"`     // coarse capabilities; see below
	OAuth            *ManifestOAuth `json:"oauth,omitempty"`  // OAuth routing config; nil if extension uses no OAuth
}

// ManifestOAuth declares how an extension's OAuth scope requests route through
// the Auth Broker. For each requested scope:
//
//   - If the scope is listed in FirstPartyUsesCoreForScopes, the broker routes
//     through Aerion core's mail OAuth (<provider>-mail client config). This
//     reuses the user's existing mail consent — no new OAuth prompt — but it's
//     only viable when the mail OAuth grant already covers that scope (e.g.,
//     contacts.readonly is on the mail grant for Google).
//
//   - Otherwise the broker routes through the extension's own client config
//     (<provider>-<extensionID>). If the account lacks the scope under that
//     config, broker returns *ErrAdditionalConsentRequired; the host runs an
//     incremental-consent flow.
//
// Mixed-scope HTTPClient calls (some scopes that route to mail, some to ext)
// are REJECTED — the extension must split into two calls.
//
// GATE: FirstPartyUsesCoreForScopes is honored ONLY for first-party extensions.
// Community extensions (v0.4+) declaring this field will fail manifest
// validation — handing them the user's mail OAuth would be a privilege
// escalation vector. For Phase 2b every extension is first-party so the field
// is unconditionally honored.
type ManifestOAuth struct {
	// FirstPartyUsesCoreForScopes lists the scope strings (exact match) that
	// should route through Aerion core's mail OAuth instead of the extension's
	// own client config. See gate above.
	FirstPartyUsesCoreForScopes []string `json:"first_party_uses_core_for_scopes,omitempty"`
}

// Capability is a coarse permission string an extension declares in its
// manifest. The host's runtime checks (Auth Broker scope checks, UI registry
// validation) verify finer-grained access at the API boundary; capabilities
// in the manifest are for upfront consent UI ("This extension wants to: read
// your contacts, add a rail tab").
//
// Known capability strings (treated as opaque otherwise so the set can grow
// without breaking older hosts):
//
//	"contacts.read"             — read core contacts and CardDAV-synced contacts
//	"contacts.write"            — write to CardDAV/Google/Microsoft (v0.4+)
//	"mail.read"                 — read messages and folders
//	"mail.write"                — mutate messages (move/archive/flag)
//	"compose"                   — open the composer with a prefilled draft
//	"ui.rail-tab"               — register a rail-tab UI surface
//	"ui.settings-tab"           — register a settings-tab UI surface
//	"ui.account-setup-hook"     — register a hook in the post-account-add flow
//	"ui.context-menu"           — register context-menu items
//	"ui.inbox-view"             — register an alternate inbox rendering
//	"storage"                   — open per-extension SQLite + KV
//	"network"                   — make outbound HTTP requests via Auth Broker
type Capability = string

// Extension is the Go-side handle every first-party extension exposes from
// its package. Community extensions (v0.4+) won't satisfy this interface
// directly (they're separate subprocesses), but their Register handshake
// over IPC will mirror these two methods.
//
// Register is called once per Aerion process lifetime, at startup, regardless
// of whether the extension is currently enabled. This matches the
// architecture-doc rule that descriptive UI registrations (rail tab, hooks)
// persist across enable/disable cycles. Active behaviors that depend on
// enabled state (sync schedulers, background work) are gated separately by
// IsExtensionEnabled checks inside Register, not by skipping Register.
//
// The returned Unregister removes all of the extension's registrations.
// Called by the host on process shutdown.
type Extension interface {
	// Manifest returns the extension's parsed manifest. Implementations
	// typically embed manifest.json via go:embed and parse once.
	Manifest() Manifest

	// Register wires the extension's UI surfaces (rail tabs, hooks, etc.)
	// and returns an Unregister func that tears them down.
	Register(core Core) (Unregister, error)
}
