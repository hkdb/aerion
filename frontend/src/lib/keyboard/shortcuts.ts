// Shared keyboard shortcut predicates — single source of truth for "what key
// combination matches what action." Consumed by both Aerion's mail UI handler
// (App.svelte) and by extension UI components in the kit (frontend/src/lib/
// components/kit/).
//
// Implementation per consumer stays separate: mail's handler dispatches to
// mail component refs; kit components handle their own keys locally via
// tabindex + stopPropagation. The bridge is THIS file — rebinding a key
// changes exactly one place.

// Modifier-state helpers. Exported so extension shortcut files
// (extensions/<name>/frontend/keyboard/shortcuts.ts) compose their predicates
// against the SAME helpers mail and the kit use — single convention for
// "no modifiers," "ctrl-or-cmd," "alt only" across the whole app.
export function noMods(e: KeyboardEvent): boolean {
  return !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey
}

export function ctrlOrMeta(e: KeyboardEvent): boolean {
  return (e.ctrlKey || e.metaKey) && !e.altKey
}

export function altOnly(e: KeyboardEvent): boolean {
  return e.altKey && !e.ctrlKey && !e.metaKey
}

// List/pane navigation — shared between mail's MessageList AND kit's ListPane,
// and mail's Sidebar AND kit's SourceSidebar.
export const LIST_NEXT = (e: KeyboardEvent): boolean =>
  (e.key === 'j' || e.key === 'ArrowDown') && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey

export const LIST_PREV = (e: KeyboardEvent): boolean =>
  (e.key === 'k' || e.key === 'ArrowUp') && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey

// Shift+J / Shift+K / Shift+Down / Shift+Up — select with checkbox toggle.
// Note: 'J' (capital) is what shift+j produces; ArrowDown stays the same key
// but with shiftKey set. Cover both forms.
export const LIST_NEXT_CHECK = (e: KeyboardEvent): boolean =>
  e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey &&
  (e.key === 'J' || e.key === 'j' || e.key === 'ArrowDown')

export const LIST_PREV_CHECK = (e: KeyboardEvent): boolean =>
  e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey &&
  (e.key === 'K' || e.key === 'k' || e.key === 'ArrowUp')

export const LIST_TOGGLE_CHECK = (e: KeyboardEvent): boolean =>
  e.key === ' ' && noMods(e)

export const LIST_OPEN = (e: KeyboardEvent): boolean =>
  e.key === 'Enter' && noMods(e)

export const LIST_SELECT_ALL = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 'a' && ctrlOrMeta(e) && !e.shiftKey

// Delete/Backspace on the focused row. Matches mail's App.svelte behavior
// (both keys trigger delete intent). Consumers MUST stopPropagation so the
// global mail handler doesn't ALSO fire on the focused mail message.
export const LIST_DELETE = (e: KeyboardEvent): boolean =>
  (e.key === 'Delete' || e.key === 'Backspace') && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey

// Pane focus cycling — Alt+H/L / Alt+Left/Right
export const PANE_FOCUS_NEXT = (e: KeyboardEvent): boolean =>
  altOnly(e) && (e.key === 'l' || e.key === 'ArrowRight')

export const PANE_FOCUS_PREV = (e: KeyboardEvent): boolean =>
  altOnly(e) && (e.key === 'h' || e.key === 'ArrowLeft')

// Folder/source navigation within a sidebar — Alt+Up/Down / Alt+J/K
export const SIDEBAR_NEXT = (e: KeyboardEvent): boolean =>
  altOnly(e) && (e.key === 'j' || e.key === 'ArrowDown')

export const SIDEBAR_PREV = (e: KeyboardEvent): boolean =>
  altOnly(e) && (e.key === 'k' || e.key === 'ArrowUp')

// Convenience: namespace export for predicates that the kit imports as a group.
export const KEY = {
  LIST_NEXT,
  LIST_PREV,
  LIST_NEXT_CHECK,
  LIST_PREV_CHECK,
  LIST_TOGGLE_CHECK,
  LIST_OPEN,
  LIST_SELECT_ALL,
  LIST_DELETE,
  PANE_FOCUS_NEXT,
  PANE_FOCUS_PREV,
  SIDEBAR_NEXT,
  SIDEBAR_PREV,
}

// matchesAny is a small helper for cases where several predicates should be
// treated as the same logical action.
export function matchesAny(e: KeyboardEvent, defs: Array<(e: KeyboardEvent) => boolean>): boolean {
  return defs.some(def => def(e))
}
