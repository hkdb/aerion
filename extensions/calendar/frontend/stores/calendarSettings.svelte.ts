// Calendar extension-local settings store. Persisted via localStorage —
// pragmatic for a single user-pref store today; the public API of this
// module is shaped to be swappable for a Wails-backed Store when the
// broader extension-settings infrastructure follows the SQLite pattern.
//
// Holds:
//   - displayTimezone: '' = auto-detect, '<IANA>' = override.
//   - globalDefaultCalendarId: the calendar pre-selected when the composer
//     opens. '' = fall back to first writable calendar.
//   - providerDefaults: per-source map of {sourceId → calendarId}. Edited
//     by the settings dialog and the add-flow defaults control; not used
//     as live composer behavior (the composer respects the user's manual
//     Select choice once made).
//
// Stale-pruning: globalDefaultCalendarId / providerDefaultFor() validate
// the stored ID still exists + is writable in `calendarSources`. If not,
// the getter returns '' but does NOT mutate state — cleanup happens when
// the user next writes a default, avoiding cascade writes during render.

import { logger } from '$extensions/calendar/frontend/lib/logger'
import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'

const STORAGE_KEY_TZ        = 'aerion:calendar:displayTimezone'
const STORAGE_KEY_GLOBAL    = 'aerion:calendar:globalDefaultCalendarId'
const STORAGE_KEY_PROVIDER  = 'aerion:calendar:providerDefaults'

let displayTimezone = $state<string>('')
let globalDefault = $state<string>('')
let providerDefaults = $state<Record<string, string>>({})

// Synchronous module-init reads. Safe to run before mount because the
// store module is imported lazily (only when Calendar is the active rail
// pane), and localStorage access is synchronous.
try {
  displayTimezone = window.localStorage.getItem(STORAGE_KEY_TZ) ?? ''
} catch (err) {
  logger.warn(`calendarSettings: localStorage read failed: ${err}`)
}

try {
  globalDefault = window.localStorage.getItem(STORAGE_KEY_GLOBAL) ?? ''
} catch (err) {
  logger.warn(`calendarSettings: localStorage read failed: ${err}`)
}

try {
  const raw = window.localStorage.getItem(STORAGE_KEY_PROVIDER)
  if (raw) {
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      providerDefaults = parsed as Record<string, string>
    }
  }
} catch (err) {
  logger.warn(`calendarSettings: providerDefaults read failed: ${err}`)
}

/** True iff calendarId resolves to a calendar whose source is writable AND
 *  the calendar itself is writable. The composer's default pick must be a
 *  calendar the user can actually save to. */
function isWritableCalendar(calendarId: string): boolean {
  if (!calendarId) return false
  for (const src of calendarSources.sources) {
    if (!src.writable) continue
    for (const cal of calendarSources.calendarsBySource[src.id] || []) {
      if (cal.id !== calendarId) continue
      return cal.writable !== false
    }
  }
  return false
}

function persistProviderDefaults(): void {
  try {
    window.localStorage.setItem(STORAGE_KEY_PROVIDER, JSON.stringify(providerDefaults))
  } catch (err) {
    logger.warn(`calendarSettings: providerDefaults write failed: ${err}`)
  }
}

function clearProviderDefault(sourceId: string): void {
  delete providerDefaults[sourceId]
  providerDefaults = { ...providerDefaults }
  persistProviderDefaults()
}

function writeProviderDefault(sourceId: string, calendarId: string): void {
  providerDefaults = { ...providerDefaults, [sourceId]: calendarId }
  persistProviderDefaults()
}

export const calendarSettings = {
  /** User's stored choice. Empty string = auto-detect. */
  get displayTimezone() { return displayTimezone },

  /** The IANA timezone all formatters + tzMath helpers should use. Resolves
   *  the auto-detect fallback so callers never have to. Computed on each
   *  read — avoids module-scope $derived sequencing surprises. */
  get effectiveTimezone() {
    return displayTimezone !== ''
      ? displayTimezone
      : Intl.DateTimeFormat().resolvedOptions().timeZone
  },

  /** Set the user's tz choice. Empty string clears the override. */
  setDisplayTimezone(tz: string) {
    displayTimezone = tz
    try {
      if (tz === '') {
        window.localStorage.removeItem(STORAGE_KEY_TZ)
        return
      }
      window.localStorage.setItem(STORAGE_KEY_TZ, tz)
    } catch (err) {
      logger.warn(`calendarSettings: localStorage write failed: ${err}`)
    }
  },

  /** The calendar pre-selected when the composer opens. Returns '' if the
   *  stored ID no longer resolves to a writable calendar (deleted source,
   *  permission flipped read-only, etc.) so the composer falls back to its
   *  first-writable rule. State isn't mutated on read; the stale value
   *  gets overwritten the next time the user picks a different default. */
  get globalDefaultCalendarId() {
    return isWritableCalendar(globalDefault) ? globalDefault : ''
  },

  /** The raw stored global default (no writability check). Used by the
   *  settings dialog so it can show the stale entry until the user fixes
   *  it. Most consumers want globalDefaultCalendarId. */
  get globalDefaultCalendarIdRaw() { return globalDefault },

  setGlobalDefaultCalendarId(id: string) {
    globalDefault = id
    try {
      if (id === '') {
        window.localStorage.removeItem(STORAGE_KEY_GLOBAL)
        return
      }
      window.localStorage.setItem(STORAGE_KEY_GLOBAL, id)
    } catch (err) {
      logger.warn(`calendarSettings: globalDefault write failed: ${err}`)
    }
  },

  /** Per-source default calendar ID. Returns '' when the stored entry no
   *  longer resolves to a writable calendar in `sourceId`. */
  providerDefaultFor(sourceId: string): string {
    if (!sourceId) return ''
    const stored = providerDefaults[sourceId] || ''
    if (!stored) return ''
    const cals = calendarSources.calendarsBySource[sourceId] || []
    const match = cals.find(c => c.id === stored)
    if (!match) return ''
    if (match.writable === false) return ''
    return stored
  },

  /** Raw stored provider default (no writability check). For settings UI. */
  providerDefaultForRaw(sourceId: string): string {
    return providerDefaults[sourceId] || ''
  },

  setProviderDefaultFor(sourceId: string, calendarId: string) {
    if (!sourceId) return
    if (calendarId === '') {
      clearProviderDefault(sourceId)
      return
    }
    writeProviderDefault(sourceId, calendarId)
  },
}
