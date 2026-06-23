# Extension Architecture — Hard Rules

This document is the single canonical sanity-check list for the Aerion
extension system. Every extension-touching change MUST be cross-checked
against these rules before commit. If a proposed change requires breaking
one of them, the design is wrong — back up and rethink.

Companion docs:
- `docs/EXTENSIONS.md` — full guide with examples and design rationale.
- `docs/EXTENSION_ARCHITECTURE.md` — high-level invariants.
- This file (`docs/EXT_RULES.md`) — the rule book, scannable, no prose.

## 1 · The boundary (hardest rules)

- **R1.** Extensions consume `coreapi.Core` and the shared backend kit
  (`internal/kit/*`) — nothing else. They do NOT import any other
  `internal/*` packages. The `internal/kit/*` carve-out covers generic,
  extension-agnostic utilities (no extension-specific naming or behavior);
  see R26.
- **R2.** The host MUST NOT pass closures wrapping `internal/*` calls into
  extension code (e.g., `Func` fields on `BridgeDeps`). That's hidden
  coupling — still violates R1 in spirit. If an extension needs it, add it
  to `coreapi`.
- **R3.** Anything an extension needs from the host gets added as a method
  on `coreapi.Core` (or a surface returned from it). Period.
- **R4.** Cross-extension calls go through `core.Extension(id)` only. Never
  direct Go imports of another extension's package.
- **R5.** Core code MUST NOT write to an extension's per-extension SQLite
  database. Ever. Each extension's DB is its own data sovereignty boundary.
- **R6.** Core code MUST NOT bake extension-specific knowledge into core
  packages. No methods like `SetCalDAVPassword` / `SetCalendarFoo` on
  `internal/credentials/store.go`. Generic primitives or extension-agnostic
  convenience surfaces only. Extension-specific names belong in the
  extension's own package.

## 2 · When to add `coreapi` surfaces

- **R7.** Do NOT add `coreapi.<X>` interfaces speculatively. Add them only
  when there is a CONCRETE consumer (the host or another extension) that
  needs to call them. Single-extension features with no external consumer
  belong on the extension's Bridge, not on `coreapi`.
- **R8.** When adding a `coreapi` surface, let the consumer's actual
  requirements drive the method shapes. Don't pre-design a 10-method
  interface where 1 method is needed today.
- **R9.** If two extensions need the same kind of thing (e.g., secret
  storage, KV config), promote it to `coreapi`. If only one does, the
  extension implements it locally.

## 3 · Data ownership

- **R10.** Each extension owns its per-extension SQLite, opened via
  `internal/extensions.OpenStore(dataDir, name, migrations)` at
  `<dataDir>/extensions/<name>/data.db`. Schema migrations are
  extension-owned (live in `extensions/<name>/backend/store.go`).
- **R11.** Per-extension SQLite is opened LAZILY inside the bridge's
  `ensureInit()` (sync.Once-gated), on the first enabled bridge call. A
  disabled extension never opens its DB. Once open, the DB stays open
  until process exit — disable/enable cycles within a session reuse the
  existing connection. Schema migrations run at the point of first open,
  not at app startup.
- **R12.** Extensions never read each other's tables. Cross-extension data
  access flows through `coreapi`.
- **R13.** Each extension owns its OAuth client-config slots, declared in
  `extensions/<name>/creds.go` as `coreapi.OAuthProviderRegistration`.
  Slot IDs are `<provider>-<extensionID>` (e.g., `google-calendar`,
  `microsoft-contacts`). Credentials are ldflag-injected at build time.
- **R14.** Settings key for each extension is reserved in
  `internal/settings/store.go` as `KeyExtension<Name>Enabled =
  "extension_<name>_enabled"` and added to `AllExtensionKeys`. Default
  disabled.

## 4 · Lightweight-by-default

- **R15.** Disabled extensions contribute zero perceptible cost. The
  ~80-byte Bridge struct allocation is the entire footprint until the
  first enabled method call.
- **R16.** Bridge methods MUST gate-check via `gateEnabled()` before any
  work. Disabled = empty results / no-op return. No errors surfaced.
- **R17.** Background services (sync schedulers, IDLE managers, event
  publishers) start inside `ensureInit()`, not at `Startup`. A disabled
  extension contributes zero goroutines, zero timers, zero file handles.

## 5 · Naming (compile-time / collision-safety rules)

- **R18.** Bridge struct type MUST be named `<Name>Bridge` (e.g.,
  `ContactsBridge`, `CalendarBridge`). Generic `Bridge` collides on
  anonymous embed in `*app.App`. Same for the deps struct
  (`<Name>BridgeDeps`) and constructor (`New<Name>Bridge`).
- **R19.** All Wails-bound methods MUST be named `<Extension>_<Method>`
  (e.g., `Contacts_UpdateContact`, `Calendar_AddCalDAVSource`). Embedded
  method promotion happens in one flat namespace on `*App`; unprefixed
  names would collide silently.
- **R20.** OAuth client config IDs follow `<provider>-<extensionID>` (no
  underscores between provider and extension). Example:
  `google-calendar`, NOT `google_calendar` or `googleCalendar`.
- **R21.** Frontend Svelte components live in
  `extensions/<name>/frontend/components/`. Use the `$extensions/<name>/...`
  Vite/tsconfig path alias to import them. Use `$wailsjs` for generated
  Wails bindings. Use `$lib` for kit primitives.

## 6 · Host wiring (the only place core code touches an extension)

- **R22.** Each extension has exactly ONE host wiring file:
  `app/extension_<name>.go`. ~20-50 LOC. Constructs the Bridge with
  `BridgeDeps`, registers OAuth providers via
  `oauth2.RegisterCredentialsProvider`. Nothing else.
- **R23.** `*app.App` embeds the extension's `*<Name>Bridge` anonymously
  (so Wails method-promotion picks up the `<Extension>_*` methods). Plus
  one named field for the lifecycle handle (`a.<name>Ext`) iterated by
  the Register loop in Startup.
- **R24.** Extension lifecycle Registration happens in Startup's
  `knownExtensions` loop, regardless of enabled state. Descriptive
  registrations (rail tab, settings tab, account-setup hook) persist
  across enable/disable cycles; the frontend filters by enabled state at
  render time.

## 7 · Frontend (extension SDK pattern)

- **R25.** Kit primitives (`frontend/src/lib/components/kit/*` on the
  frontend, `internal/kit/<area>/` on the backend) are greenfield SDK —
  generic, extension-agnostic building blocks designed for extensions to
  consume. The mail UI (`ConversationViewer`, `MessageList`, etc.) is NOT
  refactored to share with kit. If a kit primitive solves a mail problem,
  mail can opt in at its own pace — but the kit isn't a retrofit target.
- **R26.** Calendar/Contacts-domain components stay in their respective
  `extensions/<name>/frontend/components/` (or `extensions/<name>/backend/`
  for Go). They only get promoted to kit when a SECOND extension (or core
  + an extension) actually consumes the same shape, and the promoted code
  has no extension-specific naming.
- **R27.** Extension component file structure mirrors the contacts
  precedent: `components/`, `stores/`, `hooks/` (optional), `i18n/`.
- **R28.** `App.svelte` dispatches the active extension's root pane via
  `{#if getActiveExtension() === '<name>'} <NamePane /> {/if}`. Static
  dispatch by extension ID — same pattern `ExtensionSettingsDialog.svelte`
  uses for per-extension settings dialogs.
- **R29.** All UI strings use `$_('namespace.key')` from svelte-i18n.
  English source is `frontend/src/lib/i18n/locales/en.json` (core) or
  `extensions/<name>/frontend/i18n/locales/en.json` (extension).
  Non-English locales get English placeholders by default for new keys;
  translators upgrade them.

## 8 · Migrations + bindings

- **R30.** Migrations are forward-only. Use `CREATE TABLE IF NOT EXISTS` /
  `CREATE INDEX IF NOT EXISTS` for idempotency — the project convention.
- **R31.** Core migrations only run for shared infrastructure (e.g.,
  `extension_secrets`). Per-extension data tables go in the extension's
  own SQLite migrations.
- **R32.** Rollback SQL lives in `tools/db/rollback-v<latest>-to-v30.sql`
  + corresponding doc bump in `docs/SQL_ROLLBACK.md`. Update both when
  adding a core migration.
- **R33.** After backend changes that add/remove Wails methods, run
  `make generate` to regenerate frontend bindings
  (`frontend/wailsjs/go/app/App.{js,d.ts}`). Source of truth is the Go
  method signatures; the JS bindings follow.

## 9 · The smell checklist (self-correct before committing a design)

Before writing code that touches the extension system, ask:

- Am I tempted to add a `*Func` field on `BridgeDeps`?
  → **It probably belongs on `coreapi`.** Stop and design the coreapi
  surface first.
- Am I tempted to add an extension-specific method
  (`SetCalDAVPassword`, `GetCalendarFoo`) directly to a core package?
  → **No.** Either it should be a generic primitive (no extension name
  baked in), or it should be a host-internal helper that backs a
  generic `coreapi` surface.
- Am I tempted to import `internal/*` from inside `extensions/<name>/`?
  → **No.** Stop. The extension never imports internal packages.
- Am I tempted to have core code write to the extension's per-extension
  SQLite (directly or via a method call)?
  → **No.** That violates the data ownership boundary. The extension
  owns its data; core never reaches in.
- Am I tempted to give the extension a closure from the host that wraps
  internal calls?
  → **No.** That's a hidden `internal/*` dependency. Use `coreapi`.
- Am I about to add a `coreapi.<X>` interface for a feature only one
  extension uses?
  → **Probably wrong.** Single-extension features belong on the
  extension's Bridge. Promote to `coreapi` only when a second
  consumer arrives.
- Am I about to bake extension-specific knowledge ("caldav", "contacts",
  etc.) into a generic helper or table?
  → **No.** Use the extension ID as a parameter (`extension TEXT NOT
  NULL` column, `extension string` argument). Generic helpers stay
  generic.
- Am I naming the new extension's Bridge struct just `Bridge`?
  → **No.** It's `<Name>Bridge`. Anonymous-embed collision is a
  compile error.
- Did I forget to run `make generate` after adding/removing a Wails
  method?
  → **Bindings will be stale.** Always regenerate at the end of any
  bridge-method change.

## 10 · When in doubt

When in doubt about whether something belongs in core or in the
extension, ask: **could a community extension built by an outside
developer rely on this?**
- If yes → it belongs in `coreapi` (the published SDK).
- If no → it belongs in the extension package OR is host-internal
  (between `app/` and `internal/*`).

When the answer is "no" but the host needs to centralize the logic, the
right shape is: a host-internal helper in an `internal/*` package, called
from `app/coreimpl.go` to back a generic `coreapi` surface. Extensions
see only the `coreapi` surface; the internal helpers are invisible to
them.
