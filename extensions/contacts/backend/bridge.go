package backend

import (
	"errors"
	"fmt"
	"sync"

	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/database"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/platform"
)

// ContactsBridge is the Wails-bindable surface for the Contacts extension. It's
// embedded into the host `*app.App` struct; Go's method-promotion makes
// every ContactsBridge method appear on App so Wails' reflection-based bind
// generator picks them up. All Contacts-specific logic lives here, not
// in the host. The host's `app/extension_contacts.go` is reduced to a
// dozen lines of construction wiring.
//
// Method naming: all Wails-bound bridge methods use the `Contacts_` prefix
// so they can't collide with another extension's methods after embedding
// into the same App. This convention is documented in docs/EXTENSIONS.md
// and is enforced by code review when accepting 3rd-party extension PRs.
//
// Lightweight-by-default invariant: when the user has the Contacts
// extension disabled, NOTHING is loaded beyond the ~80-byte ContactsBridge struct
// itself. Stores, the extension's per-extension SQLite, and the API
// wrapper are all lazy-constructed inside `ensureInit`, gated by
// `sync.Once`. The first enabled method call triggers init; subsequent
// calls are fast. If the user disables the extension after it was
// initialized, the in-memory state stays until the next Aerion launch
// (acceptable trade — matches VS Code / browser extension behavior).
type ContactsBridge struct {
	// Dependencies provided by the host at construction time. None of these
	// own anything Contacts-specific.
	deps ContactsBridgeDeps

	// Lazy-initialized Contacts state. Nil while disabled or until the
	// first enabled method call kicks ensureInit.
	initOnce sync.Once
	initErr  error
	api      *API
}

// ContactsBridgeDeps bundles the host-provided dependencies the bridge needs.
// Grouped into a struct so adding a new dep (e.g., logger, event bus)
// doesn't churn every call site in the host.
type ContactsBridgeDeps struct {
	// SettingsStore is consulted on every bridge call for the enabled gate
	// (lightweight invariant — disabled calls short-circuit before any work).
	SettingsStore SettingsStore

	// Paths gives the bridge access to the OS-appropriate data directory
	// for opening the extension's per-extension SQLite. Read at init time.
	Paths *platform.Paths

	// DB is the shared application database. Used to construct the
	// Contacts-specific stores at init time.
	DB *database.DB

	// Emitter forwards `contacts:*` Wails events to the frontend. Captured
	// here so the bridge doesn't have to reach back into the host for ctx
	// every time it needs to publish a conflict event.
	Emitter EventEmitter

	// Core is the coreapi.Core handle the bridge uses to call host-owned
	// cross-extension surfaces:
	//   - Source-management methods (ListSources, LinkAccountSource) that
	//     back the extension's sidebar + account-setup hook.
	//   - Storage().HostSecrets() — read-only access to core-managed
	//     CardDAV passwords, since the contacts extension's writes need
	//     the password but core owns the credential lifecycle (Pattern B
	//     per docs/EXTENSIONS.md).
	Core coreapi.Core

	// GetStandaloneSourceToken returns a valid OAuth access token for a
	// standalone contacts-only OAuth source (account_id IS NULL). Mirrors
	// the host getter the carddav syncer uses for the read path; the
	// contacts API uses it for the write path so create/update/delete work
	// on standalone Google/Microsoft sources just like they do on
	// account-linked ones. Nil-safe (writes to standalone sources then
	// error with a clear message).
	GetStandaloneSourceToken func(sourceID string) (string, error)
}

// SettingsStore is the narrow interface the bridge needs from the host's
// settings store. Defined here (rather than importing the concrete type)
// so 3rd-party extensions can swap in their own implementation for tests
// and so this file doesn't grow a host-package dependency.
type SettingsStore interface {
	IsExtensionEnabled(id string) (bool, error)
}

// EventEmitter forwards Wails events to the frontend. The host wires this
// to `wailsRuntime.EventsEmit(ctx, ...)` during Startup once the Wails ctx
// is available. Defined as a function type so callers don't have to write
// a one-method struct.
type EventEmitter func(eventName string, payload any)

// NewContactsBridge constructs the bridge with its dependencies. Does NOT touch
// the DB or open any extension state — that happens lazily in ensureInit
// when the first enabled method call arrives.
func NewContactsBridge(deps ContactsBridgeDeps) *ContactsBridge {
	return &ContactsBridge{deps: deps}
}

// extensionID is the key the bridge looks up in settings for the
// enabled-state check. Kept as a const so a typo doesn't silently disable
// every bridge method.
const extensionID = "contacts"

// gateEnabled returns true when the extension is currently enabled AND
// the host gave us a SettingsStore. Returns false (silently) when the
// store is nil — the host always sets it, so nil here means a misconfigured
// test environment where short-circuiting is the right behavior.
//
// Errors reading the settings table also count as "not enabled" — a
// best-effort read; we don't want a transient DB hiccup to surface as
// "your extension method failed."
func (b *ContactsBridge) gateEnabled() bool {
	if b.deps.SettingsStore == nil {
		return false
	}
	enabled, err := b.deps.SettingsStore.IsExtensionEnabled(extensionID)
	if err != nil {
		return false
	}
	return enabled
}

// ensureInit lazily constructs the Contacts-specific stores, the per-
// extension SQLite, and the API wrapper. Called only after a successful
// gateEnabled() check so disabled extensions never trigger any of this
// work. sync.Once means it runs at most once per process lifetime; later
// disable-then-enable cycles in the same session reuse the same state
// (and the disabled call still short-circuits before reaching here).
func (b *ContactsBridge) ensureInit() error {
	b.initOnce.Do(func() {
		if b.deps.DB == nil || b.deps.Paths == nil {
			b.initErr = errors.New("contacts.ContactsBridge: missing DB or Paths in deps")
			return
		}
		contactStore := contact.NewStore(b.deps.DB.DB)
		carddavStore := carddav.NewStore(b.deps.DB.DB)
		extStore, err := NewStore(b.deps.Paths.Data)
		if err != nil {
			b.initErr = err
			return
		}
		b.api = NewAPI(contactStore, carddavStore, extStore, b.deps.Core, b.deps.DB.DB)
		b.api.SetStandaloneSourceTokenGetter(b.deps.GetStandaloneSourceToken)
	})
	return b.initErr
}

// emitConflict translates a `*coreapi.ErrConflict` from a write path into
// a `contacts:conflict` event the frontend listens for. Returns true when
// the error was a conflict (and an event was emitted) so the caller can
// short-circuit further error handling — the user's intent was acknowledged,
// just superseded by the server.
func (b *ContactsBridge) emitConflict(err error) bool {
	var conflict *coreapi.ErrConflict
	if !errors.As(err, &conflict) {
		return false
	}
	if b.deps.Emitter != nil {
		b.deps.Emitter("contacts:conflict", map[string]string{
			"contactId": conflict.ContactID,
			"message":   conflict.Message,
		})
	}
	return true
}

// ============================================================================
// Wails-bound surface
//
// Every method below uses the `Contacts_` prefix so it can't collide with
// any other extension's bridge methods after embedding into App. The
// frontend imports these as e.g. `Contacts_UpdateContact` from
// $wailsjs/go/app/App.
// ============================================================================

// Contacts_ListContactsForBrowse returns contacts filtered by sourceID:
//   - ""                            → merged (local + carddav, search applied)
//   - SourceIDLocal                 → core local contacts only
//   - <carddav source UUID>         → contacts from a specific CardDAV source
//
// Gated on the extension being enabled — disabled returns nil so the
// frontend can call this unconditionally without checking state.
func (b *ContactsBridge) Contacts_ListContactsForBrowse(query, sourceID string, limit, offset int) ([]coreapi.Contact, error) {
	if !b.gateEnabled() {
		return nil, nil
	}
	if err := b.ensureInit(); err != nil {
		return nil, err
	}
	return b.api.ListContacts(coreapi.ContactFilter{
		Query:    query,
		SourceID: sourceID,
		Limit:    limit,
		Offset:   offset,
	})
}

// Contacts_GetContactDetail returns a single contact by email (if argument
// contains '@') or by CardDAV UUID otherwise.
func (b *ContactsBridge) Contacts_GetContactDetail(emailOrID string) (*coreapi.Contact, error) {
	if !b.gateEnabled() {
		return nil, nil
	}
	if err := b.ensureInit(); err != nil {
		return nil, err
	}
	return b.api.GetContact(emailOrID)
}

// Contacts_CreateContact creates a new contact and returns its id.
//
// Dispatch by input.SourceID (Track B / 2b.2.c):
//   - "", "local", "local:manual" → local manual entry. Returns the normalized
//     email as the id.
//   - <CardDAV source UUID>        → PUTs a new vCard to the source's
//     addressbook (input.AddressbookID, or the source's first enabled
//     addressbook if empty). Returns the new record UUID.
//   - "local:collected"            → rejected.
//   - Google / Microsoft sources   → ErrUnimplemented (2b.3).
//
// The historical `Contacts_CreateLocalContact(email, name)` shape was renamed
// here in Track B because the backend already dispatched by source; the bridge
// just hadn't surfaced the full input shape yet.
func (b *ContactsBridge) Contacts_CreateContact(input coreapi.ContactCreateInput) (string, error) {
	if !b.gateEnabled() {
		return "", nil
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	return b.api.CreateContact(input)
}

// Contacts_ListAddressbooks returns the enabled addressbooks for a CardDAV
// source. Used by the Add Contact dialog to populate the addressbook picker
// when the user chooses a multi-addressbook source.
//
// Returns nil for: extension disabled, empty sourceID, non-CardDAV source,
// or any error during lookup (the caller treats nil as "no addressbooks to
// pick" — falls back to letting the backend resolve the default).
func (b *ContactsBridge) Contacts_ListAddressbooks(sourceID string) ([]coreapi.Addressbook, error) {
	if !b.gateEnabled() {
		return nil, nil
	}
	if err := b.ensureInit(); err != nil {
		return nil, err
	}
	return b.api.ListAddressbooks(sourceID)
}

// Contacts_ListSources returns all configured contact sources via the
// host's coreapi.Contacts surface. The extension's frontend store
// (extensions/contacts/frontend/stores/contactSources.svelte.ts) caches
// the result and derives isSourceWritable / source-by-id lookups locally
// off the cached array.
//
// Returns nil when extension is disabled. No ensureInit required — this
// proxies to host state via coreapi, never touches the extension's own
// stores. Frontend can call this safely even before the extension's
// SQLite has been opened.
func (b *ContactsBridge) Contacts_ListSources() ([]coreapi.ContactSource, error) {
	if !b.gateEnabled() {
		return nil, nil
	}
	if b.deps.Core == nil {
		return nil, nil
	}
	return b.deps.Core.Contacts().ListSources()
}

// Contacts_LinkAccountSource creates a new contact source backed by an
// existing email account's OAuth tokens. Called by the AccountContactsHookPanel
// after a user clicks "Set up contacts" during the post-account-add flow.
// Returns the new source's id.
//
// Returns "" (no error) when extension is disabled. Otherwise proxies to
// coreapi.Contacts.LinkAccountSource, which in turn delegates to the host's
// existing source-management implementation.
func (b *ContactsBridge) Contacts_LinkAccountSource(accountID, name string, syncInterval int) (string, error) {
	if !b.gateEnabled() {
		return "", nil
	}
	if b.deps.Core == nil {
		return "", nil
	}
	return b.deps.Core.Contacts().LinkAccountSource(accountID, name, syncInterval)
}

// Contacts_SyncSource triggers an immediate sync against one source.
// Used by the sidebar footer's Ctrl+Shift+S handler so the user can
// refresh the focused address book without opening settings.
func (b *ContactsBridge) Contacts_SyncSource(sourceID string) error {
	if !b.gateEnabled() {
		return errors.New("contacts: extension disabled")
	}
	if b.deps.Core == nil {
		return errors.New("contacts: core not wired")
	}
	return b.deps.Core.Contacts().SyncSource(sourceID)
}

// Contacts_SyncAllSources triggers an immediate sync against every
// configured contact source. Used by the sidebar footer's Ctrl+Shift+A
// shortcut.
func (b *ContactsBridge) Contacts_SyncAllSources() error {
	if !b.gateEnabled() {
		return errors.New("contacts: extension disabled")
	}
	if b.deps.Core == nil {
		return errors.New("contacts: core not wired")
	}
	return b.deps.Core.Contacts().SyncAllSources()
}

// Contacts_UpdateContact applies a ContactPatch to a contact. Source
// dispatch handled inside the API:
//   - Local records → contact.Store.UpsertRecord (full-fidelity write)
//   - CardDAV records → server PUT gated on the source's writable flag
//
// 412 conflicts surface as a contacts:conflict event the UI listens for;
// the method returns nil on conflict (the user's edit was discarded but
// the local cache now matches the server, so the UI just reloads).
func (b *ContactsBridge) Contacts_UpdateContact(idOrEmail string, patch coreapi.ContactPatch) error {
	if !b.gateEnabled() {
		return nil
	}
	if err := b.ensureInit(); err != nil {
		return err
	}
	err := b.api.UpdateContact(idOrEmail, patch)
	if b.emitConflict(err) {
		return nil
	}
	return err
}

// Contacts_DeleteLocalContact removes a contact. Local records
// cascade-delete in the unified store; CardDAV records DELETE on the
// server (gated on writable) and then cascade locally. 412 conflicts
// surface via the contacts:conflict event. Idempotent on local + 404
// paths.
//
// Note: there's a separate top-level `App.DeleteContact` from pre-
// extension days for legacy callers. This one is gated to the extension's
// enabled state.
func (b *ContactsBridge) Contacts_DeleteLocalContact(idOrEmail string) error {
	if !b.gateEnabled() {
		return nil
	}
	if err := b.ensureInit(); err != nil {
		return err
	}
	err := b.api.DeleteContact(idOrEmail)
	if b.emitConflict(err) {
		return nil
	}
	return err
}

// ResizedContactPhoto is the return shape for Contacts_ResizeContactPhoto.
// Frontend destructures {data, mediaType}. Empty Data + empty MediaType
// is not used here (caller filters out empties); errors come back as Go
// errors. Defined here so the extension owns its types end-to-end.
type ResizedContactPhoto struct {
	Data      string `json:"data"`
	MediaType string `json:"mediaType"`
}

// Contacts_EnableWriteAccess runs the interactive OAuth flow to grant write
// access on a contact source, attaching the grant to a user-picked existing
// auth context (either a mail account or a standalone contact source).
//
// Synchronous: blocks until OAuth completes (success / cancel / error). On
// success: tokens are persisted under the picked identity, and the contact
// source's Writable flag flips. The frontend's WriteAccessAccountPicker
// dialog `await`s this.
//
// Inputs:
//   sourceID              — the contact source being granted write access
//   authContextKind       — "mail" or "standalone-contacts"
//   authContextIdentifier — account_id (for "mail") or source_id (for
//                           "standalone-contacts"); identifies the
//                           OAuth identity the new tokens attach to
//   expectedEmail         — the picked identity's email; enforced on
//                           OAuth callback (mismatch = reject)
//
// Aerion's design forbids creating new accounts from inside the contacts
// extension; all auth contexts MUST be one the user already set up in core
// (Mail account add OR standalone contacts source add).
func (b *ContactsBridge) Contacts_EnableWriteAccess(sourceID, authContextKind, authContextIdentifier, expectedEmail string) error {
	if !b.gateEnabled() {
		return nil
	}
	if b.deps.Core == nil {
		return errors.New("contacts: write-access flow unavailable (core not wired)")
	}
	if sourceID == "" {
		return errors.New("contacts: sourceID is required")
	}
	if authContextIdentifier == "" {
		return errors.New("contacts: authContextIdentifier is required")
	}
	if expectedEmail == "" {
		return errors.New("contacts: expectedEmail is required")
	}

	// Resolve the source's provider → clientConfigID + write scope.
	sources, err := b.deps.Core.Contacts().ListSources()
	if err != nil {
		return err
	}
	var providerType string
	for _, s := range sources {
		if s.ID == sourceID {
			providerType = s.Type
			break
		}
	}
	if providerType == "" {
		return fmt.Errorf("contacts: source %q not found", sourceID)
	}

	var clientConfigID coreapi.ClientConfigID
	var writeScope string
	switch providerType {
	case "google":
		clientConfigID = "google-contacts"
		writeScope = "https://www.googleapis.com/auth/contacts"
	case "microsoft":
		clientConfigID = "microsoft-contacts"
		writeScope = "https://graph.microsoft.com/Contacts.ReadWrite"
	default:
		return fmt.Errorf("contacts: source provider %q does not support write access", providerType)
	}

	req := coreapi.StartIncrementalConsentRequest{
		ClientConfigID: clientConfigID,
		Scopes:         []coreapi.AuthScope{{Resource: writeScope}},
		ExpectedEmail:  expectedEmail,
		LoginHint:      expectedEmail,
	}
	switch authContextKind {
	case "mail":
		req.AccountID = authContextIdentifier
	case "standalone-contacts":
		req.SourceID = authContextIdentifier
	default:
		return fmt.Errorf("contacts: unknown authContextKind %q", authContextKind)
	}

	if err := b.deps.Core.Auth().StartIncrementalConsent(req); err != nil {
		return err
	}

	return b.deps.Core.Contacts().SetSourceWritable(sourceID, true)
}

// Contacts_ResizeContactPhoto takes a base64-encoded image, decodes it,
// resizes to a max edge of 256px, and re-encodes as JPEG at quality 85.
// Used by the contacts Edit dialog after the frontend HTML file input
// hands over a picked image.
func (b *ContactsBridge) Contacts_ResizeContactPhoto(b64In string) (ResizedContactPhoto, error) {
	if !b.gateEnabled() {
		return ResizedContactPhoto{}, nil
	}
	if err := b.ensureInit(); err != nil {
		return ResizedContactPhoto{}, err
	}
	data, mediaType, err := b.api.ResizeContactPhoto(b64In)
	if err != nil {
		return ResizedContactPhoto{}, err
	}
	return ResizedContactPhoto{Data: data, MediaType: mediaType}, nil
}
