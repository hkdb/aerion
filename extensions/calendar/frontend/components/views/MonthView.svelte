<script lang="ts">
  // MonthView — 6-row × 7-col grid showing the anchored month plus spill
  // days from adjacent months. Reads events from the events store (already
  // expanded into EventInstances for the visible range).
  //
  // Per-cell rendering:
  //   - Day number top-left. Muted for other-month days. Circled for today.
  //   - Up to 3 visible event pills sorted by InstanceStartUnix.
  //   - "+N more" overflow indicator at the bottom of overcrowded cells.
  //   - Multi-day events render as a continuous band — Phase 1F polish
  //     adds the proper "spans across cells" layout; 1D ships each cell
  //     showing the event as a normal pill (so a 3-day event shows in 3
  //     cells with the same summary, which is acceptable for v1).
  //
  // Click an empty area of a day → switch to DayView anchored there (the
  // DayView is a "coming soon" placeholder in 1D; the navigation works).
  // Click an event pill → calendarView.selectEvent(instance.id).

  import { _ } from 'svelte-i18n'
  import EventCard from '$extensions/calendar/frontend/components/EventCard.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { toTzDate } from '$extensions/calendar/frontend/lib/tzMath'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  const MAX_EVENTS_PER_CELL = 3

  // 6 rows × 7 cols = 42 cells starting from the grid-start (in tz).
  const gridStart = $derived(calendarView.monthGridStart(calendarView.anchorDate))
  const anchorMonth = $derived(toTzDate(calendarView.anchorDate).getMonth())
  const today = $derived(calendarView.startOfDay(new Date()).getTime())

  const cells = $derived.by(() => {
    const out: { date: Date; isOtherMonth: boolean; isToday: boolean }[] = []
    for (let i = 0; i < 42; i++) {
      const date = calendarView.addDays(gridStart, i)
      out.push({
        date,
        isOtherMonth: toTzDate(date).getMonth() !== anchorMonth,
        isToday: date.getTime() === today,
      })
    }
    return out
  })

  // Group events by day-of-grid. An event overlaps a day if its
  // [start, end] intersects [dayStart, dayEnd).
  const eventsByCell = $derived.by(() => {
    const out: backend.EventInstance[][] = Array.from({ length: 42 }, () => [])
    if (events.instances.length === 0) return out

    for (let i = 0; i < 42; i++) {
      const dayStart = cells[i].date.getTime() / 1000
      const dayEnd = dayStart + 86400
      for (const inst of events.instances) {
        // Overlap test: instance ends after day starts AND instance starts before day ends.
        if (inst.instanceEndUnix > dayStart && inst.instanceStartUnix < dayEnd) {
          out[i].push(inst)
        }
      }
      // Sort each cell by start time so the time-prefixed labels render in order.
      out[i].sort((a, b) => a.instanceStartUnix - b.instanceStartUnix)
    }
    return out
  })

  const weekdayLabels = $derived([
    $_('calendar.month.weekdayShort.sun'),
    $_('calendar.month.weekdayShort.mon'),
    $_('calendar.month.weekdayShort.tue'),
    $_('calendar.month.weekdayShort.wed'),
    $_('calendar.month.weekdayShort.thu'),
    $_('calendar.month.weekdayShort.fri'),
    $_('calendar.month.weekdayShort.sat'),
  ])

  function onCellClick(date: Date) {
    calendarView.setViewKind('day')
    calendarView.setAnchorDate(date)
  }

  function onEventClick(inst: backend.EventInstance) {
    calendarView.selectEvent(inst.id)
  }

  const noSources = $derived(calendarSources.sources.length === 0)
</script>

<div class="flex-1 flex flex-col min-h-0 bg-background">
  {#if noSources}
    <div class="flex-1 flex items-center justify-center text-muted-foreground text-sm px-6 text-center">
      {$_('calendar.month.emptyState')}
    </div>
  {/if}

  {#if !noSources}
    <!-- Weekday header row -->
    <div class="grid grid-cols-7 border-b border-border bg-muted/20">
      {#each weekdayLabels as label, i (i)}
        <div class="px-2 py-1 text-[11px] font-medium text-muted-foreground uppercase tracking-wide text-center">
          {label}
        </div>
      {/each}
    </div>

    <!-- 6-row grid -->
    <div class="flex-1 grid grid-cols-7 grid-rows-6 min-h-0">
      {#each cells as cell, i (i)}
        {@const cellEvents = eventsByCell[i]}
        {@const visibleEvents = cellEvents.slice(0, MAX_EVENTS_PER_CELL)}
        {@const overflow = cellEvents.length - visibleEvents.length}
        <button
          type="button"
          class="flex flex-col gap-0.5 p-1 text-left border-b border-r border-border min-h-0 overflow-hidden
                 hover:bg-muted/30 transition-colors
                 {cell.isOtherMonth ? 'bg-muted/10' : 'bg-background'}"
          onclick={() => onCellClick(cell.date)}
        >
          <div class="flex items-center justify-start shrink-0">
            <span
              class="inline-flex items-center justify-center text-xs leading-none w-5 h-5 rounded-full
                     {cell.isToday ? 'bg-primary text-primary-foreground font-semibold' : ''}
                     {cell.isOtherMonth && !cell.isToday ? 'text-muted-foreground/50' : 'text-foreground'}"
            >
              {toTzDate(cell.date).getDate()}
            </span>
          </div>
          <div class="flex-1 flex flex-col gap-0.5 min-h-0 overflow-hidden">
            {#each visibleEvents as inst (inst.id + ':' + inst.instanceStartUnix)}
              <EventCard
                instance={inst}
                color={calendarSources.colorOf(inst.calendarId)}
                onclick={() => onEventClick(inst)}
              />
            {/each}
            {#if overflow > 0}
              <span class="text-[10px] text-muted-foreground px-1">
                {$_('calendar.month.moreEventsHidden', { values: { n: overflow } })}
              </span>
            {/if}
          </div>
        </button>
      {/each}
    </div>
  {/if}
</div>
