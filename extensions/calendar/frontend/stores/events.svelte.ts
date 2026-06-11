// Calendar events store — cache of expanded EventInstances for the current
// visible range. Refetches on:
//   - explicit fetchRange call
//   - Wails event `calendar:sync-complete` (the syncer publishes this
//     after each successful per-source sync)
//   - via $effect in CalendarPane: changes to
//     calendarSources.visibleCalendarIDs or calendarView.visibleRange

// @ts-ignore - wailsjs bindings
import { Calendar_ListEventsInRange } from '$wailsjs/go/app/App.js'
// @ts-ignore - wailsjs bindings
import { EventsOn } from '$wailsjs/runtime/runtime.js'
// @ts-ignore - wailsjs bindings
import type { backend } from '$wailsjs/go/models'

type EventInstance = backend.EventInstance

let instances = $state<EventInstance[]>([])
let loading = $state(false)
let lastFetchKey = $state<string>('')
let subscribed = false

function fetchKey(calendarIDs: string[], fromUnix: number, toUnix: number): string {
  const ids = [...calendarIDs].sort().join(',')
  return `${ids}|${fromUnix}|${toUnix}`
}

async function fetchRange(calendarIDs: string[], fromUnix: number, toUnix: number) {
  const key = fetchKey(calendarIDs, fromUnix, toUnix)
  lastFetchKey = key

  if (calendarIDs.length === 0) {
    instances = []
    return
  }

  loading = true
  try {
    const result = (await Calendar_ListEventsInRange(calendarIDs, fromUnix, toUnix)) || []
    // Drop the result if a newer fetch superseded us mid-flight.
    if (lastFetchKey !== key) return
    instances = result
  } catch (err) {
    console.error('Failed to fetch events in range:', err)
  } finally {
    if (lastFetchKey === key) loading = false
  }
}

async function refetchLast(calendarIDs: string[], fromUnix: number, toUnix: number) {
  // Same as fetchRange but used by the sync-complete handler to refetch
  // whatever window is currently visible.
  await fetchRange(calendarIDs, fromUnix, toUnix)
}

function initSubscription(getActiveQuery: () => { calendarIDs: string[]; fromUnix: number; toUnix: number }) {
  if (subscribed) return
  subscribed = true
  EventsOn('calendar:sync-complete', () => {
    const q = getActiveQuery()
    void refetchLast(q.calendarIDs, q.fromUnix, q.toUnix)
  })
}

export const events = {
  get instances() { return instances },
  get loading() { return loading },

  fetchRange,
  initSubscription,
}
