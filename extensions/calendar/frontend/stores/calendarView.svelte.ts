// Calendar UI view state: which view (month/week/day/agenda), what date
// is anchored, the currently-selected event (for 1E's detail overlay),
// and the focus-mode flag (also 1E).
//
// Not persisted across app sessions in 1D. 1G can wire to
// core.Storage().KV('calendar') if cross-session memory matters.

export type ViewKind = 'month' | 'week' | 'day' | 'agenda'

let viewKind = $state<ViewKind>('month')
let anchorDate = $state<Date>(startOfMonth(new Date()))
let selectedEventId = $state<string | null>(null)
let eventFocusMode = $state<'off' | 'event'>('off')

// The visible UTC window for the active view + anchor. Derived so the
// events store can re-fetch whenever the view or anchor changes.
//
// Month: the 6-row × 7-col grid window — from the first Sunday on/before
// the 1st of the anchored month, to the last Saturday on/after the last
// day of that month + spill into next month. For day/week/agenda we use
// simple offsets — those aren't wired in 1D but the math is here for 1F.
const visibleRange = $derived.by<{ fromUnix: number; toUnix: number }>(() => {
  if (viewKind === 'month') {
    const start = monthGridStart(anchorDate)
    const end = addDays(start, 42) // 6 rows × 7 days
    return { fromUnix: secondsAt(start), toUnix: secondsAt(end) }
  }
  if (viewKind === 'week') {
    const start = weekStart(anchorDate)
    return { fromUnix: secondsAt(start), toUnix: secondsAt(addDays(start, 7)) }
  }
  if (viewKind === 'day') {
    const start = startOfDay(anchorDate)
    return { fromUnix: secondsAt(start), toUnix: secondsAt(addDays(start, 1)) }
  }
  // agenda: 2 weeks centered on anchor (≈10 days back, 4 forward feels weird;
  // use 14d forward starting from anchor for simplicity).
  const start = startOfDay(anchorDate)
  return { fromUnix: secondsAt(start), toUnix: secondsAt(addDays(start, 14)) }
})

function setViewKind(k: ViewKind) {
  // Anchor-snap when leaving Month: jump to today if the visible month
  // contains today, else the 1st of the visible month (already anchorDate).
  // Predictable land-position when switching out of Month.
  if (viewKind === 'month' && k !== 'month') {
    const today = startOfDay(new Date())
    const sameMonth = anchorDate.getFullYear() === today.getFullYear()
      && anchorDate.getMonth() === today.getMonth()
    if (sameMonth) anchorDate = today
  }
  viewKind = k
  selectedEventId = null
  eventFocusMode = 'off'
}

function setAnchorDate(d: Date) {
  anchorDate = startOfDay(d)
}

function goPrev() {
  if (viewKind === 'month') {
    anchorDate = startOfMonth(addMonths(anchorDate, -1))
    return
  }
  if (viewKind === 'week' || viewKind === 'agenda') {
    anchorDate = addDays(anchorDate, -7)
    return
  }
  if (viewKind === 'day') {
    anchorDate = addDays(anchorDate, -1)
  }
}

function goNext() {
  if (viewKind === 'month') {
    anchorDate = startOfMonth(addMonths(anchorDate, 1))
    return
  }
  if (viewKind === 'week' || viewKind === 'agenda') {
    anchorDate = addDays(anchorDate, 7)
    return
  }
  if (viewKind === 'day') {
    anchorDate = addDays(anchorDate, 1)
  }
}

function goToday() {
  if (viewKind === 'month') {
    anchorDate = startOfMonth(new Date())
    return
  }
  anchorDate = startOfDay(new Date())
}

function selectEvent(id: string | null) {
  selectedEventId = id
  eventFocusMode = 'off'
}

function toggleEventFocus() {
  if (eventFocusMode === 'event') {
    eventFocusMode = 'off'
    return
  }
  if (selectedEventId !== null) {
    eventFocusMode = 'event'
  }
}

// --- date helpers (local-tz) -------------------------------------------------

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

function startOfMonth(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1)
}

function addDays(d: Date, n: number): Date {
  const out = new Date(d)
  out.setDate(out.getDate() + n)
  return out
}

function addMonths(d: Date, n: number): Date {
  const out = new Date(d)
  out.setMonth(out.getMonth() + n)
  return out
}

function weekStart(d: Date): Date {
  // Sunday as week start. Matches the Month view grid.
  const out = startOfDay(d)
  out.setDate(out.getDate() - out.getDay())
  return out
}

function monthGridStart(d: Date): Date {
  // First Sunday on/before the 1st of d's month.
  return weekStart(startOfMonth(d))
}

function secondsAt(d: Date): number {
  return Math.floor(d.getTime() / 1000)
}

export const calendarView = {
  get viewKind() { return viewKind },
  get anchorDate() { return anchorDate },
  get selectedEventId() { return selectedEventId },
  get eventFocusMode() { return eventFocusMode },
  get visibleRange() { return visibleRange },

  setViewKind,
  setAnchorDate,
  goPrev,
  goNext,
  goToday,
  selectEvent,
  toggleEventFocus,

  // Re-export helpers — MonthView uses them to compute per-cell dates.
  monthGridStart,
  startOfDay,
  startOfMonth,
  addDays,
  addMonths,
}
