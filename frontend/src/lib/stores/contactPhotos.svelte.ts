// Session cache of contact profile photos, keyed by lowercased email, for the
// opt-in "Show contact photos in message list" feature.
//
// Batched by design: ensure() fetches every not-yet-known email in ONE Wails
// call; message rows read via get(). Misses are cached as `null` so they are
// never re-queried, and an `inflight` guard prevents the same email being
// fetched twice across overlapping/rapid ensure() calls (fast folder switching).
// Reads the local contact DB only — no network — so it works offline.

// @ts-ignore - wailsjs bindings
import { GetContactPhotos } from '../../../wailsjs/go/app/App.js'
// @ts-ignore - wailsjs bindings
import { EventsOn } from '../../../wailsjs/runtime/runtime'

type Photo = { data: string; mediaType: string }

// Reactive cache: reassigned on each merge so row reads re-render when photos
// arrive. `null` = looked up, no inline photo. Missing key = not yet fetched.
let cache = $state<Record<string, Photo | null>>({})

// Non-reactive dedupe set of emails with a fetch in flight.
const inflight = new Set<string>()

// The most recent batch the message list asked for — re-fetched on invalidate
// so newly-synced photos appear without an app restart.
let lastEmails: string[] = []

// Subscribe to `contacts:changed` exactly once (lazily, on first use). The host
// fires it after any contact source sync (CardDAV/Google/MS background, post-add,
// or manual) as well as local edits — meaning a contact's photo may have changed,
// so drop the session cache and re-fetch what's currently visible.
let subscribed = false
function subscribeOnce() {
  if (subscribed) return
  subscribed = true
  EventsOn('contacts:changed', () => invalidate())
}

function norm(email: string): string {
  return email.trim().toLowerCase()
}

// Clear the session cache and re-fetch the last-requested emails. Repopulating
// `cache` reassigns it, so the reactive reads in message rows re-render.
function invalidate(): void {
  cache = {}
  inflight.clear()
  if (lastEmails.length) void ensure(lastEmails)
}

async function ensure(emails: string[]): Promise<void> {
  subscribeOnce()
  lastEmails = emails
  const missing: string[] = []
  const seen = new Set<string>()
  for (const raw of emails) {
    const e = norm(raw)
    if (!e || seen.has(e)) continue
    seen.add(e)
    if (e in cache || inflight.has(e)) continue
    missing.push(e)
  }
  if (missing.length === 0) return

  for (const e of missing) inflight.add(e)
  try {
    const results = (await GetContactPhotos(missing)) || []
    // Seed every requested email as a miss, then overwrite the ones that
    // returned a photo — so misses are cached and won't be re-queried.
    const updates: Record<string, Photo | null> = {}
    for (const e of missing) updates[e] = null
    for (const r of results) {
      if (r?.email) updates[norm(r.email)] = { data: r.data, mediaType: r.mediaType }
    }
    cache = { ...cache, ...updates }
  } catch (err) {
    console.error('Failed to fetch contact photos:', err)
  } finally {
    for (const e of missing) inflight.delete(e)
  }
}

function get(email: string): Photo | undefined {
  return cache[norm(email)] ?? undefined
}

export const contactPhotos = { ensure, get, invalidate }
