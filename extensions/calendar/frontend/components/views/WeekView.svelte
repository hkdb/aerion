<script lang="ts">
  // WeekView — 7-column composition of the shared TimelineView. The visible
  // week is anchored by calendarView.anchorDate via the store's weekStart
  // helper (Sunday as week start, matching MonthView's grid).

  import TimelineView from './TimelineView.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'

  // Derive the 7-day window from the tz-aware weekStart helper. The helper
  // returns a real UTC Date of Sunday-midnight in the user's chosen tz, so
  // addDays(+i) walks the visible week correctly across DST.
  const dates = $derived.by(() => {
    const start = calendarView.weekStart(calendarView.anchorDate)
    return [0, 1, 2, 3, 4, 5, 6].map(i => calendarView.addDays(start, i))
  })
</script>

<TimelineView {dates} />
