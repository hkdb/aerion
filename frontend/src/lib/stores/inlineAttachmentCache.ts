/**
 * In-memory cache for inline attachments within a session.
 *
 * This cache avoids redundant API calls when switching between messages.
 * The backend already stores inline attachment content in SQLite for offline access,
 * but this frontend cache prevents unnecessary round-trips when revisiting messages.
 *
 * Cache is unlimited (no eviction) - grows throughout the session.
 * Memory is released when the app is closed/refreshed.
 */

// Map of messageId -> Record<contentId, dataUrl>
const cache = new Map<string, Record<string, string>>();

/**
 * Get cached inline attachments for a message
 * @param messageId The message ID to look up
 * @returns The cached data URL map, or null if not cached
 */
export function getCached(messageId: string): Record<string, string> | null {
  return cache.get(messageId) ?? null;
}

/**
 * Store inline attachments in the cache
 * @param messageId The message ID to cache for
 * @param data The content-id to data URL map
 */
export function setCache(
  messageId: string,
  data: Record<string, string>
): void {
  cache.set(messageId, data);
}

/**
 * Clear the entire cache
 * Can be used for memory management if needed
 */
export function clearCache(): void {
  cache.clear();
}

/**
 * Get the current cache size (number of messages cached)
 * Useful for debugging
 */
export function getCacheSize(): number {
  return cache.size;
}
