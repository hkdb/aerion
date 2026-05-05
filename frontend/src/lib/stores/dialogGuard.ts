// Tracks open dialogs that should prevent background reloads from destroying
// the component tree (e.g., folder picker dialog dismissed by sync reload).
let count = 0;

export function dialogGuardOpen() {
  count++;
}

export function dialogGuardClose() {
  count--;
}

export function isDialogGuardActive(): boolean {
  return count > 0;
}
