// Shared post-add helper that turns the user's intent in the four
// add-calendar dialogs (Local / CalDAV / Google / Microsoft) into the
// right calendarSettings writes.
//
// Contract: each dialog runs the actual provider call (Calendar_AddXSource /
// Calendar_AddLocalCalendar / etc.) then hands us the resulting sourceId +
// the mapping from "tempId" (used by the picker UI before backend IDs exist)
// to the real Calendar IDs the backend returned. We resolve the user's
// provider-default / global-default choices against that mapping and persist
// them.
//
// Auto-default semantics:
//   - If the user didn't pick a provider default AND no provider default is
//     stored for this source yet, the first writable newly-added calendar
//     silently becomes it. Mirrors "first calendar created = default."
//   - Same rule for global default when none is stored yet.

import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'

export interface AddedCalendar {
  /** Backend-assigned Calendar ID. */
  id: string
  /** Temp ID the dialog used in the picker UI before the backend assigned id. */
  tempId: string
  writable: boolean
}

export interface DefaultsApplyInput {
  sourceId: string
  added: AddedCalendar[]
  /**
   * tempId of the candidate the user chose as provider default, or '' if
   * they didn't pick one. The tempId must match one of `added` rows.
   */
  providerDefaultTempId: string
  /**
   * Reference to the chosen global default. Either:
   *   - 'new:<tempId>' — pick the newly-added calendar with that tempId.
   *   - '<calendarId>' — an existing calendar's ID (already in another source).
   *   - '' — no change. Auto-default kicks in if globally unset.
   */
  globalDefaultRef: string
}

export function applyDefaultsAfterAdd(input: DefaultsApplyInput): void {
  const { sourceId, added, providerDefaultTempId, globalDefaultRef } = input
  const writableAdded = added.filter(c => c.writable)

  // Provider default: user choice → first writable added → leave stored as-is.
  const userProviderPick = added.find(c => c.tempId === providerDefaultTempId)
  if (userProviderPick && userProviderPick.writable) {
    calendarSettings.setProviderDefaultFor(sourceId, userProviderPick.id)
  }
  if (!userProviderPick && !calendarSettings.providerDefaultForRaw(sourceId) && writableAdded.length > 0) {
    calendarSettings.setProviderDefaultFor(sourceId, writableAdded[0].id)
  }

  // Global default: 'new:<tempId>' → resolve; raw ID → use as-is; '' →
  // auto-default if globally unset.
  if (globalDefaultRef.startsWith('new:')) {
    const tempId = globalDefaultRef.slice(4)
    const pick = added.find(c => c.tempId === tempId)
    if (pick && pick.writable) {
      calendarSettings.setGlobalDefaultCalendarId(pick.id)
    }
    return
  }
  if (globalDefaultRef !== '') {
    calendarSettings.setGlobalDefaultCalendarId(globalDefaultRef)
    return
  }
  if (!calendarSettings.globalDefaultCalendarIdRaw && writableAdded.length > 0) {
    calendarSettings.setGlobalDefaultCalendarId(writableAdded[0].id)
  }
}
