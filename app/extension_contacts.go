package app

import (
	"github.com/hkdb/aerion/extensions/contacts"
	extcontactsbe "github.com/hkdb/aerion/extensions/contacts/backend"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/oauth2"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// extensionOAuthProvider adapts a slice of coreapi.OAuthProviderRegistration
// entries into an oauth2.CredentialsProvider. Used to register a single
// extension's OAuth client configs into the global resolver chain without
// the extension itself having to import internal/oauth2.
type extensionOAuthProvider []coreapi.OAuthProviderRegistration

func (p extensionOAuthProvider) Lookup(configID string) (oauth2.ClientCredentials, bool) {
	for _, r := range p {
		if r.ConfigID != configID {
			continue
		}
		if r.ClientID == "" {
			return oauth2.ClientCredentials{}, false
		}
		return oauth2.ClientCredentials{
			ClientID:     r.ClientID,
			ClientSecret: r.ClientSecret,
		}, true
	}
	return oauth2.ClientCredentials{}, false
}

// initContactsExtension wires the Contacts extension's Bridge into App
// during Startup. All bridge logic lives in extensions/contacts/backend/
// bridge.go; this file exists ONLY so the host can supply the bridge with
// its host-provided dependencies (settings store, paths, db, event emitter)
// and so the embedded-field promotion makes the bridge methods Wails-bindable.
//
// The bridge lazy-initializes its Contacts-specific state (stores, per-
// extension SQLite, API wrapper) inside `ensureInit` on the first enabled
// method call. When Contacts is disabled in settings, zero work happens
// beyond the ~80-byte Bridge struct allocation — this is how the
// lightweight-by-default promise is held.
func (a *App) initContactsExtension() {
	// Per-extension Core handle for cross-extension coreapi calls (source
	// management via ListSources / LinkAccountSource). Distinct from the
	// Core constructed in the Startup Register loop but functionally
	// equivalent — both point at the same app, scoped to the same
	// extension identity for Auth routing.
	contactsCore := newCoreForExtension(a, a.contactsExt)

	a.ContactsBridge = extcontactsbe.NewContactsBridge(extcontactsbe.ContactsBridgeDeps{
		SettingsStore: a.settingsStore,
		Paths:         a.paths,
		DB:            a.db,
		Emitter: func(eventName string, payload any) {
			wailsRuntime.EventsEmit(a.ctx, eventName, payload)
		},
		// CardDAV passwords flow through Core.Storage().HostSecrets()
		// (Pattern B — core owns the lifecycle; extension reads). No
		// per-credential closure injection needed; the bridge constructs
		// the right key prefix when reading.
		//
		// Core gives the bridge access to host-owned cross-extension
		// surfaces — Contacts().ListSources() and LinkAccountSource()
		// back the sidebar + account-setup hook flows; Storage().HostSecrets()
		// backs CardDAV writes.
		Core: contactsCore,
	})

	// Register the extension's declared OAuth client configs with the
	// global resolver. The extension declares pairs as
	// coreapi.OAuthProviderRegistration entries — the host wraps them in
	// an oauth2.CredentialsProvider adapter so the extension never imports
	// internal/oauth2 directly. Replaces the prior package-init registration
	// that lived inside the extension's creds.go.
	oauth2.RegisterCredentialsProvider(extensionOAuthProvider(contacts.OAuthClients()))
}
