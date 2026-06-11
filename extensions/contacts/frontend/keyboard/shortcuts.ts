// Contacts extension's keyboard shortcut predicates.
//
// Lives inside the extension's own directory so the Contacts extension is
// self-contained — adding/removing the extension doesn't require touching
// host-side keyboard files. Mail and the kit consume their shared predicates
// from `$lib/keyboard/shortcuts.ts`; extensions own theirs.
//
// Composable helpers (noMods, ctrlOrMeta, altOnly) are imported from the host
// shortcuts file so the modifier-checking conventions match mail's exactly.
// Predicates here get registered via registerExtensionShortcut at component
// mount; the host's global key handler dispatches them via
// dispatchExtensionShortcut when the Contacts extension is the active rail
// pane.

import { noMods, ctrlOrMeta } from '$lib/keyboard/shortcuts'

/** `e` — edit the currently-focused contact. */
export const CONTACT_EDIT = (e: KeyboardEvent): boolean =>
  e.key === 'e' && noMods(e)

/** `Ctrl/Cmd+N` — open the new-contact dialog, pre-targeted to the
 *  sidebar-focused addressbook (the dialog's own `autoFillFromSidebar`
 *  reads `contactsView.selectedSourceId` and falls back to local when
 *  the focused source isn't writable). Routed by the extension shortcut
 *  registry before App.svelte's mail-domain switch — only fires when
 *  contacts is the active rail. */
export const CONTACT_NEW = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 'n' && ctrlOrMeta(e) && !e.shiftKey && !e.altKey

/** `Ctrl/Cmd+Shift+A` — sync every configured contact source. Mirrors
 *  mail's Ctrl+Shift+A "sync all accounts" and calendar's
 *  CALENDAR_SYNC_ALL — the same chord routes to whichever extension is
 *  the active rail. */
export const CONTACT_SYNC_ALL = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 'a' && ctrlOrMeta(e) && e.shiftKey

/** `Ctrl/Cmd+Shift+S` — sync the contact source the sidebar currently
 *  has focused (resolved at dispatch time from contactsView.selectedSourceId).
 *  Built-in entries (All / local / local:manual / local:collected) have
 *  no remote to sync — the handler toasts a warning instead of calling
 *  the backend. */
export const CONTACT_SYNC_FOCUSED = (e: KeyboardEvent): boolean =>
  e.key.toLowerCase() === 's' && ctrlOrMeta(e) && e.shiftKey

export const KEY = {
  CONTACT_EDIT,
  CONTACT_NEW,
  CONTACT_SYNC_ALL,
  CONTACT_SYNC_FOCUSED,
}
