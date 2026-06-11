package app

import (
	extcontactsbe "github.com/hkdb/aerion/extensions/contacts/backend"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

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
		// Mirrors what the carddav syncer gets via SetTokenGetters — the
		// same proactively-refreshing token accessor for standalone
		// contacts-only OAuth sources, now reused on the write side so
		// create/update/delete succeed regardless of whether the user
		// linked the source to an email account or set it up via the
		// contacts-only OAuth flow.
		GetStandaloneSourceToken: a.getValidContactSourceOAuthToken,
	})

	// All OAuth slot resolution lives in internal/oauth2/core_provider.go
	// now — google-contacts and microsoft-contacts are owned there. The
	// contacts extension package carries no OAuth client vars of its own.
}
