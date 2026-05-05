// Frontend cache for the image allowlist.
// Eliminates per-message async Wails calls to IsImageAllowed(),
// which can saturate the WebKit bridge on rapid IDLE syncs.
// @ts-ignore - wailsjs path
import { GetImageAllowlist } from "../../../wailsjs/go/app/App";

interface AllowlistEntry {
  type: string;
  value: string;
}

// Plain variables (NOT $state) — this cache is read imperatively, not reactively.
// Using $state would cause every EmailBody $effect to subscribe to the array,
// re-triggering all iframe rebuilds when the allowlist loads at startup.
let entries: AllowlistEntry[] = [];
let loaded = false;

export async function loadImageAllowlist() {
  try {
    const list = await GetImageAllowlist();
    entries = list || [];
    loaded = true;
  } catch (err) {
    console.error("[imageAllowlist] Failed to load:", err);
    // Mark as loaded even on error so content isn't permanently blocked
    loaded = true;
  }
}

export function isImageAllowedSync(email: string): boolean {
  if (!loaded || !email) return false;
  const normalized = email.toLowerCase().trim();
  const parts = normalized.split("@");
  if (parts.length !== 2) return false;
  const domain = parts[1];
  return entries.some(
    (e) =>
      (e.type === "sender" && e.value === normalized) ||
      (e.type === "domain" && e.value === domain)
  );
}

export function refreshImageAllowlist() {
  loadImageAllowlist();
}
