// Single-use pending deep-link buffer keyed by extension ID. The host's
// global handler for notification clicks (App.svelte's `extension:open`
// listener) stashes the click's path here before flipping the rail tab via
// setActiveExtension. The target extension's pane component then drains
// the buffer on mount and acts on the path.
//
// Why this exists: when a notification is clicked while a different rail
// tab is active, the target extension's pane isn't mounted yet, so a
// direct `EventsOn` subscription in that pane would register AFTER the
// Wails event has already fired. This buffer survives the mount gap.
//
// Buffer policy: at most one pending link per extension. A new link
// supersedes any older un-consumed link for the same extension. Consume
// always clears.

let pending = $state<{ extensionId: string; path: string } | null>(null)

/**
 * Set the pending deep-link target. If a link for the same extension was
 * already pending, it is replaced (last write wins).
 */
export function setPendingDeepLink(extensionId: string, path: string): void {
  pending = { extensionId, path }
}

/**
 * Drain the pending link if it targets `extensionId`. Returns the path on
 * match (clearing the buffer); returns null on no match (and leaves the
 * buffer alone — another extension may consume it).
 */
export function consumePendingDeepLink(extensionId: string): string | null {
  if (pending === null) return null
  if (pending.extensionId !== extensionId) return null
  const path = pending.path
  pending = null
  return path
}
