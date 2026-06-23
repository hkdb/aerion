// Extension-shortcut registry.
//
// Extensions register their pane-local keyboard shortcuts here at component
// mount; the host's global key handler (App.svelte's handleGlobalKeyDown)
// dispatches via dispatchExtensionShortcut whenever the active rail pane is
// NOT mail. This mirrors how mail dispatches its own switch-case shortcuts
// from the same global handler — extensions get a symmetric path.
//
// The handler always returns true/false from dispatch so the global key
// handler knows whether to continue down its mail-side branch. Predicates
// live in the extension's own
// `extensions/<name>/frontend/keyboard/shortcuts.ts` file (with shared
// helpers imported from `$lib/keyboard/shortcuts`).
//
// Lifetime: callers register at onMount and call the returned Unregister at
// onDestroy. The registry is window-global (single module-level map) — fine
// for now since only one extension can be the active rail pane at a time.

import { getActiveExtension } from './uiState.svelte'

export type ShortcutPredicate = (e: KeyboardEvent) => boolean
export type ShortcutHandler = (e: KeyboardEvent) => void
export type Unregister = () => void

interface Registration {
  predicate: ShortcutPredicate
  handler: ShortcutHandler
}

// Indexed by extensionId → ordered list of registrations.
const registry = new Map<string, Registration[]>()

/**
 * Register a keyboard shortcut scoped to an extension. The shortcut only
 * fires via dispatchExtensionShortcut when `getActiveExtension() === extensionId`.
 *
 * Returns an Unregister function — call it from onDestroy / the component's
 * cleanup to avoid stale handlers piling up across mount/unmount cycles.
 *
 * Multiple shortcuts per extension are supported; they're evaluated in
 * registration order, first match wins.
 */
export function registerExtensionShortcut(
  extensionId: string,
  predicate: ShortcutPredicate,
  handler: ShortcutHandler,
): Unregister {
  const reg: Registration = { predicate, handler }
  const existing = registry.get(extensionId)
  if (existing) {
    existing.push(reg)
  }
  if (!existing) {
    registry.set(extensionId, [reg])
  }
  return () => {
    const list = registry.get(extensionId)
    if (!list) return
    const idx = list.indexOf(reg)
    if (idx >= 0) list.splice(idx, 1)
    if (list.length === 0) registry.delete(extensionId)
  }
}

/**
 * Dispatch a keyboard event to the currently-active extension's registered
 * shortcuts. Called from App.svelte's global key handler.
 *
 * Returns true when a handler ran (caller should treat the event as handled —
 * don't run mail's downstream dispatch). Returns false when nothing matched
 * (caller continues to its own logic).
 *
 * Callers gate on isMailActive() / isDialogGuardActive() / inInput BEFORE
 * invoking this — those are host-level concerns the dispatcher doesn't repeat.
 */
export function dispatchExtensionShortcut(e: KeyboardEvent): boolean {
  const ext = getActiveExtension()
  if (!ext || ext === 'mail') return false
  const list = registry.get(ext)
  if (!list) return false
  for (const reg of list) {
    if (reg.predicate(e)) {
      reg.handler(e)
      return true
    }
  }
  return false
}
