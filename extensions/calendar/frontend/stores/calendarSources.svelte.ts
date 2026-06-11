// Calendar sources + their calendars store. Mirrors
// extensions/contacts/frontend/stores/contactSources.svelte.ts in shape:
// lazy-load via explicit calls (not eager $effect), cache the result,
// surface loading / error state for the UI.

// @ts-ignore - wailsjs bindings
import {
  Calendar_ListSources,
  Calendar_ListCalendars,
  Calendar_AddCalDAVSource,
  Calendar_DeleteSource,
  Calendar_SyncSource,
  Calendar_SyncAllSources,
  Calendar_SetCalendarVisible,
  Calendar_SetCalendarColor,
} from '$wailsjs/go/app/App.js'
// @ts-ignore - wailsjs bindings
import { EventsOn } from '$wailsjs/runtime/runtime.js'
// @ts-ignore - wailsjs bindings
import type { backend } from '$wailsjs/go/models'

type Source = backend.Source
type Calendar = backend.Calendar

// Per-source sync lifecycle state driven by backend events. The four
// lifecycle events from sync.go:
//   calendar:sync-started    → phase = 'started'
//   calendar:sync-progress   → phase = 'progress' (+calendarName, n/total)
//   calendar:sync-complete   → delete the entry (back to idle)
//   calendar:source-error    → phase = 'error' (+errorMessage), auto-clears
//                              after 8s so the sidebar doesn't pin a stale
//                              error forever (settings dialog shows the
//                              persistent last_error from the row instead).
export type SyncPhase = 'started' | 'progress' | 'error'
export interface SourceSyncState {
  phase: SyncPhase
  sourceName: string
  calendarName?: string
  currentCalendar?: number
  totalCalendars?: number
  errorMessage?: string
}

let sources = $state<Source[]>([])
let calendarsBySource = $state<Record<string, Calendar[]>>({})
let loading = $state(false)
let lastError = $state<string | null>(null)
let syncStates = $state<Record<string, SourceSyncState>>({})

let subscribed = false

interface SyncStartedPayload {
  sourceId: string
  sourceName: string
}

interface SyncProgressPayload {
  sourceId: string
  sourceName: string
  calendarId: string
  calendarName: string
  currentCalendar: number
  totalCalendars: number
}

interface SyncCompletePayload {
  sourceId: string
}

interface SourceErrorPayload {
  sourceId: string
  message: string
}

function deleteSyncState(sourceId: string) {
  const next = { ...syncStates }
  delete next[sourceId]
  syncStates = next
}

function ensureSyncSubscriptions() {
  if (subscribed) return
  subscribed = true

  EventsOn('calendar:sync-started', (d: SyncStartedPayload) => {
    syncStates = {
      ...syncStates,
      [d.sourceId]: { phase: 'started', sourceName: d.sourceName },
    }
  })

  EventsOn('calendar:sync-progress', (d: SyncProgressPayload) => {
    syncStates = {
      ...syncStates,
      [d.sourceId]: {
        phase: 'progress',
        sourceName: d.sourceName,
        calendarName: d.calendarName,
        currentCalendar: d.currentCalendar,
        totalCalendars: d.totalCalendars,
      },
    }
  })

  EventsOn('calendar:sync-complete', (d: SyncCompletePayload) => {
    deleteSyncState(d.sourceId)
  })

  EventsOn('calendar:source-error', (d: SourceErrorPayload) => {
    const prior = syncStates[d.sourceId]
    syncStates = {
      ...syncStates,
      [d.sourceId]: {
        phase: 'error',
        sourceName: prior?.sourceName ?? '',
        errorMessage: d.message,
      },
    }
    // Auto-clear the inline error after 8s — the persistent last_error
    // column still shows in the settings dialog row.
    setTimeout(() => {
      if (syncStates[d.sourceId]?.phase !== 'error') return
      deleteSyncState(d.sourceId)
    }, 8000)
  })
}

const isAnySyncing = $derived.by(() => {
  for (const k of Object.keys(syncStates)) {
    const s = syncStates[k]
    if (s.phase === 'started' || s.phase === 'progress') return true
  }
  return false
})

const currentSyncState = $derived.by<SourceSyncState | null>(() => {
  for (const k of Object.keys(syncStates)) {
    const s = syncStates[k]
    if (s.phase === 'started' || s.phase === 'progress') return s
  }
  return null
})

const currentErrorState = $derived.by<SourceSyncState | null>(() => {
  for (const k of Object.keys(syncStates)) {
    const s = syncStates[k]
    if (s.phase === 'error') return s
  }
  return null
})

// Flatten all visible calendar IDs across all sources. Used as the input
// to Calendar_ListEventsInRange so the events store knows which calendars
// to fetch occurrences for.
const visibleCalendarIDs = $derived.by(() => {
  const out: string[] = []
  for (const src of sources) {
    const cals = calendarsBySource[src.id] || []
    for (const cal of cals) {
      if (cal.visible) out.push(cal.id)
    }
  }
  return out
})

async function load() {
  // First load wires the sync-lifecycle subscriptions. Guarded against
  // double-bind via the module-level `subscribed` flag.
  ensureSyncSubscriptions()
  loading = true
  lastError = null
  try {
    const fetched = (await Calendar_ListSources()) || []
    sources = fetched

    const next: Record<string, Calendar[]> = {}
    for (const src of fetched) {
      const cals = (await Calendar_ListCalendars(src.id)) || []
      next[src.id] = cals
    }
    calendarsBySource = next
  } catch (err) {
    lastError = (err as Error)?.message ?? String(err)
    console.error('Failed to load calendar sources:', err)
  } finally {
    loading = false
  }
}

async function addCalDAVSource(
  name: string,
  url: string,
  username: string,
  password: string,
  organizerEmail: string,
): Promise<string> {
  const sourceID = await Calendar_AddCalDAVSource(name, url, username, password, organizerEmail)
  await load()
  return sourceID
}

async function deleteSource(sourceID: string) {
  await Calendar_DeleteSource(sourceID)
  await load()
}

async function syncSource(sourceID: string) {
  await Calendar_SyncSource(sourceID)
  // No explicit reload — the `calendar:sync-complete` event the syncer
  // emits will trigger the events store to refetch its window. We DO
  // reload sources so last_synced_at + last_error update in the sidebar.
  await load()
}

async function syncAll() {
  await Calendar_SyncAllSources()
  await load()
}

async function setVisible(calendarID: string, visible: boolean) {
  await Calendar_SetCalendarVisible(calendarID, visible)
  // Optimistic local update so the UI reacts instantly without waiting
  // for a reload.
  for (const sid of Object.keys(calendarsBySource)) {
    const cals = calendarsBySource[sid]
    for (const cal of cals) {
      if (cal.id === calendarID) {
        cal.visible = visible
      }
    }
  }
  // Reassign to trigger reactivity.
  calendarsBySource = { ...calendarsBySource }
}

async function setColor(calendarID: string, hex: string) {
  await Calendar_SetCalendarColor(calendarID, hex)
  for (const sid of Object.keys(calendarsBySource)) {
    const cals = calendarsBySource[sid]
    for (const cal of cals) {
      if (cal.id === calendarID) {
        cal.color = hex
      }
    }
  }
  calendarsBySource = { ...calendarsBySource }
}

// isWritable returns true when the source that owns this calendar accepts
// event CRUD. Local sources are always writable; CalDAV sources flip to
// writable=true after AddCalDAVSource (new) or after the first successful
// sync (existing, post-Chunk-1 upgrade). Future Google/Microsoft providers
// will set it based on accessRole / canEdit. The frontend gates Edit /
// Delete / "+ Event" affordances on this — provider type is invisible to
// the UI past Chunk 2.
function isWritable(calendarID: string): boolean {
  for (const src of sources) {
    const cals = calendarsBySource[src.id] || []
    for (const cal of cals) {
      if (cal.id === calendarID) return src.writable === true
    }
  }
  return false
}

function colorOf(calendarID: string): string {
  for (const sid of Object.keys(calendarsBySource)) {
    const cals = calendarsBySource[sid]
    for (const cal of cals) {
      if (cal.id === calendarID) {
        return cal.color || defaultColor(cal.id)
      }
    }
  }
  return defaultColor(calendarID)
}

// Deterministic per-calendar default color so each calendar gets a stable
// hue even before the user customizes one. Hash the id into the hue space.
function defaultColor(calendarID: string): string {
  let hash = 0
  for (let i = 0; i < calendarID.length; i++) {
    hash = (hash * 31 + calendarID.charCodeAt(i)) | 0
  }
  const hue = Math.abs(hash) % 360
  return `hsl(${hue}, 65%, 55%)`
}

// Hex-string equivalents for places that need #rrggbb specifically — namely
// the kit ColorPicker, which validates input as 7-char hex and falls back to
// its first preset (blue) when given an HSL string. Same hash → same color
// as defaultColor() / colorOf().
function colorOfHex(calendarID: string): string {
  for (const sid of Object.keys(calendarsBySource)) {
    const cals = calendarsBySource[sid]
    for (const cal of cals) {
      if (cal.id === calendarID) {
        if (cal.color && cal.color !== '') return cal.color
        return defaultColorHex(cal.id)
      }
    }
  }
  return defaultColorHex(calendarID)
}

function defaultColorHex(calendarID: string): string {
  let hash = 0
  for (let i = 0; i < calendarID.length; i++) {
    hash = (hash * 31 + calendarID.charCodeAt(i)) | 0
  }
  const hue = Math.abs(hash) % 360
  return hslToHex(hue, 65, 55)
}

// Standard HSL → hex conversion. Saturation + lightness are percentages
// (0..100). Returns a 7-char #rrggbb string suitable for ColorPicker's
// `value` prop.
function hslToHex(h: number, s: number, l: number): string {
  const sn = s / 100
  const ln = l / 100
  const c = (1 - Math.abs(2 * ln - 1)) * sn
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1))
  const m = ln - c / 2
  let r = 0, g = 0, b = 0
  if (h < 60)        { r = c; g = x; b = 0 }
  else if (h < 120)  { r = x; g = c; b = 0 }
  else if (h < 180)  { r = 0; g = c; b = x }
  else if (h < 240)  { r = 0; g = x; b = c }
  else if (h < 300)  { r = x; g = 0; b = c }
  else               { r = c; g = 0; b = x }
  const to2 = (v: number) => Math.round((v + m) * 255).toString(16).padStart(2, '0')
  return `#${to2(r)}${to2(g)}${to2(b)}`
}

export const calendarSources = {
  get sources() { return sources },
  get calendarsBySource() { return calendarsBySource },
  get loading() { return loading },
  get lastError() { return lastError },
  get visibleCalendarIDs() { return visibleCalendarIDs },

  // Sync-lifecycle observability — driven by backend events. The sidebar
  // footer uses isAnySyncing / currentSyncState to render the animated
  // indicator + phase-aware label; currentErrorState backs the transient
  // red banner that auto-clears after 8s.
  get isAnySyncing() { return isAnySyncing },
  get currentSyncState() { return currentSyncState },
  get currentErrorState() { return currentErrorState },
  syncStateFor(sourceId: string): SourceSyncState | null {
    return syncStates[sourceId] ?? null
  },

  load,
  addCalDAVSource,
  deleteSource,
  syncSource,
  syncAll,
  setVisible,
  setColor,
  colorOf,
  colorOfHex,
  isWritable,
}
