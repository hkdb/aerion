// Calendar UI view state: which view (month/week/day/agenda), what date
// is anchored, the currently-selected event (for 1E's detail overlay),
// and the focus-mode flag (also 1E).
//
// Not persisted across app sessions in 1D. 1G can wire to
// core.Storage().KV('calendar') if cross-session memory matters.
//
// Date math is tz-aware via toTzDate/fromTzDate (date-fns-tz) — every
// helper reads the user's chosen display timezone from calendarSettings
// at call time, so changes propagate to all dependent $derived values.

import { toTzDate, fromTzDate } from '$extensions/calendar/frontend/lib/tzMath'

export type ViewKind = 'month' | 'week' | 'day' | 'agenda'

let viewKind = $state<ViewKind>('month')
let anchorDate = $state<Date>(startOfMonth(new Date()))
let selectedEventId = $state<string | null>(null)
let eventFocusMode = $state<'off' | 'event'>('off')

// Centralized create-mode composer state. Trigger sites (the "+ Event"
// button in ViewSwitcher, empty-slot click in TimelineView, and the
// Ctrl/Cmd+N shortcut in CalendarPane) all call requestNewEvent() — only
// CalendarPane mounts the dialog, so there's one source of truth instead
// of three independent local mounts. Edit-mode composer (from EventDetail)
// is a different shape (existing event + scope) and keeps its local mount.
let composerOpen = $state(false)
let composerDefaultStart = $state<Date | null>(null)

function requestNewEvent(opts?: { defaultStart?: Date | null }) {
  composerDefaultStart = opts?.defaultStart ?? null
  composerOpen = true
}

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

// --- date helpers (tz-aware via tzMath) -------------------------------------
//
// Each helper wraps its input through toTzDate so native .getFullYear/getMonth/
// getDate/getHours etc. read the wall-clock value in the user's chosen
// display timezone, performs the mutation, then converts back via fromTzDate
// so the returned Date is a real UTC instant. Callers continue to receive
// real UTC Dates suitable for `.getTime() / 1000` boundary math.

function startOfDay(d: Date): Date {
  const z = toTzDate(d)
  z.setHours(0, 0, 0, 0)
  return fromTzDate(z)
}

function startOfMonth(d: Date): Date {
  const z = toTzDate(d)
  z.setDate(1)
  z.setHours(0, 0, 0, 0)
  return fromTzDate(z)
}

function addDays(d: Date, n: number): Date {
  const z = toTzDate(d)
  z.setDate(z.getDate() + n)
  return fromTzDate(z)
}

function addMonths(d: Date, n: number): Date {
  const z = toTzDate(d)
  z.setMonth(z.getMonth() + n)
  return fromTzDate(z)
}

function weekStart(d: Date): Date {
  // Sunday as week start. Matches the Month view grid.
  const z = toTzDate(d)
  z.setDate(z.getDate() - z.getDay())
  z.setHours(0, 0, 0, 0)
  return fromTzDate(z)
}

function monthGridStart(d: Date): Date {
  // First Sunday on/before the 1st of d's month (in tz).
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
  // Getter+setter so consumers can `bind:open={calendarView.composerOpen}`.
  // The dialog (EventComposerDialog) drives both directions: requestNewEvent
  // flips it true; the dialog's internal close path flips it back via bind.
  get composerOpen() { return composerOpen },
  set composerOpen(v: boolean) { composerOpen = v },
  get composerDefaultStart() { return composerDefaultStart },

  setViewKind,
  setAnchorDate,
  goPrev,
  goNext,
  goToday,
  selectEvent,
  toggleEventFocus,
  requestNewEvent,

  // Re-export helpers — MonthView uses them to compute per-cell dates;
  // WeekView uses weekStart for its 7-day window.
  monthGridStart,
  weekStart,
  startOfDay,
  startOfMonth,
  addDays,
  addMonths,
}
