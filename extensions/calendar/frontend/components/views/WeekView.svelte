<script lang="ts">
  // WeekView — 7-column composition of the shared TimelineView. The visible
  // week is anchored by calendarView.anchorDate via the store's weekStart
  // helper (Sunday as week start, matching MonthView's grid).

  import TimelineView from './TimelineView.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'

  // weekStart isn't directly exported; addDays + monthGridStart are.
  // Reconstruct: start = anchor - anchor.getDay() (Sunday).
  const dates = $derived.by(() => {
    const a = calendarView.anchorDate
    const start = new Date(a.getFullYear(), a.getMonth(), a.getDate() - a.getDay())
    return [0, 1, 2, 3, 4, 5, 6].map(i => calendarView.addDays(start, i))
  })
</script>

<TimelineView {dates} />
