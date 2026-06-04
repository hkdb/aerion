// Outbox state — tracks messages currently being sent (or queued in the undo
// window) so the UI can show a non-blocking "sending" indicator while the user
// keeps working. The actual send orchestration (undo timer, network send) lives
// in App.svelte; this store just exposes the reactive count.

let activeSends = $state(0)

/** True while one or more messages are queued or in-flight. */
export function getIsSending(): boolean {
  return activeSends > 0
}

/** Mark a send as started (queued in the undo window or in-flight). */
export function sendStarted(): void {
  activeSends++
}

/** Mark a send as finished (sent, canceled, or failed). */
export function sendFinished(): void {
  activeSends = Math.max(0, activeSends - 1)
}
