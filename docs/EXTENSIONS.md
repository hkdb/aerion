# Aerion Extension System

Developer reference for building first-party extensions on top of Aerion's core.

> **Status (today):** First-party extensions only — extensions ship compiled into the binary and are individually toggleable in Settings. The bridge architecture documented here is **designed so the project can accept third-party extensions via PR if community-extension demand emerges**, but no third-party-PR intake is open today and there's no commitment to open one. A full community-extension runtime (dynamic loading, sandboxing, manifest verification) is a separate, later possibility contingent on the same demand signal; see [§ Not yet implemented](#not-yet-implemented).

This doc is the contract every Aerion extension uses to interact with the host and with other extensions. Every claim is backed by a file path you can read directly — no second source of truth.

---

## Contents

1. [Overview](#overview)
2. [Architecture at a glance](#architecture-at-a-glance)
3. [Manifest + lifecycle](#manifest--lifecycle)
4. [`coreapi` reference](#coreapi-reference)
5. [Per-extension storage](#per-extension-storage)
6. [Auth Broker](#auth-broker)
7. [OAuth client configurations](#oauth-client-configurations)
8. [UI registration](#ui-registration)
9. [Account-setup hook contract](#account-setup-hook-contract)
10. [Lifecycle](#lifecycle)
11. [Settings keys](#settings-keys)
12. [Wails-bound surface](#wails-bound-surface)
13. [Testing conventions](#testing-conventions)
14. [Frontend conventions](#frontend-conventions)
15. [Extension UI Kit](#extension-ui-kit)
16. [Write capability](#write-capability)
17. [Contributing a new extension](#contributing-a-new-extension)
18. [Distribution model](#distribution-model)
19. [Not yet implemented](#not-yet-implemented)

---

## Overview

Aerion's extension system lets first-party extensions (Calendar, Contacts, Notes/Chat in the future) ship inside the same binary as Mail, while staying invisible to users who don't enable them. Design principles, in order of importance:

1. **Built-in, disabled by default.** Extensions compile into the binary but do nothing until enabled. Minimalists never see them.
2. **Per-extension SQLite isolation.** Each extension owns its own database file under `<dataDir>/extensions/<name>/data.db`. Extensions never query each other's tables — cross-extension data access goes through Go interfaces in `internal/core/api/v1`.
3. **Shared infrastructure stays shared.** One Wails process, one OAuth manager, one credential store, one IPC bus, one notification system. The extension system adds an additional **Auth Broker** layer so extensions never see access tokens or refresh tokens.
4. **Inline + detach pattern.** Every extension works inside the main window. Workflows can optionally pop out to a separate window via IPC (identical to the existing detached composer; not yet exercised by any extension in v0.3.x).
5. **Strict zero overhead when disabled.** Each extension contributes ONE `Bridge` struct allocation (~80 bytes) at App construction. The Bridge's per-extension SQLite, stores, and API wrapper are **lazy-initialized via `sync.Once` on the first enabled method call** — disabled extensions never open their database file, never construct stores, never allocate beyond the bridge stub. The only baseline cost is binary size + 80 bytes per bridge.

Full architectural rationale lives in [`context/EXTENSION_ARCHITECTURE.md`](../context/EXTENSION_ARCHITECTURE.md). This doc is the **developer reference**; that doc is the **design rationale**.

---

## Architecture at a glance

```
┌─────────────────────────────────────────────────────────────────────┐
│  Aerion process (single binary, single WebKit view)                 │
│                                                                     │
│  ┌────────────────────────┐    ┌──────────────────────────────┐    │
│  │  Core (always running) │    │  Extensions (toggleable)     │    │
│  │                        │    │                              │    │
│  │  internal/account/     │    │  extensions/                 │    │
│  │  internal/folder/      │    │   contacts/                  │    │
│  │  internal/message/     │    │     manifest.json            │    │
│  │  internal/draft/       │    │     manifest.go              │    │
│  │  internal/contact/     │    │     backend/                 │    │
│  │  internal/carddav/     │    │       register.go, api.go..  │    │
│  │  internal/imap/        │    │     frontend/                │    │
│  │  internal/smtp/        │    │       components/, stores/   │    │
│  │  internal/oauth2/      │    │   (future: calendar/)        │    │
│  │  internal/credentials/ │    │                              │    │
│  │  internal/settings/    │    │  internal/extensions/        │    │
│  │  internal/kit/davutil  │    │   ui/, auth/, mail/, ...     │    │
│  │  ...                   │    │   (host scaffolding)         │    │
│  └──────────┬─────────────┘    └──────────┬───────────────────┘    │
│             │                              │                        │
│             ▼                              ▼                        │
│         ┌───────────────────────────────────────────┐               │
│         │  internal/core/api/v1 — the contract      │               │
│         │  (Mail, Composer, Contacts, Auth, UI,     │               │
│         │   Storage, Notifications, EventBus, Core, │               │
│         │   Manifest, Extension)                    │               │
│         └───────────────────────────────────────────┘               │
└─────────────────────────────────────────────────────────────────────┘
```

**The rule**: `extensions/<name>/` for things users toggle on/off. `internal/extensions/` for host scaffolding that always runs (UI registry, Auth Broker, Mail/Composer wrappers, per-extension Store wiring).

**Where to put each piece of code:**

| New code is... | Goes in |
|---|---|
| Extension's Go backend (logic, API impl, lifecycle hooks) | `extensions/<name>/backend/` |
| **Extension's Wails-bound surface (Bridge struct + all bound methods)** | `extensions/<name>/backend/bridge.go` |
| Extension's manifest metadata | `extensions/<name>/manifest.json` + `manifest.go` (root, embeds JSON) |
| Extension's Svelte components | `extensions/<name>/frontend/components/` |
| Extension's Svelte stores | `extensions/<name>/frontend/stores/` |
| Extension's account-setup hook panel | `extensions/<name>/frontend/hooks/` |
| Extension's host-side wiring (one embed field + one constructor call) | `app/extension_<name>.go` — see below |
| A type or interface ALL extensions might consume | `internal/core/api/v1/` |
| Shared host-side scaffolding (registry, broker, wrappers) | `internal/extensions/` |
| **Generic backend utility extensions are allowed to import** | `internal/kit/<area>/` |
| Host-owned UI used by the rail/dialog (not extension-specific) | `frontend/src/lib/components/rail/`, etc. |

**`internal/kit/` — shared extension-facing building blocks.** Backend analog of the frontend `lib/components/kit/`. Modules under `internal/kit/*` are generic, extension-agnostic (no extension-specific naming or behavior) and may be imported directly from extension code — they're carved out from the otherwise blanket "extensions can't import `internal/*`" rule. Today's only member is [`internal/kit/davutil`](../internal/kit/davutil), which holds the `XMLFixTransport` and the `NewHTTPClient(timeout)` builder both `internal/carddav` and the calendar extension use to normalize WebDAV ETag / lastmodified server quirks. Add a new `internal/kit/<area>/` when two consumers need the same primitive and one of them is an extension.

**The Bridge pattern + the `app/` minimum**: Wails v2 binds methods on structs in the `Bind` list at `wails.Run` time, generating frontend bindings via Go reflection. Because Go's reflection enumerates methods on **embedded** types via standard method promotion, the host (App) can embed a `*Bridge` struct from each extension's package; the Bridge's methods then appear in the generated `App.d.ts` as if they were on App. The actual method definitions live in `extensions/<name>/backend/bridge.go`. The only host-side file (`app/extension_<name>.go`) is reduced to about 10 lines: importing the extension's package, declaring the embedded field on App, and one constructor call that wires the bridge's host-provided dependencies during Startup.

**Method naming — the `<Extension>_` prefix rule (HARD):** every Wails-bound bridge method MUST be named `<ExtensionName>_<MethodName>` (e.g., `Contacts_UpdateContact`, `Calendar_CreateEvent`). Embedded-method promotion happens in a single flat namespace on App; without the prefix, two extensions that both define `UpdateRecord()` would collide silently. The prefix is enforced by code review when accepting 3rd-party extension PRs — see [§ Contributing a new extension](#contributing-a-new-extension).

**Bridge struct type-name rule (HARD):** the Bridge struct itself MUST be named `<Name>Bridge` (e.g., `ContactsBridge`, `CalendarBridge`), not the generic `Bridge`. Go's anonymous-embed field name is derived from the type's last identifier, so two extensions that both name their struct `Bridge` would produce a `duplicate field` compile error when embedded on App. The struct name is enforced by code review alongside the method-prefix rule. Likewise, the constructor and deps-struct follow the same pattern: `NewContactsBridge(deps ContactsBridgeDeps) *ContactsBridge`.

Extensions DO NOT import from other extensions' Go packages. They go through `coreapi.Core.Extension(id)` (see [§ Core interface](#core-interface)).

---

## Manifest + lifecycle

Every first-party extension carries a `manifest.json` at its repo root and exposes a single Go object implementing `coreapi.Extension`. The host reads the manifest to build the Settings UI listing and calls `Register()` at startup to wire the extension's UI surfaces.

This shape is **subprocess-ready**: if community-extension demand emerges and a subprocess runtime is built (see [§ Distribution model](#distribution-model)), the same manifest fields and the same Register handshake move across the IPC boundary unchanged. Nothing in the manifest references Go-specific concepts (no module paths, no compiled-type names).

### Manifest schema

[`extensions/contacts/manifest.json`](../extensions/contacts/manifest.json) for the canonical example:

```json
{
  "id": "contacts",
  "name": "Contacts",
  "version": "0.1.0",
  "description": "Browse and edit contacts from your accounts (CardDAV, Google, Microsoft). Local-contact editing in v0.3.x; provider write capability rolling out incrementally.",
  "author": "Aerion",
  "minAerionVersion": "0.3.0",
  "capabilities": [
    "contacts.read",
    "contacts.write",
    "ui.rail-tab",
    "ui.account-setup-hook",
    "ui.settings-tab"
  ],
  "oauth": {
    "first_party_uses_core_for_scopes": [
      "https://www.googleapis.com/auth/contacts.readonly",
      "Contacts.Read",
      "Contacts.ReadBasic"
    ]
  }
}
```

| Field | Purpose |
|---|---|
| `id` | Canonical extension id. Must match the key used by `settings.AllExtensionKeys` and Settings flag (`extension_<id>_enabled`). |
| `name` | User-facing display name (Settings UI, rail-tab tooltip). |
| `version` | Semver. Surfaced in Settings → Extensions. |
| `description` | 1–2 sentence summary shown in the Settings listing. |
| `author` | Display name only. No URL. |
| `minAerionVersion` | Semver. Future host versions will refuse to load an extension whose minAerionVersion is higher than the running build. |
| `capabilities` | Coarse capability strings the extension declares. See [coreapi.Capability](../internal/core/api/v1/manifest.go) for the known set (e.g., `contacts.read`, `contacts.write`, `ui.rail-tab`, `ui.settings-tab`). Unknown strings are treated as opaque so the set can grow without breaking older hosts. |
| `oauth.first_party_uses_core_for_scopes` | Optional. Lists OAuth scopes that should route through Aerion core's mail OAuth (reusing the user's existing consent) instead of the extension's own client config. See [§ Manifest OAuth routing](#manifest-oauth-routing--first_party_uses_core_for_scopes). First-party only. |

### Loading the manifest into Go

Place `manifest.json` at the extension root and a tiny `manifest.go` next to it that embeds the JSON:

```go
// extensions/contacts/manifest.go
package contacts

import (
    _ "embed"
    "encoding/json"
    coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

//go:embed manifest.json
var manifestJSON []byte

func Manifest() coreapi.Manifest {
    var m coreapi.Manifest
    if err := json.Unmarshal(manifestJSON, &m); err != nil {
        panic("contacts: manifest.json is malformed (build-time bug): " + err.Error())
    }
    return m
}
```

Why a separate root-level package for the manifest: Go's `//go:embed` directive can't traverse upward (no `../manifest.json`). Keeping the embed in the root-level package lets the backend implementation in `backend/` import the manifest data cleanly while keeping the manifest file semantically at the extension root.

### Extension lifecycle

[`internal/core/api/v1/manifest.go`](../internal/core/api/v1/manifest.go):

```go
type Extension interface {
    Manifest() Manifest
    Register(core Core) (Unregister, error)
}
```

**`Register` is called once per process at startup, regardless of whether the extension is currently enabled.** This matches the architecture-doc rule that descriptive UI registrations (rail tab, account-setup hook) persist across enable/disable cycles. Active behaviors (sync schedulers, background work) are gated separately by `IsExtensionEnabled` checks; they are NOT skipped at Register time.

The returned `Unregister` removes everything Register wired. Called by the host on process shutdown.

### Example: Contacts extension

The Contacts extension splits into TWO Go types in `extensions/contacts/backend/`:

**1. `Extension` — lifecycle handle ([`extensions/contacts/backend/register.go`](../extensions/contacts/backend/register.go))**. Tiny on purpose: manifest + the `Register` handshake. No stores. No API. Allocating one costs a manifest copy.

```go
type Extension struct {
    manifest coreapi.Manifest
}

func NewExtension() *Extension {
    return &Extension{manifest: contacts.Manifest()}
}

func (e *Extension) Manifest() coreapi.Manifest { return e.manifest }

func (e *Extension) Register(core coreapi.Core) (coreapi.Unregister, error) {
    unregRail, err := core.UI().RegisterRailTab(coreapi.RailTabRequest{
        ExtensionID: e.manifest.ID,
        Label:       e.manifest.Name,
        Icon:        "mdi:account-multiple",
        Component:   "ContactsPane",
        Order:       10,
    })
    if err != nil { return nil, err }

    unregHook, err := core.UI().RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{...})
    if err != nil { unregRail(); return nil, err }

    return func() { unregHook(); unregRail() }, nil
}
```

**2. `ContactsBridge` — the Wails-bound surface ([`extensions/contacts/backend/bridge.go`](../extensions/contacts/backend/bridge.go))**. Holds host dependencies, all `Contacts_`-prefixed Wails methods, and a `sync.Once`-gated lazy initializer for the extension's `*Store` + `*API`:

```go
type ContactsBridgeDeps struct {
    SettingsStore SettingsStore        // for the enabled-flag gate
    Paths         *platform.Paths      // for the extension's SQLite dir
    DB            *database.DB         // shared writable DB handle for local contacts
    Emitter       EventEmitter         // for runtime.EventsEmit (kept generic — no Wails import in extensions/)
    Core          coreapi.Core         // host coreapi handle — used for Contacts().ListSources()/LinkAccountSource() (source management) AND Storage().HostSecrets() (read-only access to core-managed CardDAV passwords, per Pattern B in the Storage section below)
}

// Type-name rule: each extension MUST name its Bridge struct `<Name>Bridge`
// (here: `ContactsBridge`), not the generic `Bridge` — see the Bridge struct
// type-name rule above. Same goes for the deps struct and constructor.
type ContactsBridge struct {
    deps     ContactsBridgeDeps
    initOnce sync.Once
    initErr  error
    api      *API
}

func NewContactsBridge(deps ContactsBridgeDeps) *ContactsBridge {
    return &ContactsBridge{deps: deps}
}

// Gate every bound method. Disabled = empty results, never errors.
func (b *Bridge) gateEnabled() bool {
    on, _ := b.deps.SettingsStore.IsExtensionEnabled("contacts")
    return on
}

// sync.Once — first enabled call opens the SQLite file, applies migrations,
// constructs Store + API. Subsequent calls hit the live API directly.
func (b *Bridge) ensureInit() error {
    b.initOnce.Do(func() { /* open store, build API, capture initErr */ })
    return b.initErr
}

// All bound methods follow the same shape.
// Contacts_ prefix is mandatory — embedded promotion shares one App namespace.
func (b *Bridge) Contacts_ListContactsForBrowse(query, sourceID string, limit, offset int) ([]coreapi.Contact, error) {
    if !b.gateEnabled() { return nil, nil }
    if err := b.ensureInit(); err != nil { return nil, err }
    return b.api.ListContacts(query, sourceID, limit, offset)
}
// ... Contacts_GetContactDetail, Contacts_CreateContact, Contacts_UpdateContact,
//     Contacts_DeleteLocalContact, Contacts_ResizeContactPhoto,
//     Contacts_ListAddressbooks, Contacts_ListSources, Contacts_LinkAccountSource ...
```

### Host-side startup

`app/app.go` embeds the bridge **directly on the App struct**. Go's standard embedded-field method promotion exposes every `Contacts_*` method as if it were on App, so Wails reflection picks them up at `wails.Run` time and emits them into `frontend/wailsjs/go/app/App.{js,d.ts}`. No `Bind` list edit required, no per-method adapter.

```go
// app/app.go
type App struct {
    *extcontactsbe.Bridge   // embedded — promotes Contacts_* methods to App
    // ... existing App fields ...
}

// app/app.go Startup:
a.initContactsExtension()  // wires bridge dependencies
a.contactsExt = extcontactsbe.NewExtension()
a.knownExtensions = []coreapi.Extension{a.contactsExt}
// ... iterate and call Register on each, as before ...
```

The wiring file ([`app/extension_contacts.go`](../app/extension_contacts.go), ~28 LOC total) is the entire host-side cost of the Contacts extension:

```go
package app

import (
    extcontactsbe "github.com/hkdb/aerion/extensions/contacts/backend"
    wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) initContactsExtension() {
    a.ContactsBridge = extcontactsbe.NewContactsBridge(extcontactsbe.ContactsBridgeDeps{
        SettingsStore: a.settingsStore,
        Paths:         a.paths,
        DB:            a.db,
        Emitter: func(eventName string, payload any) {
            wailsRuntime.EventsEmit(a.ctx, eventName, payload)
        },
    })
}
```

When you add a new first-party extension, the host side is similarly thin: embed the extension's `*Bridge` on App, add an `init<Name>Extension()` helper, append the lifecycle `Extension` to `a.knownExtensions`. Everything else (Settings UI listing, rail rendering, hook discovery, Wails bindings generation) flows automatically.

### Example: Calendar extension

The Calendar extension is the second canonical extension and exercises every Phase 1 capability the SDK exposes. Where Contacts focuses on a single domain (per-source contact CRUD wrapped around the host's `coreapi.Contacts` surface), Calendar is **fully self-contained at the data layer** — it owns its per-extension SQLite with five tables, runs its own CalDAV sync engine, parses iCal events / RRULE expansions / VALARM blocks, and schedules desktop notifications via `coreapi.Notifications`. Use it as the reference when your extension needs to do more than wrap an existing host surface.

**Package layout** ([`extensions/calendar/`](../extensions/calendar)):

```
extensions/calendar/
├── manifest.go            // wraps embedded manifest.json
├── manifest.json          // capabilities + reserved OAuth slots
├── creds.go               // ldflag-injected GoogleClientID/Secret etc. (Phase 2)
├── register.go            // RegisterRailTab + RegisterSettingsTab
└── backend/
    ├── bridge.go          // CalendarBridge + Calendar_* Wails methods
    ├── api.go             // extension-local API (source CRUD, sync interval validation)
    ├── store.go           // per-extension SQLite + migrations v1–v4
    ├── caldav.go          // emersion/go-webdav/caldav wrapper + handwritten backfills
    ├── sync.go            // per-source poll scheduler, CTag-based incremental sync
    ├── ical_convert.go    // VEVENT ↔ internal Event (emersion/go-ical)
    ├── rrule_expand.go    // Component.RecurrenceSet → rrule.Set.Between() expansion
    ├── alarm.go           // VALARM parser + per-instance trigger computation
    ├── alarm_scheduler.go // time.AfterFunc scheduler subscribed to system:wake
    └── xmlfix.go          // inline-duplicated XML normalizer for buggy CalDAV servers
```

**Manifest** ([`extensions/calendar/manifest.json`](../extensions/calendar/manifest.json)) declares two capabilities and reserves Phase 2 OAuth slots so a future Google/Microsoft Calendar build doesn't need a schema change:

```json
{
  "id": "calendar",
  "name": "Calendar",
  "capabilities": ["ui.rail-tab", "ui.settings-tab"],
  "oauth_slots": [
    { "id": "google-calendar", "provider": "google", "scopes": ["https://www.googleapis.com/auth/calendar"] },
    { "id": "microsoft-calendar", "provider": "microsoft", "scopes": ["Calendars.ReadWrite"] }
  ]
}
```

**Per-extension SQLite** — Calendar owns five tables across four migrations:

| Migration | Tables | Purpose |
|---|---|---|
| v1 | `meta` | Generic key/value store. Currently holds the user's display-timezone choice; future settings extend here. |
| v2 | `calendar_sources`, `calendars` | CalDAV source rows + per-calendar visibility + color overrides. |
| v3 | `events`, `event_recurrence_overrides`, `sync_log` | Materialized events from sync; RECURRENCE-ID exception blobs; per-sync audit trail. |
| v4 | `event_alarms` | One row per (occurrence × VALARM) within the scheduler's 24h horizon; `INSERT OR IGNORE` unique index makes re-evaluation after sync idempotent. |

This is **distinct from Contacts**, which uses the host's `coreapi.Contacts` surface and only adds a thin local-store layer. Calendar wraps no host data — it owns everything from sync to expansion to alarms.

**Bridge + lazy init** ([`extensions/calendar/backend/bridge.go`](../extensions/calendar/backend/bridge.go)) follows the same `<Name>Bridge` + `sync.Once` pattern Contacts uses, but the `ensureInit` body wires **three** subsystems instead of one:

```go
b.initOnce.Do(func() {
    store, err := backend.OpenStore(b.deps.Paths.DataDir, "calendar", migrations)
    if err != nil { b.initErr = err; return }

    secrets := b.deps.Core.Storage().Secrets("calendar")
    b.api = NewAPI(store, secrets)
    b.syncer = NewSyncer(store, secrets, b.deps.Core.Events(), b.deps.SettingsStore)
    b.syncer.Start()
    b.alarms = NewAlarmScheduler(store, b.deps.Core.Notifications(), b.deps.Core.Events())
    b.alarms.Start(context.Background())
})
```

The Syncer subscribes to `system:wake` and `system:network-online` for immediate resync; the AlarmScheduler subscribes to `calendar:sync-complete` (re-evaluates alarms after each sync writes new VALARM rows) and `system:wake` (sweeps past alarms to `fired` status without firing-after-the-fact, re-arms future ones). Both subscriptions go through `coreapi.EventBus`; the extension never imports `internal/platform`.

**VALARM notifications via `coreapi.Notifications`** — at sync time, the upsert transaction extracts VALARM templates from each event's stored ICSBlob, projects them onto the expanded recurrence instances within a 7-day window, and writes one row per `(event_id, instance_unix, trigger_unix)` triple. The scheduler reads pending rows in `[now, now+24h]`, arms `time.AfterFunc` callbacks, and fires desktop notifications via the published `coreapi.Notifications` surface:

```go
s.notif.Show(coreapi.NotifyRequest{
    Title: ev.Summary,
    Body:  formatAlarmBody(*ev, a, time.Unix(a.InstanceUnix, 0)),
    OnClick: coreapi.NotifyClickAction{
        Kind:        "open-extension",
        ExtensionID: "calendar",
        Path:        "/event/" + ev.ID,
    },
})
```

Clicking the notification raises the Aerion window, switches the rail to Calendar via the host's generic `extension:open` Wails event, and the calendar pane drains the `/event/<id>` path on mount via a small kit-level pending-deep-link buffer ([`frontend/src/lib/stores/extensionDeepLink.svelte.ts`](../frontend/src/lib/stores/extensionDeepLink.svelte.ts)) — solving the mount-gap problem where the target pane isn't mounted yet when the Wails event fires.

**Frontend patterns Calendar pioneered** that future extensions can borrow from:

- **Tz-aware UI** via `date-fns-tz` + a calendar-scoped helper module ([`extensions/calendar/frontend/lib/tzMath.ts`](../extensions/calendar/frontend/lib/tzMath.ts)) wrapping `toZonedTime`/`fromZonedTime`. Every date-math helper in the view store reads the user's chosen tz at call time so changes propagate. See the [Per-extension npm deps convention](#vite--tsconfig-aliases) note for how the extension declares its dep without touching the host config files structurally.
- **Searchable IANA timezone picker** via `Intl.supportedValuesOf('timeZone')` + `Intl.DateTimeFormat({ timeZone })` for formatters. localStorage persistence behind a `calendarSettings` store with the API shape ready to swap for a Wails-backed Store when extensions get richer settings infrastructure.
- **Greenfield kit primitives Calendar consumed first**: `DetailOverlay` (right-side focus-mode panel — the first kit primitive with no mail equivalent, sanctioned by R25), `SidebarAddItem` (in-list "+ Add …" button), `ColorPicker` (palette + hex input pass-through). All three are calendar-driven additions to the kit; future extensions can adopt them.

**Settings dialog** ([`extensions/calendar/frontend/components/CalendarSettingsDialog.svelte`](../extensions/calendar/frontend/components/CalendarSettingsDialog.svelte)) follows the same `bits-ui` Dialog pattern Contacts uses but adds a per-source `Select`-based sync-interval picker, a Sync Now button with optimistic UI, and a `ConfirmDialog`-driven Delete flow. Add CalDAV source uses the existing 2-stage AddCalDAVSourceDialog (color picker stage 2 was added during the per-calendar color work).

The wiring file on the host side stays minimal — same shape as `app/extension_contacts.go`, ~30 LOC, just constructs the bridge with its BridgeDeps:

```go
// app/extension_calendar.go
func (a *App) initCalendarExtension() {
    a.CalendarBridge = calendarbackend.NewCalendarBridge(calendarbackend.CalendarBridgeDeps{
        SettingsStore: a.settingsStore,
        Paths:         a.paths,
        Core:          newCoreImpl(a, "calendar"),
        // ... + the EventEmitter closure + OAuth provider registrations ...
    })
}
```

When your extension needs more than wrapping a host surface — own data sovereignty, custom sync protocols, scheduled background work, desktop-level integrations — Calendar is the shape to copy.

---

## `coreapi` reference

Package: `github.com/hkdb/aerion/internal/core/api/v1` (12 files; entirely interface + type declarations, no logic).

The full surface is defined in [`internal/core/api/v1/`](../internal/core/api/v1). Every extension method receives a `coreapi.Core` (see [§ Core interface](#core-interface)) at initialization. From there it grabs the surfaces it needs.

This is the complete list of APIs your extension is allowed to consume. Anything not on this list is off-limits — see [§ The two hard rules for any new extension](#the-two-hard-rules-for-any-new-extension). If a surface you need isn't here (or is here but returns `ErrUnimplemented`), open a Feature Request issue — see [§ Requesting a new extension API](#requesting-a-new-extension-api).

### Status at a glance

✅ usable today  ·  ⚠️ partial (some methods return `ErrUnimplemented`)  ·  🚧 interface only (every method returns `ErrUnimplemented`)

| Surface | Status | What works today | Notes |
|---|---|---|---|
| `core.Mail()` | ⚠️ | `ListMessages`, `GetMessage`, `ListFolders`, `GetSpecialFolder` | Mutators (`MoveMessage`, `Archive`, `Trash`, `SetFlags`, `AppendMessage`) and `SubscribeToMailEvents` return `ErrUnimplemented`. |
| `core.Composer()` | ⚠️ | `OpenComposer` (mailto URL form) | `Attachments` and `ReplyTo` in `ComposeRequest` return `ErrUnimplemented`. |
| `core.Contacts()` | ✅ | `ListSources`, `LinkAccountSource`, `ListAddressbooks`, `SetSourceWritable`, `SearchContacts`; `ContactSource.AccountID` field surfaced | Source-management surface used by the Contacts extension itself + the first read-side cross-extension method (`SearchContacts`). `SearchContacts` was wired in v0.3.0 to back the Calendar extension's attendee-picker autocomplete — see [§ Cross-extension consumption (Search example)](#cross-extension-consumption-search-example) below. Remaining contact CRUD methods (`GetContact`/`ListContacts`/`Create`/`Update`/`Delete`/`ListAddressbooks`) still return `ErrUnimplemented` — no cross-extension consumer queries those yet, and routing them through coreImpl would force the Contacts extension to initialize even when disabled. They get wired in when a real consumer arrives. |
| `core.Auth()` | ✅ | `HTTPClient(accountID, scopes)` — bearer + transparent refresh; `StartIncrementalConsent(req StartIncrementalConsentRequest)` — synchronous OAuth consent flow that persists tokens against either an account or a standalone contacts source (see [§ Write-access grant flow](#write-access-grant-flow-account-picker-model)) | `IMAPClient` and `SMTPClient` return `ErrUnimplemented`. |
| `core.UI()` | ⚠️ | `RegisterRailTab`, `RegisterAccountSetupHook`, `OpenURL` | `RegisterSettingsTab`, `RegisterContextMenuItem`, `RegisterInboxView` accept registrations but no consumer reads them yet. `OpenURL` opens URLs in the system browser via the host's hardened resolver (protocol allowlist, Linux portal-first). |
| `core.Storage()` | ⚠️ | `Secrets(extensionID)` — keyring-first with AES-encrypted DB fallback | `KV(extensionID)` still returns `stubKV` (all methods `ErrUnimplemented`) — wires up when a real consumer arrives. Per-extension SQLite (your own `*sql.DB`) is the parallel persistence path — see [§ Per-extension storage](#per-extension-storage). |
| `core.Notifications()` | ✅ | `Show(NotifyRequest)` — dispatches to the host's platform notifier; click routing supports `open-extension` (raises window + emits Wails `extension:open`) | Calendar's VALARM scheduler is the first concrete consumer. |
| `core.Events()` | ✅ | `Publish` (fan-out to Go subscribers + Wails frontend) + `Subscribe` (in-process Go handlers); host publishes `system:wake` and `system:network-online` for sleep/wake + network state | Lazy singleton via `sync.Once` — disabled-only configs pay nothing. Calendar consumes both system events. |
| `core.Extension(id)` | 🚧 | Returns `(nil, false)` always | Typed cross-extension handles not wired yet. |

Each subsection below documents the interface signatures + behavior in detail.

### Cross-extension consumption (Search example)

The canonical pattern for one extension to consume another's data is a
**Wails wrapper method on the consuming extension's bridge that delegates
through `coreapi`**. Consuming extensions DO NOT import the producing
extension's Go package, and the frontend DOES NOT call the producing
extension's `<Producer>_*` bridge methods directly.

Concrete example (landed for v0.3.0): the Calendar extension needs
contact-autocomplete suggestions in its attendee picker. Mail's composer
already calls `Contacts_SearchContacts` via the Contacts extension's
bridge — but Calendar can't (cleanly) reach into Contacts' bridge from
its own frontend. The pattern:

1. **Coreapi method**: `coreapi.Contacts.SearchContacts(query, limit)
   ([]Contact, error)` is defined on the contacts interface in
   `internal/core/api/v1/types.go`.
2. **Host implementation**: `app/coreimpl.go`'s `contactsCoreImpl.SearchContacts`
   delegates to the same host `App.SearchContacts` that the
   `Contacts_SearchContacts` bridge method uses. Same backend
   function, just behind the typed interface.
3. **Consumer-side bridge wrapper**: Calendar's bridge exposes
   `Calendar_SearchContacts(query, limit)` (a ~10-line wrapper at
   `extensions/calendar/backend/bridge.go::Calendar_SearchContacts`)
   that calls `b.deps.Core.Contacts().SearchContacts(...)`. Wails
   generates a TypeScript binding under the calendar namespace, which
   Calendar's frontend (`AttendeeInput.svelte`) imports directly.

The result: Calendar has no hard dependency on Contacts being enabled
(coreapi returns a no-op when Contacts is disabled), Mail's composer
still works unchanged, and the `<Extension>_` prefix rule keeps the
Wails namespace collision-free. The same pattern applies to any future
cross-extension consumption — extend the relevant `coreapi.<Producer>`
interface, implement it in `coreimpl`, expose a wrapper in the
consumer's bridge.

### Self-match via identities (RSVP example)

Several extension flows need to know "did the current user perform this
action / appear in this list?" — e.g., calendar's RSVP buttons on
EventDetail only appear when the user is one of the event's attendees.
The canonical pattern is **identity union match**:

1. Frontend gathers the lowercase union of `Account.Email +
   Identity.Email` across every account from `accountStore`.
2. Frontend passes that union into the consuming Wails method as a
   `[]string` parameter (e.g., `Calendar_UpdateMyAttendeeStatus(eventId,
   selfEmails, partStat)`).
3. Backend probes its data with that set as a `map[string]struct{}` for
   O(1) lookup against each candidate row.

This keeps account/identity state out of the extension's own database
(no duplication, no cache-staleness) while letting the extension still
make per-user decisions. First wired examples:

- `Calendar_UpdateMyAttendeeStatus` at
  `extensions/calendar/backend/bridge.go` (delegates to
  `api.UpdateMyAttendeeStatus` at
  `extensions/calendar/backend/api_attendees.go`) — RSVP self-match.
- `Calendar_QueryFreeBusy` at the same bridge (delegates to
  `api.QueryFreeBusyForAttendees` →
  `freebusy_aggregator.go::QueryAggregatedFreeBusy`) — routes self
  emails to a local DB scan (no remote query) and non-self emails to
  Google `freeBusy.query` or Microsoft `getSchedule` via fan-out.

### Stability

The current `coreapi` surface is the one extensions should code against. **Non-breaking additions** (new methods on existing interfaces with sensible defaults, new event types, new fields on request structs with zero values) may land between minor releases. **Breaking changes** are avoided wherever possible; when they're truly necessary, they ship with migration notes and a deprecation period rather than a silent rename.

Aerion is still pre-1.0 — the surface may continue to evolve as more first-party extensions surface their needs.

### `Core` interface

[`internal/core/api/v1/core.go`](../internal/core/api/v1/core.go)

```go
type Core interface {
    Mail() Mail
    Composer() Composer
    Contacts() Contacts
    Auth() Auth
    Notifications() Notifications
    UI() UI
    Storage() Storage
    Events() EventBus

    // Extension returns the typed handle published by another extension via
    // its api.go interface, or (nil, false) if the extension is not enabled
    // or has not published a typed API.
    Extension(id string) (any, bool)
}
```

Extensions call `core.Mail().ListMessages(...)`, `core.Auth().HTTPClient(...)`, etc. For Phase 1 first-party extensions, all capabilities are implicitly granted; surfaces an extension isn't using simply aren't called.

### `Mail`

[`internal/core/api/v1/mail.go`](../internal/core/api/v1/mail.go)

```go
type Mail interface {
    // Read
    ListMessages(filter MessageFilter) ([]Message, error)
    GetMessage(id string, includeBody bool) (*Message, error)
    ListFolders(accountID string) ([]Folder, error)
    GetSpecialFolder(accountID string, kind FolderKind) (*Folder, error)

    // Mutate — Phase 1 returns ErrUnimplemented; Phase 2+ wires through
    // app/actions.go so undo/sync/events fire identically to user actions.
    MoveMessage(id, destFolderID string) error
    Archive(id string) error
    Trash(id string) error
    SetFlags(id string, flags Flags) error
    AppendMessage(accountID, folderID string, raw []byte, flags Flags) error

    // Events
    SubscribeToMailEvents(types []MailEventType) (<-chan MailEvent, Unsubscribe, error)
}
```

Concrete impl: [`internal/extensions/mail/api.go`](../internal/extensions/mail/api.go). Read methods are wired. Mutators + `SubscribeToMailEvents` return `coreapi.ErrUnimplemented` in v0.3.0.

### `Composer`

[`internal/core/api/v1/compose.go`](../internal/core/api/v1/compose.go)

```go
type Composer interface {
    OpenComposer(req ComposeRequest) error
}
```

Phase 1 impl ([`internal/extensions/compose/api.go`](../internal/extensions/compose/api.go)) builds an RFC 6068 mailto URL from the request and delegates to the host's existing `OpenComposerWindow`. Attachments and `ReplyTo` are deferred (`ErrUnimplemented`) because they need composer-state integration beyond mailto.

### `Contacts`

[`internal/core/api/v1/contacts.go`](../internal/core/api/v1/contacts.go)

```go
type Contacts interface {
    // Contact CRUD (consumed by the Contacts extension's own Bridge)
    SearchContacts(query string, limit int) ([]Contact, error)
    GetContact(emailOrID string) (*Contact, error)
    ListContacts(filter ContactFilter) ([]Contact, error)
    ListAddressbooks(sourceID string) ([]Addressbook, error)
    CreateContact(input ContactCreateInput) (id string, err error)
    UpdateContact(id string, patch ContactPatch) error
    DeleteContact(id string) error

    // Source management (host-implemented in app/coreimpl.go; available to
    // cross-extension consumers + used by the Contacts extension's bridge to
    // drive the sidebar source list + account-setup hook).
    ListSources() ([]ContactSource, error)
    LinkAccountSource(accountID, name string, syncInterval int) (string, error)

    // SetSourceWritable flips a contact source's writable flag. Used by the
    // Phase 2b.3 incremental-consent flow to enable write access after a
    // user grants the OAuth write scope; CardDAV sources also use it as a
    // pure flag flip via the extension settings UI.
    SetSourceWritable(sourceID string, writable bool) error

    // Events (Phase 3+, when a core event bus exists)
    SubscribeToContactEvents(types []ContactEventType) (<-chan ContactEvent, Unsubscribe, error)
}

// ContactSource is the API-surface descriptor for a configured contact
// source. AccountID (added in Phase 2b.3) carries the linked email account
// id, when the source was created via LinkAccountSource. Standalone CardDAV
// / contacts-only OAuth sources have AccountID == "".
type ContactSource struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Type      string `json:"type"`              // "carddav" | "google" | "microsoft"
    Writable  bool   `json:"writable"`
    AccountID string `json:"accountId,omitempty"`
}

// Full multi-field patch shape (2b.2.b.2). Pointer fields distinguish
// "leave unchanged" (nil) from "set to empty"; pointer-to-slice for
// multi-value fields preserves the same three states.
type ContactPatch struct {
    Name       *string           `json:"name,omitempty"`
    Nickname   *string           `json:"nickname,omitempty"`
    Org        *string           `json:"org,omitempty"`
    Title      *string           `json:"title,omitempty"`
    Note       *string           `json:"note,omitempty"`
    Bday       *string           `json:"bday,omitempty"`
    Emails     *[]ContactEmail   `json:"emails,omitempty"`
    Phones     *[]ContactPhone   `json:"phones,omitempty"`
    Addresses  *[]ContactAddress `json:"addresses,omitempty"`
    URLs       *[]ContactURL     `json:"urls,omitempty"`
    IMPPs      *[]ContactIMPP    `json:"impps,omitempty"`
    Categories *[]string         `json:"categories,omitempty"`
    Photo      *ContactPhoto     `json:"photo,omitempty"`
}
```

**Split implementation** — two backends sit behind this interface depending on the method:

- **Contact CRUD** (`Search`/`Get`/`List`/`ListAddressbooks`/`Create`/`Update`/`Delete`): implemented in [`extensions/contacts/backend/api.go`](../extensions/contacts/backend/api.go) and exposed via the Contacts extension's Bridge. `Search`/`Get`/`List` wrap `contact.Store` + `carddav.Store`. `Create`/`Update`/`Delete` source-dispatch by `carddav.Source.Type`:
  - **Local** (`SourceID == "local"` / `"local:manual"`) → `contact.Store` directly.
  - **CardDAV** → `extensions/contacts/backend/api.go writeCardDAVRecord` / `deleteCardDAVRecord` (server PUT/DELETE with basic-auth + `If-None-Match: *` on create).
  - **Google** → [`extensions/contacts/backend/google_api.go`](../extensions/contacts/backend/google_api.go) (Phase 2b.3 Track B). Uses the People API via [`extensions/contacts/backend/google_write.go`](../extensions/contacts/backend/google_write.go); `recordToGooglePerson` mapping in [`extensions/contacts/backend/google_convert.go`](../extensions/contacts/backend/google_convert.go). ETag stored per-record in the extension's SQLite (`oauth_record_state` table) and stamped at `metadata.sources[0].etag` on PATCH. 412/`failedPrecondition` becomes `*coreapi.ErrConflict` → `contacts:conflict` Wails event.
  - **Microsoft** → [`extensions/contacts/backend/microsoft_api.go`](../extensions/contacts/backend/microsoft_api.go) (Phase 2b.3 Track C). Uses Graph via [`extensions/contacts/backend/microsoft_write.go`](../extensions/contacts/backend/microsoft_write.go); `recordToMicrosoftContact` in [`extensions/contacts/backend/microsoft_convert.go`](../extensions/contacts/backend/microsoft_convert.go). Effectively last-writer-wins (Graph contacts don't strictly enforce `If-Match`); etag stored for telemetry only. Multi-URL records collapse to `businessHomePage` (single field on Graph) with a log warn — documented lossy mapping.
  - `SubscribeToContactEvents` returns `ErrUnimplemented` until a core event bus exists.
- **Source management** (`ListSources`/`LinkAccountSource`/`SetSourceWritable`): implemented in [`app/coreimpl.go`](../app/coreimpl.go) `contactsCoreImpl`, wrapping `app.carddavStore.ListSources()` + the existing `App.LinkAccountContactSource` + `carddavStore.SetSourceWritable`. These live host-side because `contact_sources` is a host-owned table (mail's autocomplete also reads it). The Contacts extension's bridge proxies through `b.deps.Core.Contacts().*`.
- The `Get`/`List`/`Create`/`Update`/`Delete` methods on `app/coreimpl.go contactsCoreImpl` are intentionally `ErrUnimplemented`: routing them through coreImpl would force the Contacts extension's stores to initialize even when disabled, breaking the lightweight invariant. They get filled in when a cross-extension consumer actually needs them.
- **`SearchContacts` IS wired** (v0.3.0) — delegates to `App.SearchContacts`, the same backend mail's composer hits via the `Contacts_SearchContacts` bridge method. The host-level search doesn't touch the Contacts extension's own state (it queries the shared `contact.Store` + `contact_sources`-derived OAuth providers directly), so it's lightweight-safe. First cross-extension consumer: the Calendar extension's `Calendar_SearchContacts` bridge wrapper (see [§ Cross-extension consumption](#cross-extension-consumption-search-example)).

**Addressbook synthetic IDs (Phase 2b.3)**:

`ListAddressbooks` returns synthetic IDs for OAuth sources so the Add Contact dialog can target a specific group/folder without exposing remote ids directly to the UI:

| Source type | Addressbook ID format | Maps to |
|---|---|---|
| CardDAV | `<addressbook UUID>` | Row in `carddav_source_addressbooks` (local mirror table). |
| Google — My Contacts | `google-mycontacts:<sourceID>` | Default destination; no `ModifyGroupMembership` call. |
| Google — specific group | `google-group:<contactGroupResourceName>` | POST + then `POST .../{groupResourceName}/members:modify` to add the new contact. |
| Microsoft — default folder | `ms-default:<sourceID>` | POST `/me/contacts`. |
| Microsoft — specific folder | `ms-folder:<folderID>` | POST `/me/contactFolders/{folderID}/contacts`. |

`parseAddressbookGroupID` / `parseAddressbookFolderID` (in `google_convert.go` / `microsoft_convert.go`) parse these back to remote IDs at write time.

**`ContactFilter.SourceID` conventions:**

| Value | Behavior |
|---|---|
| `""` (empty) | Merged listing — when `Query` is set, calls `contact.Store.Search` (local + vCard + CardDAV merged + ranked). When `Query` is empty, falls back to local-only list. |
| `"local"` | All local contacts (manual + collected). |
| `"local:manual"` | User-added local contacts (Add Contact UI). Also the canonical target for `CreateContact` when SourceID is "" or "local". |
| `"local:collected"` | Auto-collected from sent-mail recipients. Read-only as a *create target* (the `collected` kind is reserved for the email-collection process to assign); `UpdateContact`/`DeleteContact` work fine. |
| `<carddav source UUID>` | Contacts from a specific CardDAV source. Reads use `carddav.Store.ListRecordIDsForSource`; writes PUT/DELETE the source's WebDAV. |

**`GetContact` / `UpdateContact` / `DeleteContact` argument:** if the id contains `@`, treated as an email and routed to the local store; otherwise treated as a record UUID (works for both local and CardDAV records). `GetContact` calls `enrichCardDAVSourceID` to rewrite the literal `"carddav"` string from `fromRecord` into the actual sidebar source UUID, so the frontend's writability gate finds the source row. As of Phase 2b.3, Write methods on Google AND Microsoft sources are fully wired (CardDAV since 2b.2). `GetContact` returns `(nil, nil)` when not found — never an error for missing. `ContactPatch` with all-nil pointers is a no-op success.

### `Auth`

[`internal/core/api/v1/auth.go`](../internal/core/api/v1/auth.go)

```go
type Auth interface {
    HTTPClient(accountID string, scopes []AuthScope) (*http.Client, error)
    IMAPClient(accountID string, requiredCaps []string) (IMAPClient, error)
    SMTPClient(accountID string) (SMTPClient, error)

    // StartIncrementalConsent runs an interactive OAuth consent flow
    // (synchronous; opens browser, blocks on callback, persists tokens
    // against either an account or a standalone contacts source).
    // Returns nil on grant or a wrapped error on user-cancel / callback
    // failure / wrong-account mismatch. Used by extensions whose write
    // paths hit ErrAdditionalConsentRequired from HTTPClient — they call
    // this to upgrade the user's grant before retrying the write.
    //
    // Exactly one of req.AccountID or req.SourceID must be set:
    //   - AccountID: tokens persist via SetOAuthTokensForClientConfig.
    //   - SourceID:  tokens persist via SetContactSourceOAuthTokens.
    // req.ExpectedEmail enforces a post-callback email match (also
    // forwarded as login_hint so the IdP pre-selects the right account).
    StartIncrementalConsent(req StartIncrementalConsentRequest) error
}

type StartIncrementalConsentRequest struct {
    ClientConfigID ClientConfigID
    Scopes         []AuthScope
    AccountID      string // mutually exclusive with SourceID
    SourceID       string
    ExpectedEmail  string
    LoginHint      string
}
```

Extensions get pre-configured HTTP clients with bearer token injection and transparent refresh-on-401. They never see access tokens, refresh tokens, or passwords. Full details in [§ Auth Broker](#auth-broker). Write-grant details in [§ Write-access grant flow](#write-access-grant-flow-account-picker-model).

### `Notifications`

[`internal/core/api/v1/notifications.go`](../internal/core/api/v1/notifications.go)

```go
type Notifications interface {
    Show(req NotifyRequest) error
}
```

Concrete impl: `notificationsCoreImpl` in [`app/coreimpl.go`](../app/coreimpl.go). Wired. `Show` constructs an `internal/notification.Notification` from the request (Title, Body, Icon, plus a `NotificationData` carrying ExtensionID + Path) and hands it to the host's `Notifier.Show`, which routes through the platform-native channel (D-Bus on Linux, NSUserNotification on macOS, etc.).

**Click routing.** When the user clicks a notification, the host's `ClickHandler` reads back `NotificationData`:
- `ExtensionID != ""` → raises the window + emits a Wails `extension:open` event with `{extensionId, path}`. App.svelte stashes the deep link via the pending-deep-link buffer and switches the rail tab; the extension's pane drains the link on mount.
- `ExtensionID == ""` → falls through to mail's existing `notification:clicked` event (AccountID/FolderID/ThreadID).

`NotifyClickAction` documents three kinds — `open-extension` is fully wired; `open-deep-link` is encoded the same way (extension owns its path scheme); `custom` is reserved for a future host-side handler registry. Calendar's VALARM scheduler is the first concrete consumer — see [`extensions/calendar/backend/alarm_scheduler.go`](../extensions/calendar/backend/alarm_scheduler.go).

### `UI`

[`internal/core/api/v1/ui.go`](../internal/core/api/v1/ui.go)

```go
type UI interface {
    // Registrations
    RegisterRailTab(req RailTabRequest) (Unregister, error)
    RegisterSettingsTab(req SettingsTabRequest) (Unregister, error)
    RegisterContextMenuItem(req ContextMenuRequest) (Unregister, error)
    RegisterInboxView(req InboxViewRequest) (Unregister, error)
    RegisterAccountSetupHook(req AccountSetupHookRequest) (Unregister, error)

    // UI actions
    OpenURL(url string) error
}
```

Two halves: the five `Register*` methods that extensions use to publish UI surface descriptions to the host, and `OpenURL` for triggering a host-mediated user-facing action.

**Registrations.** Concrete impl: [`internal/extensions/ui/registry.go`](../internal/extensions/ui/registry.go) (Phase 2a). All five registration methods are wired and concurrency-safe (`RWMutex`-protected map per kind). `RailTab` and `AccountSetupHook` have real frontend consumers in v0.3.x; the other three (`SettingsTab`, `ContextMenuItem`, `InboxView`) accept registrations but no consumer reads them yet. See [§ UI registration](#ui-registration).

**`OpenURL(url string) error`** opens the given URL in the user's system browser via the host's hardened resolver. Behavior:

- **Protocol allowlist** — only common safe schemes (http, https, mailto, etc.) are permitted; `file://` and other dangerous schemes are rejected with `"URL protocol not allowed for security reasons"`.
- **Linux:** tries the XDG `OpenURI` portal first (works inside Flatpak where `xdg-open` can't reach host browsers), falls back to `xdg-open`.
- **Other platforms:** delegates to the platform's standard URL handler.

Extensions consume this instead of reaching for Wails' `BrowserOpenURL` directly so the security gates stay centralized. Calendar's `EventDetail` uses it to make URLs in event summary / location / description clickable; pattern is reusable from any extension's frontend through its own `<Extension>_OpenURL` Wails wrapper.

Pattern from `extensions/calendar/backend/bridge.go`:

```go
func (b *CalendarBridge) Calendar_OpenURL(url string) error {
    if !b.gateEnabled() {
        return errors.New("calendar: extension disabled")
    }
    if b.deps.Core == nil {
        return errors.New("calendar: core not available")
    }
    return b.deps.Core.UI().OpenURL(url)
}
```

Gated per R16; `ensureInit()` is intentionally NOT called because `OpenURL` touches none of the lazy-initialized extension state (no store, no syncer, no scheduler). Stateless host delegation only.

### `Storage`

[`internal/core/api/v1/storage.go`](../internal/core/api/v1/storage.go)

```go
type Storage interface {
    KV(extensionID string) KVStore
    Secrets(extensionID string) Secrets
    HostSecrets() HostSecrets
}

type KVStore interface {
    Get(key string) (string, error)
    Set(key, value string) error
    Delete(key string) error
    List(prefix string) ([]string, error)
}

type Secrets interface {
    Set(key, value string) error    // empty value treated as Delete
    Get(key string) (string, error) // returns "" + nil error when missing
    Delete(key string) error        // idempotent
    DeleteAll() error               // uninstall cleanup
}

type HostSecrets interface {
    Get(key string) (string, error) // key uses "<class>:<id>" prefix
}
```

Three scoped surfaces. `KV` and `Secrets` are extension-isolated by `extensionID`; `HostSecrets` is a single host-wide read-only façade routed by key prefix.

**Two credential-ownership patterns extensions can use:**

| Pattern | API | Owner | Example |
|---|---|---|---|
| **A — extension-owned** | `Secrets(extensionID)` | extension reads + writes | Calendar's CalDAV passwords (only the calendar extension can add/edit/delete them) |
| **B — core-owned, extension-readable** | `HostSecrets()` | core writes; extension reads | Contacts' CardDAV passwords (core's account-settings UI manages them; contacts reads to PUT vCards) |

Pick Pattern A when only your extension has any business with the credential. Pick Pattern B when the credential's lifecycle is tied to a host-level concept (an account, a shared resource) and your extension is just a consumer.

**`KV` — Phase 1 stub.** `storageCoreImpl.KV(extensionID)` returns a `stubKV` whose four methods return `ErrUnimplemented`. The interface is stable, but no consumer needs it today: every first-party extension that has settled storage needs either uses its own per-extension SQLite (Calendar's `meta` table for the display tz; Contacts via its existing schema) or uses `Secrets`/`HostSecrets` for credential material. KV gets wired (likely via a shared `ext_kv` table indexed on extensionID) when a real consumer arrives.

**`Secrets` (Pattern A) — wired.** `storageCoreImpl.Secrets(extensionID)` returns a `secretsCoreImpl` that delegates to `internal/credentials.Store`'s extension-scoped helpers, which run keyring-first with an AES-encrypted DB-table fallback when the OS keyring is unavailable. Extensions never import `internal/credentials` directly — the four methods cover the whole credential lifecycle:
- `Set` — empty value short-circuits to `Delete`.
- `Get` — `("", nil)` for missing; callers distinguish from errors by checking the string.
- `Delete` — idempotent.
- `DeleteAll` — used during uninstall to clean up all of an extension's secrets at once.

Used today by the Calendar extension for CalDAV passwords (`extensions/calendar/backend/bridge.go`'s `b.deps.Core.Storage().Secrets("calendar")`).

**`HostSecrets` (Pattern B) — wired.** `storageCoreImpl.HostSecrets()` returns a `hostSecretsCoreImpl` that routes by the key's class prefix to the matching host-side `credentials.Store` helper. Read-only by design: the host owns add/update/delete; the extension just consumes the value at request time. Add new prefixes in `app/coreimpl.go::hostSecretsCoreImpl.Get` when a future Pattern B consumer emerges.

Key format: `"<class>:<id>"`. Today only `"carddav:<sourceID>"` is supported; the contacts extension uses it like:

```go
password, err := a.core.Storage().HostSecrets().Get("carddav:" + source.ID)
```

Per-extension SQLite is the parallel persistence path for domain data — see [§ Per-extension storage](#per-extension-storage).

### `EventBus`

[`internal/core/api/v1/events.go`](../internal/core/api/v1/events.go)

```go
type EventBus interface {
    Publish(name string, payload any) error
    Subscribe(name string, handler func(payload any)) (Unsubscribe, error)
}
```

Concrete impl: `eventBusCoreImpl` in [`app/eventbus.go`](../app/eventbus.go). Wired. Two consumer audiences are served from a single Publish call:

1. **Go-side subscribers** registered via `core.Events().Subscribe(name, handler)` get fan-out as in-process function calls. The host snapshots the subscriber list under a mutex, then invokes handlers outside the lock so a handler that re-subscribes / unsubscribes doesn't deadlock.
2. **Frontend** — every Publish also calls `wailsRuntime.EventsEmit` so the Svelte side can subscribe to the same event names via `EventsOn()`. EXCEPTION: `system:*` events stay Go-side only — they're infrastructure for sync engines; the frontend uses its own event names for the equivalent UX-level signals.

**System events the host publishes today** (consumable by any extension):
- `system:wake` — emitted from `app/background.go::processSleepWakeEvents` when the platform's sleep monitor reports a wake. Calendar's Syncer + AlarmScheduler subscribe to it (resync after sleep, mark missed alarms `fired`, re-arm future ones).
- `system:network-online` — emitted when the platform's network monitor reports the link came up. Calendar's Syncer triggers an immediate `SyncAllSources`.

**Lightweight-by-default (R15) is preserved.** The EventBus is a lazy singleton via `sync.Once`; first `Publish` or `Subscribe` constructs it. `Publish` on an empty subscriber list is one map-lookup + zero-length loop (~10ns) — no extra goroutines, no extra D-Bus subscriptions, no cost to mail or to disabled-extension configurations.

Aside from `system:*`, extensions publish their own namespaced events (e.g. Calendar publishes `calendar:sync-complete` and `calendar:source-error`). Other extensions can subscribe via `core.Events().Subscribe(...)` for cross-extension loose coupling.

### Shared types

[`internal/core/api/v1/types.go`](../internal/core/api/v1/types.go)

- `Address{ Name, Email }`
- `Attachment{ Filename, MIMEType, Size, Data, Path, IsInline, ContentID }`
- `MessageRef{ AccountID, FolderID, MessageID }` — Aerion DB id, not RFC 5322 Message-ID
- `Flags{ Seen, Flagged, Answered, Draft, Deleted, Forwarded }`
- `FolderKind` — `inbox|sent|drafts|trash|archive|spam|all|starred`
- `Message`, `Folder` — API-surface mirrors of internal storage types (decoupled so internal storage can evolve)
- `MessageFilter`, `ContactFilter`
- `Contact{ ID, Name, Emails, SourceID, UpdatedAt }`
- `Unregister`, `Unsubscribe` — `func()` aliases returned by registration / subscription methods

### Sentinel errors

[`internal/core/api/v1/errors.go`](../internal/core/api/v1/errors.go)

| Error | When |
|---|---|
| `ErrDisabled` | Extension or feature is disabled; treat as a benign "feature off" signal |
| `ErrCapabilityDenied` | Method called on a capability not granted (Phase 1: never happens for first-party — all-or-nothing) |
| `ErrAccountNotFound` | API call references an account that doesn't exist |
| `ErrUnimplemented` | Method scaffolded but not implemented in this release |
| `*ErrAdditionalConsentRequired{ AccountID, ClientConfigID, MissingScopes }` | Auth Broker needs additional OAuth scopes; host handles consent, extension retries |

Use `errors.Is(err, coreapi.ErrXxx)` for sentinel matching. `ErrAdditionalConsentRequired` is a typed error (not a sentinel) — type-assert to read `MissingScopes`.

---

## Per-extension storage

Package: [`internal/extensions/`](../internal/extensions/) (files [`store.go`](../internal/extensions/store.go) and [`kv.go`](../internal/extensions/kv.go)).

```go
import "github.com/hkdb/aerion/internal/extensions"

func NewStore(dataDir string) (*extensions.Store, error) {
    return extensions.OpenStore(dataDir, "myextension", []extensions.Migration{
        {Version: 1, SQL: `CREATE TABLE myitems (...)`},
        {Version: 2, SQL: `CREATE INDEX ...`},
    })
}
```

**What `OpenStore` does:**

1. Resolves the path to `<dataDir>/extensions/<name>/data.db` (creates parent dirs with 0700 perms)
2. Opens the DB via the standard `database.Open` (inherits WAL, busy timeout, 0600 file perms, etc.)
3. Creates the canonical `ext_kv` table BEFORE user migrations run, so KV is always available even with zero user tables
4. Creates an extension-private `migrations` table and applies user migrations in version order, idempotently

**Reaching the SQL:**

```go
db := store.DB()           // *sql.DB; for the extension's own tables only
store.Path()               // on-disk file path
kv := store.KV()           // coreapi.KVStore backed by ext_kv table
```

**Migrations** start at version 1, increment monotonically. Each runs inside a transaction. Already-applied versions are skipped on every startup. Each extension's migration sequence is INDEPENDENT — no global migration namespace.

**File location:** Linux `~/.local/share/aerion/extensions/<name>/data.db`, macOS `~/Library/Application Support/Aerion/extensions/<name>/data.db`, Windows `%LOCALAPPDATA%\aerion\extensions\<name>\data.db`. The `extensions/` parent is created by [`internal/platform/paths.go EnsureDirectories`](../internal/platform/paths.go).

**Lifecycle:** Per the architecture doc, stores open EAGERLY at `App.Startup`, regardless of whether the extension is currently enabled. This keeps schemas valid across enable/disable cycles — users can disable, the migrations stay applied, re-enabling is instantaneous.

**KV namespace:** [`internal/extensions/kv.go`](../internal/extensions/kv.go) implements `coreapi.KVStore` backed by the `ext_kv` table. Use it for sync tokens, view prefs, anything that doesn't warrant its own table. `Get` returns `("", nil)` for missing keys (no error). `Delete` is idempotent. `List(prefix)` returns sorted keys; `prefix=""` returns all.

---

## Auth Broker

Package: [`internal/extensions/auth/`](../internal/extensions/auth/) (files [`broker.go`](../internal/extensions/auth/broker.go), [`transport.go`](../internal/extensions/auth/transport.go), [`scope.go`](../internal/extensions/auth/scope.go)).

The Auth Broker is the ONLY way an extension reaches external services that use OAuth. Extensions never see OAuth access tokens or refresh tokens — token refresh is transparent. Multi-client-config routing handles the "Mail uses project A, Calendar/Contacts use project B" reality without forcing users to re-authenticate the unrelated service.

For host-managed *basic-auth passwords* (today: CardDAV passwords whose lifecycle lives in core's account settings), extensions consume the documented Pattern B surface — `core.Storage().HostSecrets().Get("carddav:<sourceID>")` — rather than the Auth Broker. That's the *only* mechanism by which an extension may read a host-managed password, and it's read-only.

### `HTTPClient`

```go
client, err := core.Auth().HTTPClient(accountID, []coreapi.AuthScope{
    {Resource: "https://www.googleapis.com/auth/calendar.readonly",
     Reason:   "Read your calendar to sync events"},
})
if err != nil {
    var needConsent *coreapi.ErrAdditionalConsentRequired
    if errors.As(err, &needConsent) {
        // Don't try to fix this — the HOST is responsible for triggering
        // consent. Return ErrAdditionalConsentRequired up the call chain;
        // the host's Wails layer will surface the consent UI and the user
        // retries the action.
        return err
    }
    return fmt.Errorf("auth broker: %w", err)
}

// Use the client normally — bearer token + refresh-on-401 are transparent.
resp, err := client.Get("https://www.googleapis.com/calendar/v3/users/me/calendarList")
```

### Routing logic

`core.Auth().HTTPClient(...)` calls into [`internal/extensions/auth/broker.go HTTPClientForExtension`](../internal/extensions/auth/broker.go), which reads the calling extension's manifest to decide where each scope routes:

1. Broker reads the account's existing Mail tokens to discover its provider (`google`, `microsoft`).
2. Broker classifies each requested scope using the extension's manifest:
   - Scopes listed in `manifest.oauth.first_party_uses_core_for_scopes` route to Aerion core's **mail** client config (`<provider>-mail`) — reuses existing mail consent; no new prompt.
   - Scopes NOT listed route to the **extension's own** client config (`<provider>-<extensionID>`, e.g., `google-contacts`).
3. **Mixed-scope calls are rejected.** Some-to-core + some-to-own in a single call returns an error; the extension must split into two HTTPClient calls.
4. Broker checks whether the account has tokens under the resolved `ClientConfigID` covering the requested scopes.
5. **Covered**: returns `*http.Client` whose Transport injects bearer + refreshes on 401.
6. **Not covered**: returns `*coreapi.ErrAdditionalConsentRequired{ AccountID, ClientConfigID, MissingScopes }`. The host runs the incremental-consent flow ([§ Incremental consent flow](#incremental-consent-flow)) and the extension retries.

### Token refresh

[`internal/extensions/auth/transport.go bearerRefreshTransport`](../internal/extensions/auth/transport.go) handles refresh. It serializes refreshes per `(accountID, clientConfigID)` so N concurrent expired-token requests cause exactly one refresh.

### IMAP / SMTP

```go
imapClient, err := core.Auth().IMAPClient(accountID, []string{"SIEVE"})
smtpClient, err := core.Auth().SMTPClient(accountID)
```

Phase 1: both return `coreapi.ErrUnimplemented`. Phase 2+ wires them when a real consumer needs IMAP-via-broker (Sieve script management, custom X-* commands) or SMTP-via-broker (delayed-send queues).

---

## OAuth client configurations

Package: [`internal/oauth2/clientconfig.go`](../internal/oauth2/clientconfig.go).

Each first-party extension owns its own OAuth client (Google Cloud project / Azure AD registration). This is INTENTIONAL: it sets the precedent for future community extensions (no first-party shortcut to grandfather in), avoids re-verification cascade when Mail's project doesn't need to change, and the UX cost (one Google consent click per account + per extension) is acceptable because the browser is already signed in.

### Registry

```go
type ClientCredentials struct {
    ClientID     string
    ClientSecret string
}

// Known ids:
//   "google-mail"          — Aerion core's Mail-scoped Google project
//   "microsoft-mail"       — Aerion core's Mail-scoped Azure AD registration
//   "google-<extensionID>" — per-extension Google project (e.g., "google-contacts")
//   "microsoft-<extensionID>" — per-extension Azure AD registration (e.g., "microsoft-contacts")
func ClientConfigForID(id string) (ClientCredentials, bool)
```

Resolution order:

1. User override from `credentials.Store` (Settings UI override) via `oauth2.UserOverrideLookup`
2. Registered `CredentialsProvider` chain — Aerion core's mail slots, then each extension's own slots (registered at startup from each extension's `OAuthClients()` return value)
3. `(zero, false)` → the consent flow surfaces "no creds configured" pointing the user at the extension's settings dialog

> **Don't use `oauth2.GetProvider(clientConfigID)` to test "is this slot configured?"** It silently inherits the mail-side ldflag creds when the extension slot is empty, producing a misleading "configured" answer. Use `oauth2.ClientConfigForID(clientConfigID)` — that's the canonical resolver and only returns truthy when there are real per-slot creds. See [§ Incremental consent flow](#incremental-consent-flow) for the correctness rule.

### Provider lookup

```go
provider, err := oauth2.GetProviderForClientConfig("google-contacts")
// provider.ClientID, provider.ClientSecret are populated from the extension's slot
// (via ClientConfigForID's resolution chain).
// provider.Scopes are the default Google scopes (override per-extension as needed).
```

### Provisioning a new client config

When you ship a new first-party extension that needs its own OAuth project:

1. Create a Google Cloud project (or Azure AD app registration) with the scopes your extension needs.
2. Define ldflag-injected vars in `extensions/<name>/creds.go` (e.g., `GoogleClientID`, `GoogleClientSecret`, `MicrosoftClientID`). See [`extensions/contacts/creds.go`](../extensions/contacts/creds.go) for the canonical pattern.
3. Return them from the extension's `OAuthClients()` as `[]coreapi.OAuthProviderRegistration` keyed by `<provider>-<extensionID>`. The host iterates this list at startup and registers each entry into the global `ClientConfigForID` resolver chain.
4. Inject the actual values at build time: typically a per-extension `.env` file (`extensions/<name>/.env`) consumed by the Makefile via `-ldflags '-X github.com/hkdb/aerion/extensions/<name>.GoogleClientID=...'`.
5. Optionally also expose an "Aerion - {Google,Microsoft}" option in the extension slot's dropdown (see [§ User-supplied OAuth credentials](#user-supplied-oauth-credentials-override-ui)) so users on builds with shipped credentials can opt into them without pasting anything. The option only appears when the corresponding shipped creds were injected at build time. Choosing it clears any user-typed creds on the slot; the resolver then falls through to the shipped values via the provider chain.

Once the extension's slot is populated, `ClientConfigForID("<provider>-<extensionID>")` returns configured credentials and the Auth Broker routes the extension's scope requests to that client. Empty entries are safe — extensions can declare all their slots unconditionally and rely on build-time injection to fill in only the ones with credentials.

Aerion core's mail credentials follow a separate path: they're loaded from the `aerion-creds` shim binary (or build-time ldflags) at startup. See [`internal/oauth2/config.go`](../internal/oauth2/config.go). Extension credentials do NOT use the shim — they live in their own extension package.

### Mapping legacy provider names

[`oauth2.ClientConfigIDForProvider(name)`](../internal/oauth2/clientconfig.go) maps legacy provider strings (stored in `oauth_tokens.provider` column) to their default Mail client config:

| Provider name | Maps to |
|---|---|
| `google`, `google-contacts` | `google-mail` |
| `microsoft`, `microsoft-contacts` | `microsoft-mail` |

Used internally for back-compat queries; extension code rarely needs this directly.

---

## UI registration

Phase 1: interfaces only ([`internal/core/api/v1/ui.go`](../internal/core/api/v1/ui.go)). Phase 2a ships the first concrete registry implementation in `internal/extensions/ui/`. The five registration methods all return an `Unregister` func the caller invokes to remove the registration (e.g., on extension disable or shutdown).

### `RegisterRailTab` — Phase 2a (Contacts)

A vertical icon button on the left activity bar. The rail only renders when 2+ extensions are enabled.

```go
unreg, err := core.UI().RegisterRailTab(coreapi.RailTabRequest{
    ExtensionID: "contacts",
    Label:       "Contacts",
    Icon:        "mdi:account-multiple",
    Component:   "ContactsPane",   // Svelte component identifier
    Order:       10,
})
```

### `RegisterAccountSetupHook` — Phase 2a (Contacts)

A panel that appears in the post-account-add flow in `AccountDialog`. See [§ Account-setup hook contract](#account-setup-hook-contract).

```go
unreg, err := core.UI().RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
    ExtensionID: "contacts",
    Providers:   []string{"google", "microsoft"},
    ButtonLabel: "Also set up your contacts",
    Component:   "AccountContactsHookPanel",
})
```

### `RegisterSettingsTab`, `RegisterContextMenuItem`, `RegisterInboxView` — Phase 3+

Registrations are accepted but no consumer reads them yet. Reserved for future use; design preserved in the v1 interface so extensions can declare intent now.

---

## Account-setup hook contract

The most important contract for extension UX. Mirrors Thunderbird's "Also set up Calendar / Contacts for this account?" flow.

### Backend registration

In your extension's startup wiring:

```go
core.UI().RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
    ExtensionID: "myext",
    Providers:   []string{"google", "microsoft", "imap"},
    ButtonLabel: "Also set up <feature> for this account",
    Description: "Optional context shown alongside the button",
    Component:   "MyExtAccountHookPanel",  // Svelte component identifier
})
```

`Providers` lists which mail-account provider strings the hook matches. Only hooks whose `Providers` includes the just-added account's provider will be offered to the user.

### Frontend flow

Wired in Phase 2a via [`AccountDialog.svelte`](../frontend/src/lib/components/settings/AccountDialog.svelte):

1. After `AccountDialog.handleSubmit` successfully creates an account, the dialog computes a `provider` string: `oauthCredentials.provider` for OAuth accounts (`"google"` or `"microsoft"`), `"imap"` otherwise.
2. Dialog calls `loadAccountSetupHooks(provider)` ([`extensionRegistry.svelte.ts`](../frontend/src/lib/stores/extensionRegistry.svelte.ts)) which wraps the Wails-bound `App.ListAccountSetupHooksForProvider`. Hooks are returned regardless of enable state — the hook IS the discovery surface that enables the extension.
3. **Zero hooks** → dialog closes. **Non-zero** → dialog renders a "hooks step" UI that dispatches each hook to its registered Svelte component by `hook.component` name (e.g., `"AccountContactsHookPanel"` → [`extensions/contacts/frontend/hooks/AccountContactsHookPanel.svelte`](../extensions/contacts/frontend/hooks/AccountContactsHookPanel.svelte)).
4. Each panel is opt-in: user clicks "Set up" or "Skip". The "Set up" handler runs the extension's onboarding (Phase 2a Contacts: `LinkAccountContactSource` + `SetExtensionEnabled('contacts', true)` + `refreshExtensionRegistry()`).
5. When all panels resolve (set up or skipped), or the user clicks "Skip all", the dialog closes.

The dispatch in `AccountDialog.svelte` is a static `{#if hook.component === '...'}` block. When you add a new hook component, extend that block — don't switch to `<svelte:component>` dynamic mounting (the component identifier is descriptive only).

### Constraints

- Hook panels must NEVER auto-enable extensions or auto-grant scopes. Every action requires an explicit user click.
- Skipping a panel is the explicit default. Closing the dialog mid-wizard is equivalent to skipping.
- Hooks register at `App.Startup` (synchronously, before Wails serves the frontend) so the dialog's query is always race-free.
- Hooks are returned regardless of whether their extension is currently enabled. The hook IS the discovery surface — its "Set up" handler is what enables the extension. Filtering by enabled state would hide first-party features from new users (extensions default to disabled).

---

## Lifecycle

### What runs at `App.Startup` regardless of enable state

Three things, **and nothing else**, run unconditionally per extension at startup:

1. **Bridge struct allocation** — `app/extension_<name>.go` calls `extbe.NewBridge(...)`. This is a zero-cost struct literal with host-dependency fields only; no SQLite, no migrations, no stores.
2. **Extension lifecycle struct allocation** — `extbe.NewExtension()` returns a manifest holder. Manifest copy only.
3. **`Extension.Register(core)`** — wires descriptive UI registrations (rail tab, account-setup hook) into the host's registries so the Settings UI and account-setup dialog can always list them. The frontend filters at render time on enabled state.

```go
// In app/app.go Startup:
a.initContactsExtension()                          // 1: bridge alloc
a.contactsExt = extcontactsbe.NewExtension()       // 2: lifecycle alloc
a.knownExtensions = []coreapi.Extension{a.contactsExt}

core := newCoreForExtension(a, a.contactsExt)
unreg, err := a.contactsExt.Register(core)         // 3: UI registrations
// ...
```

**Crucially, the per-extension SQLite file is NOT opened at startup.** Migrations are deferred — they fire the first time the user enables the extension *and* the bridge's first Wails method runs (which calls `ensureInit()` under a `sync.Once`). A user who never enables an extension never pays for its database. This is a deliberate departure from earlier drafts of `context/EXTENSION_ARCHITECTURE.md` that opened the file eagerly; the lightweight-by-default invariant trumped the cross-cycle migration durability concern, since SQLite migrations run idempotently from current schema version anyway.

### What runs only when enabled

Two things gate on the enabled flag:

1. **`Bridge.gateEnabled()` short-circuits Wails-bound methods.** Disabled = the method returns `nil`/empty (no error) before `ensureInit()` is ever called. Frontend code never needs to check enabled state.
2. **Lazy init fires once.** The first enabled call invokes `ensureInit()`, which opens SQLite, applies migrations, and constructs `Store + API`. Subsequent calls reuse the live API.

Background services (sync schedulers, IDLE managers, event publishers) follow the same lazy pattern — start them inside `ensureInit()` rather than at `Startup`, so a disabled extension contributes no goroutines, no timers, and no file handles. The Bridge struct itself is the only thing in memory.

**Implication for the user:** enabling an extension after Aerion is launched works — the first method call performs the migrations + initialization transparently. Disabling an extension stops new Wails calls from reaching its API; the already-allocated Store + API stay in memory until the next app restart. Users who want to **fully reclaim memory** after disabling must restart — the project deliberately accepts this trade-off because (a) disable→memory-free without restart would require a coordinated shutdown of the extension's goroutines that isn't worth designing yet, and (b) Aerion is a long-running desktop app where restart is cheap.

### Enable / disable

User-facing enable/disable goes through `App.SetExtensionEnabled(name, enabled)` ([§ Wails-bound surface](#wails-bound-surface)). The host is responsible for starting/stopping the extension's background services in response to the flag changing. Phase 1 ships the flag; full lifecycle wiring lands when each extension ships its own background services.

---

## Settings keys

[`internal/settings/store.go`](../internal/settings/store.go) ships the canonical key constants:

```go
const (
    KeyExtensionCalendarEnabled = "extension_calendar_enabled"
    KeyExtensionContactsEnabled = "extension_contacts_enabled"
)
```

Format: `extension_<name>_enabled`. All extensions default to `false`. Helpers:

```go
func (s *Store) IsExtensionEnabled(name string) (bool, error)
func (s *Store) SetExtensionEnabled(name string, enabled bool) error
```

When you ship a new extension, add its key constant alongside the existing two. Use the generic `name`-based helpers — don't write a typed `Get/SetXxxEnabled` per extension.

---

## Wails-bound surface

The frontend calls these via the generated Wails bindings at `frontend/wailsjs/go/app/App.{js,d.ts}`. After modifying any Wails-bound method on `*App` OR on an embedded `*Bridge`, run `make generate` to regenerate the bindings.

### Host methods (`App` package, no prefix)

Extension-relevant subset. Many other `App.*` methods exist for mail-side concerns that extensions don't touch.

| Method | Purpose |
|---|---|
| `App.IsExtensionEnabled(name string) (bool, error)` | Read the extension's enabled flag |
| `App.SetExtensionEnabled(name string, enabled bool) error` | Write the enabled flag (frontend triggers from Settings UI) |
| `App.LogFrontend(level, message string)` | Bridge for frontend logging — appears in the same zerolog stream as backend logs with `component=frontend`. Levels: `debug|info|warn|error`. Unknown levels fall through to info. |
| `App.ListEnabledExtensions() ([]string, error)` | All currently-enabled extension names (iterates `settings.AllExtensionKeys`). The frontend rail renders when `len() >= 1` (one enabled extension + always-on Mail = two rail items to switch between). |
| `App.ListExtensionRailTabs() ([]v1.RailTabRequest, error)` | Rail tabs for currently-enabled extensions only. Source: [`app/extension_ui.go`](../app/extension_ui.go). |
| `App.ListAccountSetupHooksForProvider(provider string) ([]v1.AccountSetupHookRequest, error)` | Hooks matching a provider, returned regardless of enable state (hooks are the discovery surface that enables an extension). Called by `AccountDialog.svelte` after a new account is created. |
| `App.ListExtensions() ([]app.ExtensionInfo, error)` | Full extension listing for Settings → Extensions tab. Returns manifest fields + current `enabled` state per extension. Iterates `a.knownExtensions`. Source: [`app/extension_ui.go`](../app/extension_ui.go). |
| `App.SetContactSourceWritable(sourceID string, writable bool) error` | Flip a contact source's writable flag. Used by the Contacts extension's settings UI to enable/disable write access on CardDAV sources (a pure flag flip) and to disable previously-enabled OAuth sources. Enabling OAuth sources goes through `<Extension>_StartIncrementalConsent` (which calls `SetSourceWritable` server-side after consent). |
| `App.GetOAuthCredsStatus(configID string) (app.OAuthCredsStatus, error)` | Reports per-slot config presence (`hasUserOverride`, `hasShipped`, last-4-char fingerprint of the active client_id). Never returns secret values. Used by the OAuth Credentials editor in each extension's settings dialog. |
| `App.SetOAuthCreds(configID, clientID, clientSecret string) error` | Persist user-supplied client_id + secret for a slot (overrides any shipped defaults). |
| `App.ClearOAuthCreds(configID string) error` | Remove a user override for a slot (reverts to shipped values, if any). Used both by the editor's "Clear" action and when the slot dropdown switches from Custom back to the Aerion-shipped option. |
| `App.ListAuthContextsForProvider(provider string) ([]app.AuthContextInfo, error)` | Enumerates existing matching-provider auth identities: mail accounts (from `accountStore`) + standalone contact sources (`carddavStore.ListSources()` where `AccountID IS NULL` and `Type == provider`). Drives the `WriteAccessAccountPicker` dialog's radio list. Result entries carry `kind` (`"mail"` or `"standalone-contacts"`), `identifier` (account_id or source_id), `email`, and a pre-built display `label`. |
| `App.CancelOAuthFlow()` | Cancel any in-progress OAuth flow (account add, write-access grant, etc.). Stops the OAuth manager's callback server; in-flight backend code returns with a cancellation error. |

### Extension bridge methods (`<Extension>_` prefix, defined on the embedded `*Bridge`)

These methods live in `extensions/<name>/backend/bridge.go` and surface on `App` via embedded-struct method promotion. The `<Extension>_` prefix is **mandatory** — embedded promotion shares one App namespace across all extensions, so unprefixed names would collide silently. The frontend imports them with aliases for ergonomics:

```ts
// extensions/contacts/frontend/stores/contactsView.svelte.ts
import {
  Contacts_ListContactsForBrowse as ListContactsForBrowse,
  Contacts_GetContactDetail     as GetContactDetail,
  Contacts_UpdateContact        as UpdateContact,
  // ...
} from '$wailsjs/go/app/App'
```

Currently bound by the Contacts extension's bridge (all gate on `extension_contacts_enabled`; all lazy-init on first enabled call):

| Method | Purpose |
|---|---|
| `App.Contacts_ListContactsForBrowse(query, sourceID string, limit, offset int) ([]v1.Contact, error)` | Browse listing — wraps `extcontacts.API.ListContacts`. Returns `nil` when Contacts is disabled. |
| `App.Contacts_GetContactDetail(emailOrID string) (*v1.Contact, error)` | Single-contact detail load. |
| `App.Contacts_CreateContact(input v1.ContactCreateInput) (string, error)` | Create new contact. Dispatches by `input.SourceID`: `local:manual` → local store; CardDAV UUID → server PUT to the addressbook; Google source → People API; Microsoft source → Graph API. |
| `App.Contacts_UpdateContact(id string, patch v1.ContactPatch) error` | Multi-field patch update. Backend dispatches by source type (local / CardDAV / Google / Microsoft). |
| `App.Contacts_DeleteLocalContact(idOrEmail string) error` | Delete contact. Method name is historical — handles all source types (local cascade + CardDAV / Google / Microsoft server DELETE), not just local. |
| `App.Contacts_ResizeContactPhoto(b64In string) (backend.ResizedContactPhoto, error)` | Backend image resize for the Edit dialog's photo picker (decodes base64 → CatmullRom rescale to 256px max edge → JPEG re-encode at quality 85). Returns `{data, mediaType}`. |
| `App.Contacts_ListAddressbooks(sourceID string) ([]v1.Addressbook, error)` | Addressbooks for a source — CardDAV addressbooks; Google contactGroups (as `google-group:*` synthetic IDs) + a `google-mycontacts:*` default; Microsoft contactFolders (as `ms-folder:*`) + a `ms-default:*` default. See [§ Contacts](#contacts) for the synthetic-ID table. |
| `App.Contacts_ListSources() ([]v1.ContactSource, error)` | All configured contact sources. Routes through `coreapi.Contacts.ListSources` (host-owned, not bridge-API). |
| `App.Contacts_LinkAccountSource(accountID, name string, syncInterval int) (string, error)` | Creates a contact source backed by an existing OAuth account. Routes through `coreapi.Contacts.LinkAccountSource`. Used by `AccountContactsHookPanel`. |
| `App.Contacts_EnableWriteAccess(sourceID, authContextKind, authContextIdentifier, expectedEmail string) error` | Single entry point for granting write access on a Google or Microsoft contacts source. The frontend `WriteAccessAccountPicker` calls this after the user picks an existing auth identity (mail account or standalone contacts source). Backend derives `clientConfigID` and write-scope from the source's provider, then dispatches into `coreapi.Auth.StartIncrementalConsent` with either `AccountID` (for `"mail"` contexts) or `SourceID` (for `"standalone-contacts"` contexts) populated. `expectedEmail` is enforced post-callback — if the granted identity's email doesn't match, the tokens are discarded and the call returns an error. Flips the source's writable flag on success. Cancellable mid-flow via `App.CancelOAuthFlow`. |

### Frontend logger

In any Svelte component or TS file:

```ts
import { logger } from '$lib/logger'

logger.debug('user clicked send')
logger.info('extension contacts: sync started')
logger.warn('extension contacts: source unreachable')
logger.error(`extension contacts: failed: ${err}`)
```

Fire-and-forget — never throws into caller. See [`frontend/src/lib/logger.ts`](../frontend/src/lib/logger.ts).

---

## Testing conventions

Patterns established in Phase 1:

### Interface compile-tests

[`internal/core/api/v1/types_test.go`](../internal/core/api/v1/types_test.go) defines a `stubCore struct{}` that implements EVERY interface in the package with stub methods. The test simply assigns it: `var c Core = stubCore{}`. This compiles only when every interface signature is still satisfied — drift surfaces immediately.

When you ADD a method to an interface in `coreapi`, update `stubCore` in the same commit.

### Real-store integration tests

[`internal/extensions/store_test.go`](../internal/extensions/store_test.go), [`internal/extensions/auth/broker_test.go`](../internal/extensions/auth/broker_test.go): open a real SQLite via `t.TempDir()` + `database.Open`, exercise the API, assert on results. No mocking of the credentials store or DB.

### Auth broker test pattern

The broker test ([`internal/extensions/auth/broker_test.go`](../internal/extensions/auth/broker_test.go)) sets up a temp DB + real `credentials.Store` + real `oauth2.Manager` (which doesn't fire its OAuth flow without a UI). Then it inserts test tokens directly via `credStore.SetOAuthTokens`. Useful for: scope coverage check, `ErrAdditionalConsentRequired` path, 401 refresh (when the test server is wired).

When you write an extension that uses the broker, mirror this pattern.

### Don't mock; use the real store

Aerion's testing style is integration-flavored: a real SQLite at `t.TempDir()` is fast enough (~10ms per open) and exercises actual SQL behavior. Avoid mock layers unless the dependency is genuinely external (an HTTP server — use `httptest.Server`).

---

## Frontend conventions

### Where Svelte components live

| Area | Path |
|---|---|
| Extension rail (host UI) | [`frontend/src/lib/components/rail/`](../frontend/src/lib/components/rail) |
| Settings → Extensions tab (host UI) | [`frontend/src/lib/components/settings/ExtensionsTab.svelte`](../frontend/src/lib/components/settings/ExtensionsTab.svelte) |
| Contacts extension components | [`extensions/contacts/frontend/components/`](../extensions/contacts/frontend/components) |
| Contacts extension stores | [`extensions/contacts/frontend/stores/`](../extensions/contacts/frontend/stores) |
| Contacts account-setup hook panel | [`extensions/contacts/frontend/hooks/`](../extensions/contacts/frontend/hooks) |
| Contacts extension i18n | [`extensions/contacts/frontend/i18n/`](../extensions/contacts/frontend/i18n) |
| New extensions | `extensions/<name>/frontend/{components,stores,hooks,i18n}/` |

Extension-specific UI lives under `extensions/<name>/frontend/`, NOT under `frontend/src/lib/components/`. Only host-owned UI (rail, settings dialog wiring) stays in `frontend/src/`. Keep new files under ~300 LOC.

Rail switching is bound to `Ctrl+Tab` / `Ctrl+Shift+Tab` in [`App.svelte`](../frontend/src/App.svelte). The cycle order matches the rendered rail (Mail first, then enabled extensions in `Order` ASC). See [`docs/KEYBOARD_SHORTCUTS.md`](KEYBOARD_SHORTCUTS.md) for the full shortcut reference.

### Generated Wails bindings

For files inside `frontend/src/`, use relative paths:

```ts
// @ts-ignore - wailsjs bindings
import { ListContactsForBrowse } from '../../../wailsjs/go/app/App'
```

For files inside `extensions/<name>/frontend/`, use the `$wailsjs` alias:

```ts
// @ts-ignore - wailsjs bindings
import { ListContactsForBrowse } from '$wailsjs/go/app/App'
// @ts-ignore - wailsjs bindings
import type { v1 } from '$wailsjs/go/models'
```

The `@ts-ignore` lines stay mandatory in both locations — the generated `.d.ts` files don't carry TS-friendly path aliases.

### Extension i18n

Each extension owns its translation files, parallel to (and never mixed into) core's. Storage:

```
extensions/<name>/frontend/i18n/
  index.ts                       # exports registerExtensionI18n()
  locales/
    en.json                      # mandatory — English source of truth
    zh-HK.json                   # other locales — added by translators in follow-up PRs
    ...
```

The extension's `index.ts` is a one-liner per locale file using the same `svelte-i18n` `register()` API the core uses:

```ts
// extensions/<name>/frontend/i18n/index.ts
import { register } from 'svelte-i18n'

export function registerExtensionI18n() {
  register('en', () => import('./locales/en.json'))
  register('zh-HK', () => import('./locales/zh-HK.json'))
  // ... add a line per locale that ships ...
}
```

**Auto-discovery — no host edits per extension.** The core's `initI18n()` uses [Vite's `import.meta.glob`](https://vite.dev/guide/features.html#glob-import) to find every extension's `i18n/index.ts` at build time and call its `registerExtensionI18n()` automatically:

```ts
// frontend/src/lib/i18n/index.ts
const extensionRegistrars = import.meta.glob<{
  registerExtensionI18n: () => void
}>('../../../../extensions/*/frontend/i18n/index.ts', { eager: true })

export async function initI18n(savedLocale?: string): Promise<void> {
  for (const mod of Object.values(extensionRegistrars)) {
    mod.registerExtensionI18n?.()
  }

  init({ fallbackLocale: 'en', initialLocale: savedLocale || detectSystemLocale() })
  await waitLocale()
}
```

Adding a new extension's i18n is purely a file drop under `extensions/<name>/frontend/i18n/` — no edit to `frontend/src/lib/i18n/index.ts` required. Vite resolves the glob at build time, so the registration calls are statically baked into the bundle (no runtime filesystem reads).

**Why `register()` allows merging:** `svelte-i18n` accepts multiple loaders per locale code. When a locale activates, all registered loaders for that code resolve in parallel and their dictionaries merge into one namespace. Core ships `common.*`, `viewer.*`, `composer.*`; an extension ships `contacts.*` (or whatever its own namespace is) — no key collision possible as long as namespaces are distinct.

**Key namespace convention:** use the extension id as the top-level namespace (`contacts.edit.save`, `calendar.event.create`). Don't reuse core's `common.*` — duplicate translations of `Save`/`Cancel` are cheap and prevent accidental coupling.

**Translator workflow:** translators follow [`docs/LANGUAGE.md`](LANGUAGE.md), which lists the extension locale files as additional bullets in the checklist. A translator who finishes the core file but skips an extension still produces a usable PR — the extension's UI falls back to English via svelte-i18n's `fallbackLocale: 'en'` setting until someone fills in the gap.

**Lightweight invariant:** every extension's `registerExtensionI18n()` runs at startup regardless of enabled state, but `register()` only queues a lazy loader — the JSON isn't fetched until the locale activates and the extension's component first calls `$_()`. Disabled extensions whose UI never renders never load their dictionaries.

### Stores

[`frontend/src/lib/stores/extensionRegistry.svelte.ts`](../frontend/src/lib/stores/extensionRegistry.svelte.ts) — frontend cache of enabled extensions and rail tabs. Exposes:

```ts
extensionRegistry.enabled       // string[]
extensionRegistry.railTabs      // v1.RailTabRequest[]
extensionRegistry.railVisible   // boolean (true when length >= 1 — Mail + 1 extension)
extensionRegistry.isEnabled(name)
refreshExtensionRegistry()      // call after enable/disable toggle
loadAccountSetupHooks(provider) // returns v1.AccountSetupHookRequest[]
```

Call `refreshExtensionRegistry()` after `SetExtensionEnabled` so the rail/hooks reflect the new state.

### Active-extension state

Persisted via [`uiState.svelte.ts`](../frontend/src/lib/stores/uiState.svelte.ts) field `activeExtension`:

```ts
import { getActiveExtension, setActiveExtension } from '$lib/stores/uiState.svelte'

const current = getActiveExtension()  // 'mail' | 'contacts' | …
setActiveExtension('contacts')        // debounced save to backend
```

The default is `'mail'`. Switching does NOT clear mail selection (folder/thread state); flipping back to Mail restores the previous mail context exactly.

### Rail-tab component contract

Rail tabs are declared by the backend (`coreapi.RailTabRequest`); the frontend renders them via [`ExtensionRail.svelte`](../frontend/src/lib/components/rail/ExtensionRail.svelte). Each tab needs:

- `extensionId` — the canonical extension name (must match `settings.AllExtensionKeys`)
- `label` — display string (no i18n keys yet — Phase 2a uses plain English)
- `icon` — iconify identifier (e.g., `mdi:account-multiple`)
- `component` — Svelte component identifier; App.svelte switches on `extensionId` to pick the matching component to render

### Slot pattern

The "slot" is a conditional in [`App.svelte`](../frontend/src/App.svelte):

```svelte
{#if getActiveExtension() === 'contacts'}
  <ContactsPane />
{:else}
  <!-- mail layout -->
{/if}
```

When adding a new extension, extend this `if`/`else if` block. Don't refactor it into a dynamic Svelte `<svelte:component>` mount — the component identifier in `RailTabRequest` is descriptive only; the host owns the static dispatch table.

### Vite + tsconfig aliases

Two aliases are configured in [`frontend/vite.config.ts`](../frontend/vite.config.ts) and [`frontend/tsconfig.json`](../frontend/tsconfig.json):

| Alias | Resolves to | Used by |
|---|---|---|
| `$extensions/*` | `<repo>/extensions/*` | Host (App.svelte, AccountDialog.svelte) importing extension Svelte components |
| `$wailsjs/*` | `<repo>/frontend/wailsjs/*` | Extension Svelte/TS files importing generated Wails bindings (without deep `../` chains) |

Because extension files live outside `frontend/`, Rollup's default node-modules walking doesn't find `frontend/node_modules`. Shared npm dependencies (currently `@iconify/svelte`, `svelte-i18n`) are aliased explicitly in `vite.config.ts` to point back at the host's `node_modules`. Add new entries to the alias list as extensions pull in additional npm packages.

**Extension-pulled deps convention (interim).** Until per-extension `package.json` / npm workspaces land, every extension dep is installed at the host's `frontend/package.json` and aliased in `vite.config.ts`. Tag each alias with the owning extension's manifest ID in a comment so we know which deps to move when we split. The aliases in `vite.config.ts` are grouped into **SHARED** (kit + multi-extension) and **PER-EXTENSION** sections — keep new entries in the right group.

Three steps to add an extension-pulled npm dep:

1. `npm install <pkg>` in `frontend/` (writes to host's `package.json` + `package-lock.json`).
2. Add the alias to `vite.config.ts` under PER-EXTENSION, with the owning extension's manifest ID in a one-line comment.
3. Run `python3 build/flatpak/flathub/gen-node-sources.py` so Flatpak CI picks up the tarball (per `feedback_regenerate_node_sources.md`). Commit lockfile + Flatpak sources together.

If the dep's package.json ships a broken `exports` map (no `types` condition), add a small `.d.ts` shim under `extensions/<name>/frontend/lib/` so `svelte-check` can resolve it. `date-fns-tz` is the current example.

This coupling is architectural debt — extensions touching `frontend/vite.config.ts` and `frontend/package.json` violates the SDK promise. Tracked for future work (Vite resolver plugin → manifest-declared deps → per-extension `package.json`).

`tsconfig.json` includes `../extensions/**/frontend/**/*.{ts,svelte}` in its `include` array so `svelte-check` validates extension code alongside host code. Explicit `paths` entries (`@iconify/*`, `svelte`, `svelte/*`) keep TypeScript's type resolution pointing at the host's `node_modules`.

---

## Extension UI Kit

The kit at [`frontend/src/lib/components/kit/`](../frontend/src/lib/components/kit) is the layer extensions compose their UI from. Theme tokens, keyboard navigation, density, accent-bar selection, avatar palette, dialog interactions — all are baked in, **matching mail's behavior 1-for-1**. Your extension provides data and callbacks, the kit owns rendering, and the end user gets a UX indistinguishable from the rest of Aerion.

### Why the UI kit exists

Extensions need to look and behave like the rest of Aerion — same keys, same focus rules, same scrolling, same dialog interactions. Modifying mail's code (`MessageList.svelte`, `Sidebar.svelte`, `ConversationViewer.svelte`, etc.) to share components directly carries too much regression risk to do that way. The kit is the mechanism for getting cohesion without touching mail. **It's not an alternative design — it's the necessary copy of mail's UX, made consumable by extensions.**

### The 1-for-1 rule

Every kit primitive (`Avatar`, `PaneLayout`, `ListPane`, `ListRow`, `ListHeader`, `ResponsiveSidebarToggle`, `SidebarFrame`, `SourceSidebar`, `SourceItem`, `SidebarAddItem`, `DetailPane`, `ConfirmDialog`, `ColorPicker`, `OAuthCredsSlotEditor`, …) is a behavioral replica of how the equivalent functionality works in mail today: same key bindings, same focus semantics, same scroll-into-view, same edge-case behavior. The backwards-compat test: **if mail were ever refactored to consume the kit, the user should see zero difference**. If you can't pass that test on a kit primitive you're writing, you've diverged.

**Greenfield exception (R25).** Some kit primitives have no mail equivalent — Calendar's `DetailOverlay`, for example, since mail's viewer is a flex-chain pane, not a fixed overlay. Per [`EXT_RULES.md` R25](./EXT_RULES.md), kit is an extension-driven SDK; when mail has no counterpart, the primitive is designed cleanly from the consumer's needs. The 1-for-1 rule applies to primitives that DO have a mail counterpart (`SidebarAddItem` ↔ mail's "+ Add Account" inline button, `ConfirmDialog` ↔ mail's confirms, etc.). Greenfield primitives are still bound by the kit's general conventions: theme tokens, density-aware sizing, layout-store responsive handling, `shortcuts.ts` predicates for keys, and **no imports from mail's `components/{list,sidebar,viewer}/` namespace**.

**Practical consequence: read the mail equivalent before implementing a kit primitive.** Don't infer behavior. Don't reach for a generic third-party pattern. Open `MessageList.svelte` / `Sidebar.svelte` / `ConversationViewer.svelte` / the relevant `ui/` host primitive and study how it handles keyboard, focus, scroll, and edge cases. Then match that behavior in the kit.

Visual consistency is also preserved at the **theme layer**. The kit's `Avatar` uses the same `.avatar-1..14` CSS classes (defined in [`frontend/src/themes/_utilities.css`](../frontend/src/themes/_utilities.css)) that mail's avatar uses, so colors match. Same applies to all theme tokens (`bg-muted`, `border-border`, `text-foreground`, `bg-accent`, `text-primary`).

When the host primitive has a bug that affects the kit, **fix it at the host layer** so both benefit. Don't add the fix only to the kit wrapper — that creates drift and silently breaks the 1-for-1 contract. Example: `ui/confirm-dialog/ConfirmDialog.svelte` was missing `dialogGuard` registration (which prevents mail's global key handler from killing Enter/Space activation on dialog buttons). The fix landed on the host primitive, and the kit's thin pass-through inherited it automatically.

This pattern is anchored in [the lightweight-by-default motto](../README.md) — Aerion remains a simple email client for users who don't enable extensions, and extensions opt-in to features at the cost of weight. Mail must never carry kit overhead.

### Keyboard bridge

Shortcut KEY definitions live in [`frontend/src/lib/keyboard/shortcuts.ts`](../frontend/src/lib/keyboard/shortcuts.ts) — a **single source of truth** for "what key combo matches what action." Both mail's handler (`App.svelte`) and kit components import the same predicates (`KEY.LIST_NEXT`, `KEY.LIST_PREV`, `KEY.LIST_OPEN`, etc.) and reference them via `if (KEY.LIST_NEXT(e)) { ... }`.

The **implementations differ per layer** — mail dispatches via concrete component refs; kit components handle their own events locally via `tabindex=0` + DOM `keydown` listener + `e.stopPropagation()`. The bridge is the file of predicates, not shared dispatch logic.

**Rebinding a key**: change the predicate in `shortcuts.ts`. Both mail and any kit consumer pick up the new binding automatically.

**Active-extension guard**: when an extension is the active rail pane, mail-domain shortcuts (Ctrl+R reply, Ctrl+K archive, Ctrl+J spam, Ctrl+L load-images, Ctrl+U mark-read, Ctrl+A, Ctrl+S, Ctrl+F) no-op via an `isMailActive()` check in `App.svelte`. Global shortcuts (Ctrl+Q quit, Ctrl+N compose, Ctrl+Tab rail-switch) fire regardless. Kit's keydown handlers run first when DOM-focused, so they see the events before the global handler does.

### Components

#### `Avatar` — colored initials circle

[`frontend/src/lib/components/kit/Avatar.svelte`](../frontend/src/lib/components/kit/Avatar.svelte)

```svelte
<Avatar email={contact.email} name={contact.name} density="standard" />
```

| Prop | Type | Notes |
|---|---|---|
| `email` | `string` | Color-hash seed. Same email → same color across mail and the kit. |
| `name` | `string?` | Initials source; falls back to email. |
| `density` | `'micro' \| 'compact' \| 'standard' \| 'large'` | Sizes: 24px / 28px / 32px / 40px. |
| `size` | `number?` | Override the density-derived pixel size. |

Inside the kit, treat `density` as the standard prop; only override `size` when a specific layout demands it.

#### `ListPane` + `ListRow` — keyboard-navigable list

[`frontend/src/lib/components/kit/ListPane.svelte`](../frontend/src/lib/components/kit/ListPane.svelte) and [`ListRow.svelte`](../frontend/src/lib/components/kit/ListRow.svelte)

```svelte
<ListPane
  items={contacts}
  selectedId={selected}
  focusSlot="messageList"
  label="Contacts"
  onSelect={(id) => select(id)}
>
  {#snippet row(c, { selected })}
    <ListRow {selected} onclick={() => select(c.id)}>
      <Avatar email={c.email} name={c.name} />
      <span class="flex flex-col flex-1 min-w-0">
        <span class="font-medium truncate">{c.name}</span>
        <span class="text-xs text-muted-foreground truncate">{c.email}</span>
      </span>
    </ListRow>
  {/snippet}

  {#snippet empty()}
    <p class="m-4 text-sm text-muted-foreground">No items.</p>
  {/snippet}
</ListPane>
```

**`ListPane` owns:**
- j/k/Up/Down navigation (predicates from `shortcuts.ts`)
- Enter to activate (`onActivate ?? onSelect`)
- Space to toggle check (when `onToggleCheck` provided)
- Ctrl+A to select all (when `onSelectAll` provided)
- **Delete/Backspace** — always swallowed when the list is focused (preventDefault + stopPropagation). When `onDelete` is provided, fires it with the selected id. Always swallowing — even with no handler — prevents mail's global key handler from acting on the focused message in the background.
- **Auto-scroll-into-view** on selection change (matches `MessageList.svelte`'s pattern). Uses `scrollIntoView({ block: 'nearest', behavior: 'smooth' })` so the row enters view but doesn't scroll if it's already visible.
- DOM-level focus via `tabindex=0`; registers as the focused pane's slot via `setFocusedPane(focusSlot)` when DOM-focused
- `e.stopPropagation()` when matched so the global handler doesn't double-fire

**Generic over `T extends { id: string }`** — items just need a stable `id`. The `row` snippet renderer decides everything else.

**Layout requirement**: any wrapper around `ListPane` must allow the flex children to shrink — apply `min-h-0` to the wrapper's flex column. Without it, the inner `overflow-y-auto` won't engage and the list grows past its container. Tailwind classes:

```svelte
<div class="flex-1 min-w-0 min-h-0 flex flex-col">
  <div>...toolbar...</div>
  <ListPane ... />
</div>
```

**Delete handler example:**

```svelte
<ListPane
  items={contacts}
  selectedId={selected}
  onSelect={(id) => select(id)}
  onDelete={(id) => requestDelete(id)}
>
  ...
</ListPane>
```

The `onDelete` handler typically opens a `ConfirmDialog` (see below) rather than deleting immediately — matches mail's confirmation pattern for destructive actions.

#### `SourceSidebar` + `SourceItem` — sectioned sidebar

[`frontend/src/lib/components/kit/SourceSidebar.svelte`](../frontend/src/lib/components/kit/SourceSidebar.svelte) and [`SourceItem.svelte`](../frontend/src/lib/components/kit/SourceItem.svelte)

```svelte
<SourceSidebar
  title="Contacts"
  sections={[
    { items: builtins },
    { heading: 'Sources', items: userSources },
  ]}
  selectedId={selected}
  onSelect={pick}
>
  {#snippet item(it, { active })}
    <SourceItem icon={it.icon} label={it.label} {active} onclick={() => pick(it.id)} />
  {/snippet}
</SourceSidebar>
```

**`SourceSidebar` owns:**
- Sectioned layout with optional headings
- j/k/Up/Down navigation across the flattened item list
- Enter to re-select current
- DOM-level focus; registers as `'sidebar'` slot by default (override via `focusSlot` prop)
- **Chrome via `SidebarFrame`**: container styling, narrow-mode responsive overlay (slide-in + back button), title rendering, and the body-scroll layout are all delegated to the kit's `SidebarFrame` primitive (see below). SourceSidebar wraps that chrome with its keyboard nav, focus-slot integration, and section/item rendering — but doesn't own the visual shell anymore. The benefit: when sidebar-wide settings (density, etc.) land on `SidebarFrame`, all SourceSidebar consumers inherit them automatically.

#### `SidebarFrame` — sidebar chrome primitive

[`frontend/src/lib/components/kit/SidebarFrame.svelte`](../frontend/src/lib/components/kit/SidebarFrame.svelte)

Lower-level kit primitive owning *only* the visual chrome of an extension sidebar: container styling (width, border, background), narrow-mode responsive overlay (slide-in + auto-injected back button), optional title `<h2>`, body slot (scrolling), and optional footer slot (sticky bottom strip). Used directly by extensions whose row models don't fit `SourceSidebar` (e.g. Calendar's multi-toggle visibility), and used internally by `SourceSidebar` itself so both paths share one chrome implementation.

```svelte
<SidebarFrame title={$_('calendar.sidebar.title')}>
  {#snippet body()}
    <!-- consumer's row list -->
  {/snippet}
  {#snippet footer()}
    <!-- optional sticky bottom strip (consumer owns padding + border) -->
  {/snippet}
</SidebarFrame>
```

| Prop | Type | Notes |
|---|---|---|
| `title` | `string?` | Rendered as `<h2 class="text-lg font-semibold mb-3 px-4">`. Omit for sidebars without a title. |
| `label` | `string?` | ARIA label for the `<aside>`. Defaults to `title`. |
| `body` | `Snippet` | Required. The scrollable middle. SidebarFrame wraps it in `flex-1 min-h-0 overflow-y-auto`. |
| `footer` | `Snippet?` | Optional bottom strip. Pinned with `shrink-0`. Consumer owns the strip's chrome (border-t, padding, content). |
| `containerRef` | `bindable HTMLElement \| null` | Bind to access the outer `<aside>`. SourceSidebar uses this for its tabindex-based keyboard focus. |
| `focusable` | `boolean?` | When true, the `<aside>` gets `tabindex="0"`. SourceSidebar passes true; row-only consumers leave false. |
| `class` | `string?` | Extra class string appended to the outer `<aside>`. SourceSidebar uses this for `pane-focus-flash`. |
| `onkeydown` / `onfocus` / `onmousedown` | `(e) => void`? | DOM event handlers forwarded to the outer `<aside>`. |

**Layout model**: title is non-scrolling (sticky at the top), body fills the remaining space with its own overflow-y-auto, optional footer pins at the bottom. This split is intrinsic to the primitive — it's what enables consumers like Calendar to pin a sync-indicator + settings cog strip below a scrolling list.

**Why this primitive exists**: SidebarFrame is the kit's single hook for upcoming cross-extension sidebar settings — density (like the existing message-list density), font-size variants, etc. When those settings ship, the density-aware classes live inside `SidebarFrame`, and all consumers (calendar directly, contacts via `SourceSidebar`'s internal composition, future extensions) inherit them automatically. No per-extension wiring required.

**Greenfield primitive (R25)** — no 1-for-1 mail counterpart at this abstraction level; mail's `Sidebar.svelte` has app-level `TitleBar` chrome and a different idiom. Sits in the §"The 1-for-1 rule" greenfield-exception lane.

#### `SidebarAddItem` — "+ Add …" entry for sidebar lists

[`frontend/src/lib/components/kit/SidebarAddItem.svelte`](../frontend/src/lib/components/kit/SidebarAddItem.svelte)

```svelte
<SidebarAddItem
  label={$_('calendar.sidebar.addSource')}
  onclick={() => { showAddSource = true }}
/>
```

| Prop | Type | Notes |
|---|---|---|
| `label` | `string` | Required. Button text — i18n it at the call site. |
| `icon` | `string?` | Defaults to `'mdi:plus'`. Any `@iconify/svelte` icon name. |
| `onclick` | `() => void` | Required. Invoked on click. |

1-for-1 replica of mail's "+ Add Account" inline button at [`frontend/src/lib/components/sidebar/Sidebar.svelte:606-615`](../frontend/src/lib/components/sidebar/Sidebar.svelte) — same styling (`w-full flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground hover:text-foreground hover:bg-muted/50 rounded-md transition-colors`), same `px-3 py-2` outer wrapper, same `mdi:plus` default icon.

**Intended placement**: at the bottom of the scrollable source/account list, BELOW the last source row — NOT in a separate footer strip. The primitive does not draw a divider; pair it with whatever sync-status / settings-cog footer chrome the sidebar already has (Calendar's `CalendarSidebar` is the reference: scrollable list with sources + `SidebarAddItem`, then a separate `border-t` footer with sync indicator + settings cog).

Pairs with both manually-rolled sidebars (Calendar's case — `SourceSidebar`'s single-select keyboard nav didn't fit its multi-toggle visibility semantics) and `SourceSidebar`-based sidebars (render `SidebarAddItem` outside the `SourceSidebar` so its keyboard nav doesn't try to focus the button).

#### `DetailPane` — header/body/empty-state shell

[`frontend/src/lib/components/kit/DetailPane.svelte`](../frontend/src/lib/components/kit/DetailPane.svelte)

```svelte
<DetailPane empty={!contact} emptyIcon="mdi:account-multiple-outline" emptyText="Select a contact.">
  {#snippet header()}
    <Avatar email={contact.email} name={contact.name} density="large" />
    <h1 class="text-xl font-semibold">{contact.name}</h1>
  {/snippet}
  {#snippet body()}
    <dl>...</dl>
  {/snippet}
</DetailPane>
```

Read-only shell — no keyboard ownership. Header is fixed; body scrolls. Empty-state can be customized via snippet or just `emptyIcon`/`emptyText` props.

`DetailPane` is **self-managed responsive** — it reads `getLayoutMode` / `getResponsiveView` / `hideViewer` directly from `$lib/stores/layout.svelte` and applies `responsive-viewer-overlay` + `responsive-viewer-visible` to its outer `<section>` automatically when below the medium breakpoint. A back-arrow button is injected at the start of the header in narrow mode (calls `hideViewer`). Consumers don't pass responsive props or onBack handlers — the kit handles it.

#### `DetailOverlay` — right-side detail panel with focus mode

[`frontend/src/lib/components/kit/DetailOverlay.svelte`](../frontend/src/lib/components/kit/DetailOverlay.svelte)

```svelte
<DetailOverlay
  open={selectedEventId !== null}
  focused={eventFocusMode === 'event'}
  title={selectedEvent?.summary}
  onClose={() => calendarView.selectEvent(null)}
  onToggleFocus={() => calendarView.toggleEventFocus()}
>
  {#snippet children()}
    <EventDetail eventId={selectedEventId} />
  {/snippet}
</DetailOverlay>
```

| Prop | Type | Notes |
|---|---|---|
| `open` | `bindable boolean` | Drives mount + slide-in via `transition:fly`. Flip to false to dismiss. |
| `focused` | `bindable boolean` | Drives full-window expansion. The kit toggles this through `onToggleFocus`; consumers may also flip it directly. |
| `title` | `string?` | Shown in the header — used most visibly in responsive mode where it sits next to the back button. |
| `onClose` | `() => void?` | Called when the close button, responsive back button, or Esc-while-not-focused dismisses the overlay. |
| `onToggleFocus` | `() => void?` | Called by the fullscreen toggle button OR Esc-while-focused (which exits focus rather than dismissing). |
| `children` | `Snippet?` | The body content. Rendered inside an `overflow-y-auto` container. |

**Greenfield primitive (R25)** — no mail counterpart. Mail's `ConversationViewer` is part of the 3-column flex chain, not a fixed overlay. The 1-for-1 rule's backwards-compat test does not apply here; see the [Greenfield exception in §"The 1-for-1 rule"](#the-1-for-1-rule).

**Three positioning modes**, driven by `focused` and `isResponsive()` from `$lib/stores/layout.svelte`:
- **Regular desktop** (`focused=false`, not responsive): `position: fixed; right:0; top:0; bottom:0; w-[340px]`. Right-anchored sidebar overlay. **No scrim** — the view underneath stays interactive, so consumers can swap the children content (e.g., select a different row in the underlying list) without dismissing the overlay.
- **Focused desktop** (`focused=true`): `position: fixed; inset:0`. Full-window. Reuses the same component tree; just a class swap with a 200ms ease-out transition.
- **Responsive** (narrow breakpoint, regardless of `focused`): `position: fixed; inset:0` AND a back button auto-injected at the start of the header (calls `onClose`). Mirrors mail's `MessageViewer` responsive back-button placement.

**Header chrome**: title text in the center; focus toggle (`mdi:fullscreen` / `mdi:fullscreen-exit`) and close (`mdi:close`) buttons on the right.

**Esc handling**: while `open`, a window-level keydown listener intercepts Escape. If `focused=true`, it calls `onToggleFocus` (exits focus, stays open). Otherwise it calls `onClose` (dismisses). `preventDefault` + `stopPropagation` so mail's global handler and dialog handlers don't double-fire.

**Containing-block caveat** (`position: fixed`): the overlay positions relative to the viewport ONLY when no ancestor has `transform`, `filter`, `perspective`, `contain: layout`/`contain: paint`, or `will-change` set to anything other than `auto`. The current ancestor chain (App.svelte → rail → extension pane) has none of these. Before adding any of those properties to a wrapping div, test the overlay or it will mis-position. Captured in the component's own header comment as well.

**Animation**: slide-in from the right via `transition:fly={{ x: 360, duration: 200, easing: cubicOut }}` on mount; reverse on unmount. Mounted-and-state-swap (open→open with different children data) does NOT re-animate — the children snippet just re-renders in place.

#### `PaneLayout` — outer container for 3-column extension panes

[`frontend/src/lib/components/kit/PaneLayout.svelte`](../frontend/src/lib/components/kit/PaneLayout.svelte)

```svelte
<PaneLayout>
  <ContactsSidebar />   <!-- wraps kit's SourceSidebar -->
  <ContactList />        <!-- wraps kit's ListPane + ListHeader -->
  <ContactDetail />      <!-- wraps kit's DetailPane -->
</PaneLayout>
```

The canonical wrapper for kit-based 3-column extension panes. Provides:
- A `relative` + `overflow-hidden` positioning context that anchors the kit primitives' `responsive-sidebar-overlay` / `responsive-viewer-overlay` and **clips their off-screen hit-test regions so sibling components (notably `ExtensionRail`) remain clickable in narrow mode**. The `overflow-hidden` is load-bearing — without it the off-screen sidebar's leftward translation leaks into the rail's column.
- The narrow-mode `responsive-scrim` rendered as a sibling overlay; click dismisses the sidebar.

Zero props. Just compose your three kit-based panes inside. Extensions don't import the layout store, manage scrim state, or wire pane-class merging — that's all internal.

#### `ListHeader` — canonical list-column toolbar

[`frontend/src/lib/components/kit/ListHeader.svelte`](../frontend/src/lib/components/kit/ListHeader.svelte)

```svelte
<ListHeader
  label={headerLabel}                       /* extension-computed $derived from sidebar selection */
  count={contactsView.contacts.length}
  searchMode={showSearch}
>
  {#snippet search()}
    <!-- consumer's search input + clear button -->
  {/snippet}
  {#snippet actions()}
    <!-- consumer's sort / add / extra buttons -->
  {/snippet}
</ListHeader>
```

Owns:
- Toolbar wrapper styling (`flex items-center justify-between px-4 py-3 border-b border-border`) so every kit consumer's list column shares mail's `MessageList` toolbar rhythm.
- Leading `<ResponsiveSidebarToggle />` auto-included.
- `<h2>` title + count badge layout.
- Search-mode swap (when `searchMode === true`, the title area is replaced by the consumer's `search` snippet).
- Trailing `actions` snippet for per-extension toolbar buttons.

Does **not** own:
- The label value (extension knows about sources/folders/categories — pass a `$derived` that tracks the active sidebar selection so the title is dynamic, not static).
- Search input markup (debounce, refs, clear-button logic stays in the consumer).
- The action buttons themselves (sort / add / etc. are extension-specific).

#### `ResponsiveSidebarToggle` — hamburger for narrow mode

[`frontend/src/lib/components/kit/ResponsiveSidebarToggle.svelte`](../frontend/src/lib/components/kit/ResponsiveSidebarToggle.svelte)

```svelte
<ResponsiveSidebarToggle />
```

Zero-prop drop-in. Renders nothing when not narrow; renders an `mdi:dock-left` icon button when narrow that fires `showSidebar()` on click. Auto-included inside `ListHeader` so extensions composing the canonical toolbar don't need to mount this directly — it appears here in the kit's component list only for the case where an extension renders its own custom toolbar and wants the canonical hamburger placement.

#### `ConfirmDialog` — destructive-action confirmation

[`frontend/src/lib/components/kit/ConfirmDialog.svelte`](../frontend/src/lib/components/kit/ConfirmDialog.svelte)

```svelte
<ConfirmDialog
  bind:open={showDeleteConfirm}
  title="Delete this contact?"
  description={`${contact.name} will be removed from your local contacts.`}
  confirmLabel="Delete"
  cancelLabel="Cancel"
  variant="destructive"
  loading={deleting}
  onConfirm={confirmDelete}
/>
```

| Prop | Type | Notes |
|---|---|---|
| `open` | `bindable boolean` | Two-way bound — flip to false to close, or call cancel inside `onConfirm`. |
| `title` | `string` | Dialog heading. |
| `description` | `string` | Body text — full sentence describing what will happen. |
| `confirmLabel` | `string?` | Default: `"Confirm"`. |
| `cancelLabel` | `string?` | Default: `"Cancel"`. |
| `variant` | `'default' \| 'destructive'?` | `destructive` applies red styling to the confirm button. |
| `loading` | `boolean?` | Show spinner on confirm + disable both buttons. |
| `onConfirm` | `() => void` | Required. Called on confirm-button click or Enter. |
| `onCancel` | `() => void?` | Called when cancel button, Escape, or click-outside dismisses the dialog. |

Pass-through to the host's [`ui/confirm-dialog/ConfirmDialog.svelte`](../frontend/src/lib/components/ui/confirm-dialog/ConfirmDialog.svelte). Same component mail uses for its permanent-delete and empty-trash confirms — behavior is identical, including Enter/Space activation, Escape to cancel, and focus trap. Extensions consume the kit version so they don't reach into the host's `ui/` namespace; the host can swap its underlying primitive (bits-ui today, anything else later) without breaking extensions.

The dialog registers with [`dialogGuard`](../frontend/src/lib/stores/dialogGuard.ts) while open, which makes mail's global key handler in `App.svelte` step out of the way. Without that guard, Enter/Space on dialog buttons get `preventDefault`'d by mail's button-pane disambiguation logic.

**If your extension defines its own custom dialog** (one that doesn't go through the kit's `ConfirmDialog`), you MUST register `dialogGuard` yourself or Enter/Space activation on the dialog's buttons will be killed by mail's global key handler. Match the convention every mail dialog uses ([`SettingsDialog.svelte:87–92`](../frontend/src/lib/components/settings/SettingsDialog.svelte), [`AccountDialog.svelte:140–141`](../frontend/src/lib/components/settings/AccountDialog.svelte)):

```svelte
<script lang="ts">
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'

  let { open = $bindable(false) }: Props = $props()

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })
</script>
```

The bits-ui Root wrappers (`ui/dialog/Dialog`, `ui/alert-dialog/AlertDialog`) deliberately don't register on their own — the convention is "consumer owns it" so registration only happens when the dialog is actually open, not just rendered.

#### `ColorPicker` — preset palette + hex input

[`frontend/src/lib/components/kit/ColorPicker.svelte`](../frontend/src/lib/components/kit/ColorPicker.svelte)

```svelte
<ColorPicker
  value={cal.color ?? ''}
  onchange={(hex) => calendarSources.setColor(cal.id, hex)}
/>
```

| Prop | Type | Notes |
|---|---|---|
| `value` | `string` | Current color as a 7-char hex (e.g. `"#3B82F6"`). Empty string is allowed and renders the first preset. |
| `onchange` | `(color: string) => void` | Called whenever the user picks a preset or enters a valid hex (`/^#[0-9A-Fa-f]{6}$/`). Not called on partial / invalid input. |

Thin pass-through to the host's [`ui/color-picker/ColorPicker.svelte`](../frontend/src/lib/components/ui/color-picker/ColorPicker.svelte) — the same component mail's [`AccountGeneralTab.svelte`](../frontend/src/lib/components/settings/account/AccountGeneralTab.svelte) and [`AccountForm.svelte`](../frontend/src/lib/components/settings/AccountForm.svelte) consume. 1-for-1 with mail's account-color picker: 8 preset swatches in a 4×2 grid, manual hex entry with auto-`#` prefix, popover dismisses on click-outside or Enter. Trigger button is `w-8 h-8` (fixed). Extensions consume the kit version so they don't reach into the host's `ui/` namespace; the host can swap the underlying primitive without breaking extensions.

The host primitive's `aria.selectColor` and `aria.selectPresetColor` i18n keys are read from the core `aria.*` namespace at [`frontend/src/lib/i18n/locales/en.json`](../frontend/src/lib/i18n/locales/en.json) — extensions don't need to declare them.

### Extension keyboard shortcuts

Extensions register their own pane-local keyboard shortcuts through a small registry. Mail's global key handler in `App.svelte` calls `dispatchExtensionShortcut(e)` before its own mail-domain switch; when the active rail pane is NOT mail, the dispatcher walks the active extension's registered predicates and invokes the first match.

**Where things live**:

| File | Owner | Purpose |
|---|---|---|
| [`frontend/src/lib/keyboard/shortcuts.ts`](../frontend/src/lib/keyboard/shortcuts.ts) | host | Predicates shared by mail AND the kit (`LIST_NEXT`, `LIST_DELETE`, `PANE_FOCUS_*`, etc.) + the composable mod-state helpers (`noMods`, `ctrlOrMeta`, `altOnly`). Exported so extensions compose their predicates against the same helpers. |
| [`frontend/src/lib/stores/extensionShortcuts.svelte.ts`](../frontend/src/lib/stores/extensionShortcuts.svelte.ts) | host | The registry — `registerExtensionShortcut(extensionId, predicate, handler)` + `dispatchExtensionShortcut(e)`. |
| `extensions/<name>/frontend/keyboard/shortcuts.ts` | extension | Predicates owned by that extension. Extension imports the host helpers and exports its own KEY namespace. |

**Defining an extension shortcut**:

```ts
// extensions/contacts/frontend/keyboard/shortcuts.ts
import { noMods } from '$lib/keyboard/shortcuts'

/** `e` — edit the currently-focused contact. */
export const CONTACT_EDIT = (e: KeyboardEvent): boolean =>
  e.key === 'e' && noMods(e)

export const KEY = { CONTACT_EDIT }
```

**Registering at component mount**:

```ts
import { onMount, onDestroy } from 'svelte'
import { registerExtensionShortcut } from '$lib/stores/extensionShortcuts.svelte'
import { KEY } from '$extensions/contacts/frontend/keyboard/shortcuts'

const unreg = registerExtensionShortcut('contacts', KEY.CONTACT_EDIT, () => {
  const id = contactsView.selectedContactId
  if (id) openEditDialog(id)
})
onDestroy(unreg)
```

The registration is scoped to the extension's id — the dispatcher only fires it when `getActiveExtension() === 'contacts'`. Multiple shortcuts per extension are supported and evaluated in registration order; first match wins.

**Important rules**:

- **Register at the highest pane component** (e.g., the extension's root pane `ContactsPane.svelte`, not the leaf `ContactList.svelte`). That way the shortcut survives across re-renders of inner components and remains active whenever the pane is mounted.
- **Always call the returned Unregister** from `onDestroy` (or equivalent cleanup). Without it, repeated mount/unmount cycles pile up stale handlers.
- **Inputs are excluded automatically**: the host dispatcher checks `inInput` before invoking extension shortcuts, so the shortcut doesn't fire while the user is typing in a text field.
- **Dialog guard suppresses extension shortcuts too**: when a `ConfirmDialog` or other guarded dialog is open, the host handler bails before the dispatcher runs. Same as mail's behavior with its own dialogs.
- **Mail-side shortcuts stay**. Extension shortcuts only run when the active rail pane is the extension. Mail's own shortcuts (`Ctrl+R`, `Ctrl+K`, `j/k` via window handler when no kit pane is focused, etc.) continue to fire when the rail pane is mail.
- **Use shared helpers** from `$lib/keyboard/shortcuts` (`noMods`, `ctrlOrMeta`, `altOnly`) to define predicates. Match mail's modifier-checking conventions exactly — that's the 1-for-1 rule applied to keyboard.

**Why the registry instead of inline dispatch**: the registry shape is what lets the host's global key handler stay extension-agnostic. App.svelte doesn't need to know about every extension's shortcuts — it just defers to whichever extension is active. Adding a new extension means adding the extension's own shortcut file + registering at mount; no host changes.

### Pane focus slots

The kit reuses Aerion's existing pane-focus store at [`frontend/src/lib/stores/keyboard.svelte.ts`](../frontend/src/lib/stores/keyboard.svelte.ts). The slot type is `'sidebar' | 'messageList' | 'viewer'` — those names are kept as-is for backward compatibility with mail's existing focus dispatch. Extension panes register against these same slots:

| Slot | Mail occupant | Kit equivalent |
|---|---|---|
| `'sidebar'` | `Sidebar.svelte` (folder tree) | `SourceSidebar.svelte` |
| `'messageList'` | `MessageList.svelte` | `ListPane.svelte` |
| `'viewer'` | `ConversationViewer.svelte` | `DetailPane.svelte` |

Alt+H/L pane cycling already cycles through these three slot names — when an extension is active, the kit components take focus and the cycling works uniformly with mail.

### Extending the kit

When a future extension needs a primitive that doesn't exist yet (e.g., Calendar's grid view):

1. **Find the mail equivalent first.** Open the matching `frontend/src/lib/components/{list,sidebar,viewer,ui,...}/` file and study how it handles keyboard, focus, scroll-into-view, and edge cases. The 1-for-1 rule starts here. **If mail has no equivalent** (e.g., overlays that aren't part of the flex chain), the primitive is greenfield per [R25](./EXT_RULES.md) — design from the consumer's needs while respecting kit conventions (theme tokens, density-aware sizing, layout-store responsive handling, `shortcuts.ts` predicates). See `DetailOverlay` for an example.
2. **Build the kit primitive to match that behavior exactly.** Where the host already has a working primitive in `ui/` (`Button`, `Input`, `Dialog`, `AlertDialog`, etc.), wrap it as a thin pass-through — see [`ConfirmDialog.svelte`](../frontend/src/lib/components/kit/ConfirmDialog.svelte) for the canonical example. Where the kit has to copy (j/k navigation, accent-bar selection, density), copy faithfully and reference the same `shortcuts.ts` predicates.
3. **Don't reach into mail's components** (`frontend/src/lib/components/{list,sidebar,viewer}/`). Those are the live mail UI, not reusable primitives. Copy the pattern, don't import it.
4. **If you find a bug in the host primitive that affects the kit, fix it at the host layer** so mail benefits too. Don't patch the kit wrapper — that creates drift that breaks the 1-for-1 contract. Same code paths, same behavior, same fixes.
5. **Add new shortcut predicates to `shortcuts.ts`** if introducing new keys. Both mail and any kit consumer should reference the same predicate.
6. Document the component here with prop table + minimal usage example.
7. **Verify the lightweight invariant**: with the new component built but no extension enabled, htop should show no Aerion/webkit2gtk activity. The kit must be lazily mounted only when an extension is active.

---

## Write capability

Phase 2b introduces write capability to extensions. Reads continue through Aerion core's existing data paths (mail OAuth + per-source CardDAV creds); writes go through a parallel per-extension OAuth path.

### Per-extension OAuth client configs

Each first-party extension that needs OAuth writes owns its OWN client config slot, with its own credentials, injected at build time from the extension's package — Aerion core compiles in only `*-mail`.

```
google-mail            ← Aerion core (mail + contacts READ via existing grant)
microsoft-mail         ← Aerion core
google-contacts        ← Contacts extension (WRITE only)
microsoft-contacts     ← Contacts extension
google-calendar        ← Calendar extension (READ + WRITE; future)
microsoft-calendar     ← Calendar extension (future)
```

Each extension's package contains:
- `extensions/<name>/manifest.json` — declares the extension
- `extensions/<name>/manifest.go` — embeds the manifest JSON

**No per-extension OAuth credentials live in extension packages.** All slot resolution is centralized in [`internal/oauth2/core_provider.go`](../internal/oauth2/core_provider.go), backed by three build-time ldflags variable pairs in `internal/oauth2/config.go`:

| Variable | Slots backed | Surfaced in picker as | Notes |
|---|---|---|---|
| `GoogleClientID` / `GoogleClientSecret` | `google-mail` | "Aerion - Google" | Mail's Google-verified project. Also routable for scopes that an extension manifest lists in `first_party_uses_core_for_scopes` (today: contacts.readonly). |
| `GoogleTestingClientID` / `GoogleTestingClientSecret` | `google-contacts`, `google-calendar` | "Aerion - Google (Testing)" | **Single shared un-Google-verified test project** that backs every first-party extension needing broader Google scopes (contacts.readwrite, full Calendar). When the mail project eventually gets verified for those scopes, the default in the picker UI switches to "Aerion - Google" and this slot becomes a fallback. |
| `MicrosoftClientID` | `microsoft-mail`, `microsoft-contacts`, `microsoft-calendar` | "Aerion - Microsoft" | One Azure AD app registration covers all three surfaces. Microsoft Graph doesn't gate scopes behind verification, so adding `Contacts.ReadWrite` / `Calendars.ReadWrite` to the existing mail registration is free. |

ldflags injection happens via the root `Makefile`'s LDFLAGS rules from the root `.env` / `.env.local`. Extension packages stay focused on domain logic — no `creds.go`, no `OAuthClients()`, no per-extension env file. If a slot's underlying variable is empty, the slot resolves to `(zero, false)` and the picker UI omits the corresponding option.

### Manifest OAuth routing — `first_party_uses_core_for_scopes`

When an extension calls `core.Auth().HTTPClient(accountID, scopes)`, the Auth Broker reads the calling extension's manifest to decide whether each scope:

- **Routes to Aerion core's mail OAuth** (`<provider>-mail`) — listed in `manifest.oauth.first_party_uses_core_for_scopes`. Reuses the user's existing mail consent; no new OAuth prompt. Only viable for scopes the user's mail OAuth already covers.
- **Routes to the extension's own creds** (`<provider>-<extensionID>`) — NOT listed. If the account lacks those scopes under the extension's config, broker returns `*coreapi.ErrAdditionalConsentRequired`; the host runs an incremental-consent flow.

```jsonc
// Contacts: READ piggybacks on mail OAuth, WRITE uses own creds
{
  "id": "contacts",
  "oauth": {
    "first_party_uses_core_for_scopes": [
      "https://www.googleapis.com/auth/contacts.readonly",
      "Contacts.Read"
    ]
  }
}

// Calendar: nothing overlaps with mail OAuth — everything uses own creds
{
  "id": "calendar",
  "oauth": {
    "first_party_uses_core_for_scopes": []
  }
}
```

Mixed-scope calls (some routing to core, some to extension) are REJECTED — the extension must split into two HTTPClient calls.

**THE GATE**: `first_party_uses_core_for_scopes` is honored ONLY for first-party extensions. If a community-extension intake ever opens, community extensions declaring this field will fail manifest validation upstream. Handing community extensions the user's mail OAuth tokens would be a privilege-escalation vector — capped at the manifest boundary.

### User-supplied OAuth credentials (override UI)

Users can paste their own Client ID + Secret per slot via Aerion's settings:

- **Aerion core's `*-mail` slots** → Settings → Accounts → "OAuth Credentials (advanced)" disclosure (collapsed by default). See [`AerionCoreOAuthSection.svelte`](../frontend/src/lib/components/settings/AerionCoreOAuthSection.svelte).
- **Per-extension slots** → that extension's own settings dialog. See [`ContactsSettingsDialog.svelte`](../extensions/contacts/frontend/components/ContactsSettingsDialog.svelte) for the canonical layout.

Both UIs use the same shared primitive [`kit/OAuthCredsSlotEditor.svelte`](../frontend/src/lib/components/kit/OAuthCredsSlotEditor.svelte) (composed from existing `ui/input`, `ui/button`, `ui/select`, `ui/confirm-dialog` — no new low-level inputs). The picker shows an enumerated set of choices returned by `App.GetOAuthCredsChoices(configID, extensionID)`. The choice IDs are stable and persisted via `App.SetOAuthCredsChoice(configID, choiceID)`:

- **`custom`** — user-supplied Client ID + Secret. Always available. Selecting reveals the edit form; saving writes to the `user_oauth_clients` table.
- **`aerion-shipped`** — the slot's own built-in client (compiled in via the extension's `.env` ldflags). Only listed when the slot's shipped creds are populated. Label is per-slot:
  - `google-contacts` / `google-calendar` → **"Aerion testing"** (un-Google-verified).
  - `microsoft-*` slots → **"Aerion - Microsoft"** (backed by mail's client after the core consolidation).
  - `<provider>-mail` → **"Aerion - Google"** / **"Aerion - Microsoft"** (mail's own settings UI).
- **`aerion-mail`** — reuse the core `<provider>-mail` slot's client for this extension. Only listed when the extension's manifest declares the provider's scopes in `first_party_uses_core_for_scopes` AND the mail slot has shipped creds. Today only Google contacts qualifies (mail's verified client carries `contacts.readonly`).

Resolution order in `oauth2.ClientConfigForID(configID)`:
1. User override (`oauth2.UserOverrideLookup`) — Settings UI `custom` choice.
2. User-set slot alias (`oauth2.SlotAliasLookup`) — Settings UI `aerion-mail` choice. Recursive lookup on the target slot id, capped at one hop.
3. Registered `CredentialsProvider` chain (Aerion core's, then each extension's).
4. `(zero, false)` → triggers `ErrAdditionalConsentRequired` or "no creds available" UX.

Storage: encrypted via `credentials.Store` (OS keyring primary, encrypted DB fallback). Custom Client IDs/Secrets live in `user_oauth_clients` ([`internal/credentials/oauth_user_creds.go`](../internal/credentials/oauth_user_creds.go)); slot aliases live in `user_oauth_slot_aliases` ([`internal/credentials/oauth_slot_alias.go`](../internal/credentials/oauth_slot_alias.go)).

### Per-extension settings dialog

Extensions register their settings dialog via `core.UI().RegisterSettingsTab(...)`. The host dispatcher [`ExtensionSettingsDialog.svelte`](../frontend/src/lib/components/settings/ExtensionSettingsDialog.svelte) opens the matching dialog (static dispatch by extension ID — same pattern as account-setup hooks).

Two entry paths:
1. **Explicit Edit button** in Settings → Extensions → row (when the extension is enabled)
2. **Extension-driven auto-open** via `openExtensionSettings(extensionId)` — the extension's frontend code can open its own settings dialog when needed (e.g., on pane mount when the extension detects it's missing OAuth creds for write capability)

### Write-access grant flow (account-picker model)

When the user wants to enable writes on a Google or Microsoft contacts source, the UI is explicit: a dialog asks which existing Aerion auth identity to attach the new write grant to. No silent retries on access-denied; no inline "consent required" dialogs popping up mid-write.

**Frontend.** [`WriteAccessAccountPicker.svelte`](../frontend/src/lib/components/oauth/WriteAccessAccountPicker.svelte) is the canonical UI. It's a generic dialog that takes `provider` (`'google'` or `'microsoft'`), `sourceID`, and `sourceName`. On open it fetches `App.ListAuthContextsForProvider(provider)` to populate the radio list. The list is the union of:

- Mail accounts of that provider (from the host's account store)
- Standalone contacts sources of that provider (from `carddavStore.ListSources()` where `AccountID IS NULL`)

There is **no "Add another account"** entry. All identities must come from Aerion's core setup paths (Mail → add account, OR Contacts → add source). If the list is empty, the dialog shows a hint pointing the user to those paths.

On Continue, the dialog calls the extension's `<Extension>_EnableWriteAccess(sourceID, authContextKind, authContextIdentifier, expectedEmail)` bridge method.

**Backend.** The extension's bridge method ([`Contacts_EnableWriteAccess`](../extensions/contacts/backend/bridge.go) is the reference) does:

1. Derive the slot's `clientConfigID` and the write scope from the source's provider (e.g. `google-contacts` + `https://www.googleapis.com/auth/contacts`).
2. Build a `coreapi.StartIncrementalConsentRequest`. Set exactly one of `AccountID` (when `authContextKind == "mail"`) or `SourceID` (when `"standalone-contacts"`). Set `ExpectedEmail` to the picked identity's email. Pass through to `core.Auth().StartIncrementalConsent(req)`.
3. On `nil` return, call `core.Contacts().SetSourceWritable(sourceID, true)`.

The host's `StartIncrementalConsent` implementation:
- Opens the browser, blocks until callback fires (or `App.CancelOAuthFlow` is called).
- Passes `login_hint=<ExpectedEmail>` so the IdP account picker pre-selects the right account.
- Validates the granted email matches `ExpectedEmail`. Mismatch → discard tokens, return error.
- Persists tokens via `SetOAuthTokensForClientConfig(AccountID, slot, …)` (for mail contexts) or `SetContactSourceOAuthTokens(SourceID, …)` (for standalone contexts).

**Slot-creds detection.** Before calling `EnableWriteAccess`, the picker doesn't probe slot state — the extension does that inside its bridge method. Use `oauth2.ClientConfigForID(clientConfigID)`; it walks the user-override + registered-provider chain and returns truthy only when there are real per-slot creds. Do NOT use `oauth2.GetProvider(clientConfigID)` for this check: it silently inherits the mail-side ldflag-injected creds when the extension's slot is empty, which produces a misleading "configured" answer.

```go
slotCreds, slotOK := oauth2.ClientConfigForID(string(clientConfigID))
if !slotOK || slotCreds.ClientID == "" {
    return fmt.Errorf("no OAuth credentials configured for %q — set them up in Settings → Extensions → … → OAuth Credentials", clientConfigID)
}
```

**Why not retry-on-write?** Retry-with-consent worked for the prototype but conflated *which* identity owned the write with *whether* scopes were missing. Users with multiple Google accounts ended up granting writes to the wrong one. The picker model surfaces the identity decision up front and verifies it on the callback.

### Local-contact edit/delete

For sent-recipient (local) contacts and CardDAV contacts alike, the Contacts extension supports multi-field edit (name, emails, phones, addresses, org, title, URLs, IMPPs, categories, birthday, note, photo) and delete via [`ContactEditDialog.svelte`](../extensions/contacts/frontend/components/ContactEditDialog.svelte).

**Flow** (read the table top-to-bottom — same SDK pattern future extensions follow):

| Layer | Responsibility |
|---|---|
| Frontend (`ContactEditDialog.svelte`) | Collects multi-field form state; calls `contactsView.updateContact(id, patch)` |
| Frontend store ([`contactsView.svelte.ts`](../extensions/contacts/frontend/stores/contactsView.svelte.ts)) | Calls Wails-bound `App.Contacts_UpdateContact(id, patch)` (imported with alias `UpdateContact`) |
| Bridge method ([`extensions/contacts/backend/bridge.go`](../extensions/contacts/backend/bridge.go)) | `Contacts_UpdateContact` gates on `gateEnabled()`, calls `ensureInit()`, then delegates to the lazy-initialized `b.api.UpdateContact(id, patch)` |
| Extension API ([`extensions/contacts/backend/api.go`](../extensions/contacts/backend/api.go)) | `UpdateContact` calls `applyContactPatchToRecord(rec, patch)` to apply every non-nil patch field, then source-dispatches by source type: local → `contact.Store.UpsertRecord(rec)`; CardDAV → `writeCardDAVRecord(rec)` (PUT the full vCard); Google → `updateGoogleContact` in `google_api.go`; Microsoft → `updateMicrosoftContact` in `microsoft_api.go`. |
| Core store ([`internal/contact/store.go`](../internal/contact/store.go)) | `UpsertRecord` / `UpsertRecordTx` writes the record + all sub-tables (emails, phones, addresses, urls, impps, categories, photo). Sets `name_overridden=1` on every email when the Name field is patched, so auto-collection never clobbers a user edit. |

**Why route through the extension API instead of calling the core store directly:** writes follow the same SDK pattern as reads. CardDAV writes shipped in 2b.2 (PUT/DELETE) and 2b.2.c (POST-new). Phase 2b.3 added Google + Microsoft branches inside `extcontactsbe.API.CreateContact` / `UpdateContact` / `DeleteContact` — the per-provider logic lives in [`extensions/contacts/backend/google_api.go`](../extensions/contacts/backend/google_api.go) and [`extensions/contacts/backend/microsoft_api.go`](../extensions/contacts/backend/microsoft_api.go). NO new Wails methods, NO new direct-store call sites. Future extensions (Calendar) declare their CRUD on their own `coreapi` interface and follow the same pattern.

### Source-dispatch pattern (transferable to Calendar / future extensions)

When an extension's API needs to mutate data that lives across multiple backends (local store, CardDAV-style WebDAV, OAuth APIs), the canonical Aerion pattern is **source dispatch inside the extension's `coreapi` impl**:

```go
// extensions/<name>/backend/api.go
func (a *API) UpdateThing(id string, patch coreapi.ThingPatch) error {
    if id == "" {
        return fmt.Errorf("…: id is required")
    }
    sourceType, err := a.resolveSourceType(id) // local | carddav | google | microsoft
    if err != nil {
        return err
    }
    switch sourceType {
    case "local":
        return a.localPath(id, patch)
    case "carddav":
        return a.carddavPath(id, patch)
    case "google":
        return a.googlePath(id, patch)
    case "microsoft":
        return a.microsoftPath(id, patch)
    }
    return coreapi.ErrUnimplemented
}
```

Aerion's house style avoids `if/else` chains. Use guard clauses for early returns and `switch` for branch dispatch — both for readability and because the no-else convention is enforced project-wide (see [§ The two hard rules](#the-two-hard-rules-for-any-new-extension)).

Rules that hold across this pattern:

- The extension API ONLY routes; it doesn't gate. Capability checks (`IsExtensionEnabled`, source `writable` flag) live in the host's Wails-bound methods.
- Each provider branch starts as `ErrUnimplemented` and gets filled in when that provider's write path lands. This lets sub-phases ship independently.
- Empty/nil patch is a no-op success — callers can issue a "touch" without sending fields. Useful for refresh-driven flows.
- Patch types use pointer fields (`*string`, `*[]string`) so consumers can distinguish "leave unchanged" from "set to empty."
- Source-dispatch keys (id format, source-table joins) are extension-specific. Contacts uses `@` → local / UUID → carddav. Calendar will use its own conventions.

When Calendar lands, its `coreapi.Calendar` interface gains `CreateEvent`/`UpdateEvent`/`DeleteEvent` with `EventPatch`, dispatched by source the same way.

### Source `writable` flag

`contact_sources.writable` is a boolean tracking whether the user has write capability enabled on a given source. **New CardDAV sources default to `writable = true`** at creation time — adding a CardDAV source signals intent to use it. New OAuth-linked sources (Google/Microsoft) default to `writable = false`; the user opts in via the incremental-consent flow.

**The canonical Enable/Disable lever lives in the extension's own settings dialog**, NOT in the core source-edit dialog. For Contacts that's Settings → Extensions → Contacts → "Write Access" section (see [`ContactsSettingsDialog.svelte`](../extensions/contacts/frontend/components/ContactsSettingsDialog.svelte)). The section lists every external source (CardDAV + OAuth) regardless of state; per-row button flips between Enable / Disable based on `source.writable`. CardDAV is a pure flag flip via `App.SetContactSourceWritable`; Google / Microsoft route through the incremental-consent flow (which itself flips writable on success via `coreapi.Contacts.SetSourceWritable`).

**Optional contextual surface** — extensions can also surface a banner in their main pane for the discoverable enable case (see [`WriteAccessBanner.svelte`](../extensions/contacts/frontend/components/WriteAccessBanner.svelte)). The banner shows enable-only rows and auto-hides once a source is writable; its visibility tracks the sidebar selection so it stays contextual. The canonical Disable lever stays in the settings dialog.

**Phase 2b.3 cleanup**: the writable toggle was REMOVED from the core source-edit dialog ([`ContactSourceDialog.svelte`](../frontend/src/lib/components/settings/ContactSourceDialog.svelte)) so the extension owns this UX. New extensions with writable external sources should follow the same pattern: own the Enable/Disable lever in your own settings dialog, don't add a toggle to host-side source-edit UIs.

---

## Contributing a new extension

> **Read first:** today's intake policy is **first-party only**. The shape described here is what a third-party extension contribution would look like *if* a community intake ever opens (see [§ Distribution model](#distribution-model)). It is documented now so the architecture stays consistent with that potential future; it does not imply that PRs from outside contributors are being accepted today.

When a new extension does land (first-party today, or third-party later if intake opens), the contract below applies. The bridge architecture exists precisely to make this contract small enough to review by hand.

### The two hard rules for any new extension

These are non-negotiable and apply equally to first-party and (hypothetical) third-party extensions:

**1. Consume `coreapi` only. Never reach into Aerion internals.**

Extensions interact with Aerion exclusively through the `coreapi.Core` surfaces listed in [§ `coreapi` reference](#coreapi-reference). They:

- DO NOT import any package from `internal/` outside `internal/extensions/` (and only the store/kv helpers there) and `internal/core/api/v1/`.
- DO NOT call functions in `app/`, `internal/account/`, `internal/folder/`, `internal/message/`, `internal/draft/`, `internal/contact/`, `internal/carddav/`, `internal/imap/`, `internal/smtp/`, `internal/oauth2/`, `internal/credentials/`, etc., directly.
- DO NOT query, read, or write any table in Aerion's main database (`aerion.db`). The accounts/folders/messages/contacts/drafts tables are core-owned and off-limits.
- DO NOT shell out, monkey-patch, or use `reflect`/build-tag tricks to do any of the above indirectly.

The Contacts extension *is allowed* to receive a `*database.DB` handle to Aerion's main DB because it's a first-party special case: it grew out of code that already lived in Aerion core, owns the lifecycle of the `contact_records` + `contact_sources` + related tables, and is explicitly the canonical owner of that data. **This special case is not extended to new extensions.** A new extension never gets a handle to `aerion.db`.

**2. If the extension needs persistence, it brings its own database.**

Per-extension SQLite is the only persistence path. Use [`internal/extensions.OpenStore`](../internal/extensions/store.go) (see [§ Per-extension storage](#per-extension-storage)). That opens `<dataDir>/extensions/<name>/data.db`, applies the extension's own migrations, and gives back a `*sql.DB` scoped to that file only. Tiny config (sync tokens, view prefs) can go in the auto-created `ext_kv` table via `coreapi.Storage.KV`.

If the extension wants to read or write data Aerion itself owns (e.g., list messages, insert a contact, move a message), the only legitimate path is calling the relevant `coreapi` interface method — and only if that method already exists and is implemented. If it doesn't exist or returns `ErrUnimplemented`, see [§ Requesting a new extension API](#requesting-a-new-extension-api) below.

### Requesting a new extension API

**Extension contributions do NOT add new methods to `internal/core/api/v1/`.** API surface evolution is a separate, deliberate process owned by the project maintainer:

- If your extension needs a `coreapi` surface that doesn't exist today (e.g., `Mail.AppendMessage` is wired but returns `ErrUnimplemented`, or there's no `Calendar` interface at all), **open a Feature Request issue describing the use case** before writing extension code that depends on it.
- The maintainer evaluates whether the requested surface fits the API's stability promise (see [§ Stability promise](#stability-promise)), what shape it should take, and which release it would land in.
- Only after the API ships does the extension PR depending on it become reviewable.

Bundling an `internal/core/api/v1/` change into an extension PR is rejected on sight, even for first-party work. The reason: every `coreapi` method becomes a backwards-compatibility commitment, and that commitment is owned by the project, not by individual extensions.

### Lightweight-by-default invariant (the load-bearing principle)

Every extension MUST cost approximately zero when disabled. The reason this is non-negotiable: Aerion's core promise to its users is that it stays a lightweight email client. Each extension that breaks this invariant erodes that promise for the *entire user base*, not just the ones who'd use that extension.

Concretely, "approximately zero when disabled" means:

- **At process startup:** one `*Bridge` struct allocation (a few host-dependency fields) + one `*Extension` allocation (a manifest copy) + descriptive UI registrations (rail tab metadata, account-setup hook metadata). No SQLite open. No goroutines. No file handles. No HTTP clients.
- **At first enabled Wails call:** `ensureInit()` fires under `sync.Once`. THIS is where the SQLite file opens, migrations run, stores construct, and background goroutines spin up.
- **When the user disables a previously enabled extension:** new Wails calls return empty (`gateEnabled() == false`). Already-allocated state stays in memory until the next process restart. Users who want to fully reclaim memory restart Aerion — this is an explicit trade-off the project accepts (see [§ Lifecycle](#lifecycle)).

Any extension that opens its database at startup, spawns goroutines unconditionally, or registers HTTP clients eagerly is non-conforming. **All such work happens inside `ensureInit()`, gated by the enabled flag.**

### Required layout

```
extensions/<name>/
  manifest.json                  # extension metadata (ID, name, providers, capabilities)
  manifest.go                    # embeds manifest.json via //go:embed, exposes Manifest()
  backend/
    register.go                  # Extension struct + NewExtension() + Register()
    bridge.go                    # <Name>Bridge struct + <Name>BridgeDeps + New<Name>Bridge() + all <Name>_-prefixed methods
    api.go                       # internal API the bridge methods delegate to
    store.go                     # per-extension SQLite via extensions.OpenStore (opened by ensureInit, not eagerly)
    # ... whatever else the extension needs ...
  frontend/
    components/                  # Svelte components rendered into RailTab / settings slots
    stores/                      # Svelte runes stores scoped to this extension's UI
    hooks/                       # account-setup hook panel components (optional)
    i18n/
      index.ts                   # exports registerExtensionI18n() — calls svelte-i18n register() per locale
      locales/
        en.json                  # English source of truth (mandatory)
        <code>.json              # other locales (added by translators, optional per locale)
app/
  extension_<name>.go            # ~28 LOC host wiring: <Name>BridgeDeps construction + EventEmitter closure
```

The host-side delta in `app/app.go` is:
1. One embedded `*<Name>Bridge` field on the App struct.
2. One `a.init<Name>Extension()` call inside `Startup`.
3. One `a.<Name>Ext = extbe.NewExtension()` + append to `a.knownExtensions`.

Go can't auto-discover packages, so the backend side has to be wired explicitly — that's why `app/extension_<name>.go` exists. Frontend i18n is auto-discovered via Vite glob (see [§ Extension i18n](#extension-i18n)), so no host edit is needed there.

That's it. **No other host file should change.** If you find yourself editing files outside the points above and `extensions/<name>/`, you're either (a) reaching into Aerion internals (forbidden — see hard rule #1), or (b) trying to add a `coreapi` method (forbidden — file a Feature Request first).

### The `<Extension>_` method-name prefix (hard rule)

Every Wails-bound method on the Bridge MUST be named `<Extension>_<Method>` (e.g., `Contacts_UpdateContact`, `Calendar_CreateEvent`). Reasoning:

1. Embedded-method promotion shares one App namespace. Two extensions both defining `UpdateRecord()` would silently override each other.
2. The prefix makes ownership obvious in the generated `App.d.ts` — anyone reading the frontend bindings can tell which extension owns a method without grep.
3. The frontend conventionally re-imports with an alias (`Contacts_UpdateContact as UpdateContact`) inside the extension's own store file, so the prefix only appears at the import boundary.

### Review checklist

Whether the extension is first-party or third-party (if an intake opens), the review covers:

1. **The two hard rules above hold.** No imports from `internal/` outside the small allowed surface. No queries against `aerion.db`. Own SQLite via `extensions.OpenStore` if persistence is needed.
2. **No `internal/core/api/v1/` changes in this PR.** If the extension needed a new API method, that landed in a separate, prior PR (gated by a Feature Request issue).
3. **Lightweight invariant intact.** Trace every line that runs at startup (`NewBridge`, `NewExtension`, `Register`, `registerExtensionI18n`). None of it can open SQLite, spawn goroutines, or do network I/O. `ensureInit()` is the ONLY place those happen.
4. **All Wails-bound methods are gated.** Every method on `*Bridge` calls `b.gateEnabled()` and short-circuits if false. Disabled state returns `nil`/empty, never an error.
5. **Prefix correctness.** Every bridge method is `<Name>_<Method>`. No exceptions.
6. **No mail-code edits.** The mail UI components and the mail backend (`internal/imap/`, `internal/smtp/`, `internal/message/`, `frontend/src/lib/components/{list,viewer,composer,sidebar}/`, etc.) are off-limits to direct edits. The extension may **call into mail via `coreapi.Mail` / `coreapi.Composer` surfaces** — that's the whole point of those interfaces. What's forbidden is editing mail's own files. If you need a mail behavior that those interfaces don't expose today, see [§ Requesting a new extension API](#requesting-a-new-extension-api).
7. **Per-extension SQLite isolation.** Extensions never read or write each other's tables; cross-extension data goes through `coreapi.Core.Extension(id)` typed handles. If your extension's bridge takes another extension's `*Store` as a dependency, it's wrong.
8. **OAuth credentials are scoped per extension.** If the extension needs OAuth, it owns its own credential slot (`<provider>-<extension-id>`) and uses the Auth Broker; it doesn't reuse Aerion core's mail slot unless the manifest declares `first_party_uses_core_for_scopes` (see [§ OAuth client configurations](#oauth-client-configurations)) — and that field is honored only for first-party extensions.
9. **Frontend is independent.** No refactors to existing mail components to "share" code with the extension. Extension components stay self-contained under `extensions/<name>/frontend/`. The kit (`frontend/src/lib/components/kit/`) holds neutral primitives extensions can consume, but anything mail-specific stays mail-specific.
10. **Extension owns its i18n.** New strings live under `extensions/<name>/frontend/i18n/locales/<code>.json` (own files, not mixed into core's `frontend/src/lib/i18n/locales/<code>.json`). At minimum `en.json` ships with the PR. Other locales are added later by translators in separate PRs; absence falls back to English at runtime. See [§ Extension i18n](#extension-i18n).
11. **Documentation updated.** `docs/EXTENSIONS.md` § Wails-bound surface gains a row per new bridge method.

The bridge architecture is what makes this checklist enforceable as a code review rather than a multi-week audit.

---

## Distribution model

### Today: static linking, first-party only

All extensions compile into the single Aerion binary. Extensions live as Go packages under `extensions/<name>/backend/` and Svelte components under `extensions/<name>/frontend/`. The host embeds each extension's `*Bridge` on App (one wiring file at `app/extension_<name>.go`, ~28 LOC) and iterates `a.knownExtensions` to call `Register()` at startup.

Today this is **first-party only**. No third-party-PR intake is open and no extension installer exists. Everything past this point is a description of what the architecture *enables* the project to do if demand for it shows up — not a roadmap.

### If community-extension demand emerges: PR contribution path

The bridge architecture is intentionally shaped so that, **if community-extension demand emerges**, the simplest first step is to open the door to third-party extensions as PRs to the Aerion repo. Once merged, they'd become first-party (shipped in the binary, opt-in via Settings → Extensions, default-disabled). The reviewer would read three things instead of auditing a sprawling diff:

1. The extension's own `extensions/<name>/` directory (manifest, backend, frontend).
2. The `app/extension_<name>.go` wiring file (one embed + one constructor call + dependency wiring).
3. The added App field + `init<Name>Extension()` call site in `app/app.go` Startup.

See [§ Contributing a new extension](#contributing-a-new-extension) for what the contribution shape would look like and what reviewers would check. This is documented now so the architecture stays consistent with that future use; it does not imply the intake is open.

### If demand exceeds what PRs can handle: subprocess + IPC

If third-party-PR intake opens and demand grows past what individual code reviews can sustain, the next step the architecture allows is a **pre-compiled subprocess + IPC** model for community extensions. Each community extension would ship as its own Go binary (cross-compiled per platform), launched as a subprocess at startup, communicating with the main app via Unix socket / named pipe — the same path Aerion already uses for the detached composer.

Why subprocess and not other options (in case it ever needs to happen):

- **Go `plugin` package (.so loading)**: requires exact same Go version + same dependency tree as host; Linux/macOS only; no way to unload. Brittle in practice; almost no one ships this way.
- **WASM**: Go-backend WASM (wazero) is still research-grade for this use case. Promising but immature.
- **Embedded scripting (Lua, JS via goja)**: would force re-implementing CalDAV/CardDAV/heavy sync libs. Aerion extensions do real work and need the real Go ecosystem.
- **Subprocess + IPC**: used by VS Code (language servers), Docker (plugins), Hashicorp's `go-plugin`, Sourcegraph. Real process isolation = security. Capability enforcement at the IPC boundary actually means something (extension never sees raw tokens, can't bypass the Auth Broker via reflection).

The current API design is already subprocess-compatible. Nothing in `coreapi v1` references Go module paths, compiled-type names, or in-process pointers:

- `coreapi v1` interfaces → become gRPC / IPC schema (Go interfaces translate cleanly to protobuf services)
- `Auth Broker` → already designed as an opaque "tokens never leave the boundary" wall, perfect for IPC
- Per-extension SQLite → extension owns its own file in either model
- `RailTabRequest.Component: "ContactsPane"` → already a descriptive string, not a compiled type reference
- `Extension.Register(core)` → works as function call (static) AND as subprocess spawn + IPC handshake

What stays in the host even if community extensions arrive: **the Svelte components**. Even Obsidian doesn't let plugins ship React/Svelte components — Obsidian plugins manipulate the DOM directly via its workspace API. Community extensions would register against pre-built UI slots (rail tab, settings tab, a "generic extension pane" that renders state declared over IPC).

### What "if" actually buys

Today's static-linking + first-party model is **sufficient on its own**. Aerion ships first-party extensions (Contacts today, others over time) and users get them as part of the binary. Nothing further needs to happen for the product to work.

The optionality described above costs nothing right now — the bridge pattern, the `coreapi v1` shape, the per-extension SQLite isolation, the Auth Broker, the `<Extension>_` prefix rule are all things the project does anyway to keep first-party extensions tidy. They happen to also be what's required to extend the model later. So:

1. **No demand**: current model stays as-is, indefinitely. That's the default.
2. **Modest demand (a handful of motivated contributors)**: open PR intake, vet by hand, merge becomes first-party.
3. **Demand exceeding PR throughput**: invest in the subprocess runtime.

Each step is contingent on the previous one materializing, not pre-committed.

---

## Not yet implemented

Things extensions CANNOT do in v0.3.0. Items marked with a phase have a planned landing window; others are speculative.

### Backend

- `Mail` mutate methods (`MoveMessage`, `Archive`, `Trash`, `SetFlags`, `AppendMessage`) — return `ErrUnimplemented`. Phase 3+ when a real consumer (filter extension) needs them.
- `SubscribeToMailEvents` — `ErrUnimplemented`. Needs a core event-bus wiring first.
- `Contacts.SubscribeToContactEvents` — `ErrUnimplemented`. Same as above.
- `Composer.OpenComposer` with `Attachments` or `ReplyTo` — `ErrUnimplemented`. Mailto-URL-only path. Phase 2+ when a consumer needs richer compose semantics.
- `Auth.IMAPClient` / `Auth.SMTPClient` — `ErrUnimplemented`. Wires when a real consumer needs them (Sieve, delayed-send).
- `Notifications.Show` — interface only. Phase 3+.
- `UI.RegisterSettingsTab`, `RegisterContextMenuItem`, `RegisterInboxView` — registrations accepted but no consumer reads them yet.
- `EventBus.Publish` / `Subscribe` — interface only.

### Frontend

- Per-extension context menu items (`UI.RegisterContextMenuItem` accepts registrations but no consumer reads them yet)
- Per-extension inbox views (`UI.RegisterInboxView` — same as above)

### System

- Community-extension runtime (dynamic loading, manifest verification, capability consent UI) — contingent on demand; see [§ Distribution model](#distribution-model)
- Per-extension capability gating — Phase 1 grants first-party extensions everything; explicit capability checks would land alongside any community-extension intake

---

## Related documents

- [`context/EXTENSION_ARCHITECTURE.md`](../context/EXTENSION_ARCHITECTURE.md) — design rationale (per-DB isolation, enable/disable, lifecycle, frontend slot pattern, OAuth scope migration strategy, Wails v2 constraints)
- [`context/EXTENSION_API_PLAN.md`](../context/EXTENSION_API_PLAN.md) — detailed Cross-Extension API surface design with motivating use cases
- [`context/CARDDAV_IMPLEMENTATION.md`](../context/CARDDAV_IMPLEMENTATION.md) — CardDAV implementation; pattern reference for the Contacts extension
- [`context/DETACHABLE_COMPOSER_IMPLEMENTATION.md`](../context/DETACHABLE_COMPOSER_IMPLEMENTATION.md) — inline + detach pattern (extensions inherit this)
- [`CLAUDE.md`](../CLAUDE.md) — overall codebase guide
