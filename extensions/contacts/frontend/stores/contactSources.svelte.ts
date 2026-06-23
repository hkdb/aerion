// Extension-owned contact-sources store. Mirrors the narrow subset of the
// core `$lib/stores/contactSources.svelte` interface the extension actually
// uses (sources cache, isSourceWritable, linkAccount, load) — but every
// backend call goes through the Contacts_* prefixed bridge methods rather
// than reaching into core's contact-source Wails methods.
//
// Why a parallel store rather than reusing core's: the core store is mail-
// side (it backs the Contacts settings tab's source list + the
// ContactSourceDialog used for mail autocomplete). Extension code reaching
// into core's `$lib/stores/contactSources.svelte` directly violates the
// EXTENSIONS.md "no internal/core-store imports" rule. This store gives
// the extension the same API shape while keeping all data flow on the
// extension's bridge surface.
//
// Mail-side `ContactSourceDialog.svelte` is intentionally untouched — it
// continues using the core store. Core code calling core methods unprefixed
// is fine; only extension → core needs to go through the bridge.

// @ts-ignore - wailsjs bindings
import {
  Contacts_ListSources as ListSources,
  Contacts_LinkAccountSource as LinkAccountSource,
  Contacts_SyncSource as SyncSource,
  Contacts_SyncAllSources as SyncAllSources,
} from '$wailsjs/go/app/App'
// @ts-ignore - wailsjs bindings
import type { v1 } from '$wailsjs/go/models'

function createContactSourcesStore() {
  let sources = $state<v1.ContactSource[]>([])
  let loading = $state(false)
  // `syncing` flips true while a sync call is in flight (single source or
  // all). The sidebar footer keys its "Syncing…" indicator off this. No
  // event-driven progress yet — contacts has no per-step sync events the
  // way calendar does; the boolean alone covers the user-facing UX.
  let syncing = $state(false)

  async function load(): Promise<void> {
    loading = true
    try {
      const result = await ListSources()
      sources = result || []
    } catch (err) {
      console.error('Failed to load contact sources:', err)
      sources = []
    } finally {
      loading = false
    }
  }

  // Alias kept for parity with the core store's interface — the extension's
  // single existing consumer (AccountContactsHookPanel) calls
  // `linkAccount(accountId, name, syncInterval)`. After linking, refresh the
  // cached list so subsequent .sources reads see the new source.
  async function linkAccount(accountId: string, name: string, syncInterval: number): Promise<void> {
    await LinkAccountSource(accountId, name, syncInterval)
    await load()
  }

  // syncSource fires a one-off sync for the given source. Used by the
  // sidebar footer's Ctrl+Shift+S handler. The `syncing` flag covers the
  // in-flight window so the footer can render "Syncing…".
  async function syncSource(sourceId: string): Promise<void> {
    if (!sourceId) return
    syncing = true
    try {
      await SyncSource(sourceId)
      await load()
    } finally {
      syncing = false
    }
  }

  // syncAll fires a sync against every configured contact source. Used by
  // the sidebar footer's Ctrl+Shift+A shortcut.
  async function syncAll(): Promise<void> {
    syncing = true
    try {
      await SyncAllSources()
      await load()
    } finally {
      syncing = false
    }
  }

  // Synchronous boolean derived from the cached list. Returns false for
  // unknown ids (the "aerion" local sentinel, OAuth sources not yet
  // writable, etc.) — callers OR with their own local-source check.
  function isSourceWritable(sourceId: string | undefined): boolean {
    if (!sourceId) return false
    const s = sources.find(s => s.id === sourceId)
    return !!s?.writable
  }

  return {
    get sources(): v1.ContactSource[] {
      return sources
    },
    get loading(): boolean {
      return loading
    },
    get syncing(): boolean {
      return syncing
    },
    load,
    linkAccount,
    syncSource,
    syncAll,
    isSourceWritable,
  }
}

export const contactSourcesStore = createContactSourcesStore()
