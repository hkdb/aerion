// Package ui is the host-side implementation of coreapi.UI. Extensions
// register rail tabs, settings tabs, context-menu items, inbox views, and
// account-setup hooks here; the frontend reads back the registrations via
// the Wails-bound List* methods in app/extension_ui.go.
//
// Phase 2a wires the two registrations that have real consumers in v0.3.x:
// RegisterRailTab (rendered by ExtensionRail.svelte) and RegisterAccountSetupHook
// (rendered by AccountDialog.svelte after a new account is created). The
// other three registration types (settings tab, context menu, inbox view)
// accept registrations but no consumer reads them yet — they are reserved
// for Phase 3+.
//
// All Register* methods return an Unregister func the caller invokes to
// remove the registration. This makes it safe for extensions to be
// disabled at runtime: the host invokes the returned func and the
// frontend's next List* query no longer sees the entry.
package ui
