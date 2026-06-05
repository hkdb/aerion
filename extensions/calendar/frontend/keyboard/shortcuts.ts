// Calendar extension's keyboard shortcut predicates.
//
// Mirrors the contacts extension's pattern — self-contained inside the
// extension's directory, registered at component mount via
// registerExtensionShortcut. The host's global handler dispatches via
// dispatchExtensionShortcut when Calendar is the active rail pane.

import { noMods, ctrlOrMeta, altOnly } from '$lib/keyboard/shortcuts'

/** `t` — jump the calendar view to today. */
export const CALENDAR_TODAY = (e: KeyboardEvent): boolean =>
  e.key === 't' && noMods(e)

/** `←` — navigate to the previous view-unit (prev month / week / day). */
export const CALENDAR_PREV = (e: KeyboardEvent): boolean =>
  e.key === 'ArrowLeft' && noMods(e)

/** `→` — navigate to the next view-unit (next month / week / day). */
export const CALENDAR_NEXT = (e: KeyboardEvent): boolean =>
  e.key === 'ArrowRight' && noMods(e)

/** `Ctrl/Cmd+R` — trigger a sync of all sources. */
export const CALENDAR_SYNC = (e: KeyboardEvent): boolean =>
  e.key === 'r' && ctrlOrMeta(e) && !e.shiftKey && !e.altKey

/** `Ctrl/Cmd+N` — open the new-event composer. Routed by the extension
 *  shortcut registry before App.svelte's mail-domain switch, so this only
 *  fires when the calendar rail is active. Mail's Ctrl+N (handleCompose)
 *  is now mail-domain-guarded and stays out of the way. */
export const CALENDAR_NEW_EVENT = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 'n' && ctrlOrMeta(e) && !e.shiftKey && !e.altKey

/** `f` — toggle focus mode for the selected event (no-op if no event selected). */
export const CALENDAR_FOCUS_TOGGLE = (e: KeyboardEvent): boolean =>
  e.key === 'f' && noMods(e)

/** `Ctrl/Cmd+Shift+A` — sync all calendar sources. Mirrors mail's
 *  Ctrl+Shift+A for "sync all accounts", routed to the calendar handler
 *  by the host when the calendar rail is active. Coexists with the
 *  existing Ctrl+R sync — both call `calendarSources.syncAll()`. */
export const CALENDAR_SYNC_ALL = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 'a' && ctrlOrMeta(e) && e.shiftKey

/** `Alt+M` — switch to month view. */
export const CALENDAR_VIEW_MONTH = (e: KeyboardEvent): boolean =>
  altOnly(e) && e.key.toLowerCase() === 'm'

/** `Alt+W` — switch to week view. */
export const CALENDAR_VIEW_WEEK = (e: KeyboardEvent): boolean =>
  altOnly(e) && e.key.toLowerCase() === 'w'

/** `Alt+D` — switch to day view. */
export const CALENDAR_VIEW_DAY = (e: KeyboardEvent): boolean =>
  altOnly(e) && e.key.toLowerCase() === 'd'

/** `Alt+A` — switch to agenda view. */
export const CALENDAR_VIEW_AGENDA = (e: KeyboardEvent): boolean =>
  altOnly(e) && e.key.toLowerCase() === 'a'

export const KEY = {
  CALENDAR_TODAY,
  CALENDAR_PREV,
  CALENDAR_NEXT,
  CALENDAR_SYNC,
  CALENDAR_SYNC_ALL,
  CALENDAR_NEW_EVENT,
  CALENDAR_FOCUS_TOGGLE,
  CALENDAR_VIEW_MONTH,
  CALENDAR_VIEW_WEEK,
  CALENDAR_VIEW_DAY,
  CALENDAR_VIEW_AGENDA,
}
