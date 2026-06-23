// Package backend is the host-linked implementation of the Contacts
// extension. It implements coreapi.Contacts (read API), owns the per-extension
// SQLite store, and exposes the lifecycle Register() that App.Startup calls.
//
// File map:
//   - register.go — Extension struct + Register(core) entry point
//   - api.go      — coreapi.Contacts implementation (Search/Get/List)
//   - convert.go  — internal types → coreapi.Contact converters
//   - store.go    — per-extension SQLite wrapper (extensions.OpenStore)
//   - api_test.go — wrapper unit tests against an in-memory SQLite
//
// The extension's manifest lives one level up at extensions/contacts/manifest.json
// (embedded via the contacts package at extensions/contacts/manifest.go).
//
// The extension's frontend lives at extensions/contacts/frontend (Svelte
// components, stores, account-setup hook panel).
package backend
