<script lang="ts">
  // DayView — single-column composition of the shared TimelineView. Anchored
  // by calendarView.anchorDate (already normalised to start-of-day by the
  // store's setAnchorDate / goToday / goPrev / goNext helpers).

  import TimelineView from './TimelineView.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'

  const dates = $derived([calendarView.startOfDay(calendarView.anchorDate)])
</script>

<TimelineView {dates} />
