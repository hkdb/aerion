<script lang="ts">
  // AgendaView — flattened, chronologically-grouped event list built on the
  // kit ListPane. Each event renders as a ListRow; a date-header divider is
  // rendered inside the row snippet ABOVE the first event of each day.
  // All-day events render as regular rows with an "all day" prefix instead
  // of a time, so they're keyboard-navigable like everything else.
  //
  // Window: calendarView.visibleRange (14 days forward by store contract).
  // Empty state: calendar.agenda.empty.
  //
  // Selection model:
  //   - selectedRowId tracks the highlighted row id (local — survives
  //     navigation but is independent from calendarView.selectedEventId,
  //     which only flips when the user activates Enter or clicks to open
  //     the DetailOverlay).
  //   - Enter (LIST_OPEN) → calendarView.selectEvent → opens DetailOverlay.

  import { _, locale } from 'svelte-i18n'
  import ListPane from '$lib/components/kit/ListPane.svelte'
  import ListRow from '$lib/components/kit/ListRow.svelte'
  import ListHeader from '$lib/components/kit/ListHeader.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  type EventRow = {
    id: string                 // instance.id
    instance: backend.EventInstance
    date: Date                 // local-day this row belongs to
    isFirstInDay: boolean      // render day header above this row
  }

  let selectedRowId = $state<string | null>(null)

  // Locale-aware AND tz-aware formatters: locale via $locale, timezone
  // via the user's chosen display timezone.
  const dateFmt = $derived(new Intl.DateTimeFormat($locale || undefined, {
    weekday: 'long', month: 'short', day: 'numeric',
    timeZone: calendarSettings.effectiveTimezone,
  }))
  const timeFmt = $derived(new Intl.DateTimeFormat($locale || undefined, {
    hour: '2-digit', minute: '2-digit',
    timeZone: calendarSettings.effectiveTimezone,
  }))
  const rangeFmt = $derived(new Intl.DateTimeFormat($locale || undefined, {
    month: 'short', day: 'numeric',
    timeZone: calendarSettings.effectiveTimezone,
  }))

  // Flatten events into rows. Sort by day (using tz-aware startOfDay so
  // grouping reflects the user's chosen tz), then all-day-first within
  // day, then by start time. Mark `isFirstInDay` on the first of each day.
  const rows = $derived.by<EventRow[]>(() => {
    const out: { id: string; instance: backend.EventInstance; date: Date }[] = []
    for (const inst of events.instances) {
      const date = calendarView.startOfDay(new Date(inst.instanceStartUnix * 1000))
      out.push({ id: inst.id, instance: inst, date })
    }
    out.sort((a, b) => {
      const da = a.date.getTime()
      const db = b.date.getTime()
      if (da !== db) return da - db
      const aaa = a.instance.isAllDay ? 0 : 1
      const bbb = b.instance.isAllDay ? 0 : 1
      if (aaa !== bbb) return aaa - bbb
      return a.instance.instanceStartUnix - b.instance.instanceStartUnix
    })

    let prevDayMs = NaN
    const finished: EventRow[] = []
    for (const r of out) {
      const dayMs = r.date.getTime()
      finished.push({ ...r, isFirstInDay: dayMs !== prevDayMs })
      prevDayMs = dayMs
    }
    return finished
  })

  function onSelect(id: string) {
    selectedRowId = id
  }

  function onActivate(id: string) {
    calendarView.selectEvent(id)
  }

  // Header label combines the existing `calendar.viewSwitcher.agenda` key with
  // an ICU-templated date range. Both parts come from i18n — no raw English.
  const headerLabel = $derived.by(() => {
    const r = calendarView.visibleRange
    const fromDate = new Date(r.fromUnix * 1000)
    const toDate = new Date((r.toUnix - 1) * 1000) // inclusive end
    const range = $_('calendar.agenda.rangeLabel', {
      values: { from: rangeFmt.format(fromDate), to: rangeFmt.format(toDate) },
    })
    return `${$_('calendar.viewSwitcher.agenda')} · ${range}`
  })

  function calendarLabel(inst: backend.EventInstance): string {
    for (const src of calendarSources.sources) {
      const cals = calendarSources.calendarsBySource[src.id] || []
      for (const cal of cals) {
        if (cal.id === inst.calendarId) {
          return `${src.name} / ${cal.displayName}`
        }
      }
    }
    return ''
  }
</script>

<div class="flex-1 flex flex-col min-h-0 bg-background">
  <ListHeader label={headerLabel} />

  <ListPane
    items={rows}
    selectedId={selectedRowId}
    label={$_('calendar.viewSwitcher.agenda')}
    {onSelect}
    {onActivate}
  >
    {#snippet row(item: EventRow, ctx)}
      {#if item.isFirstInDay}
        <div class="px-4 py-2 bg-muted/30 text-xs font-medium text-muted-foreground uppercase tracking-wide border-b border-border">
          {dateFmt.format(item.date)}
        </div>
      {/if}
      <ListRow
        selected={ctx.selected}
        density="compact"
        onclick={() => onActivate(item.id)}
      >
        <span
          class="shrink-0 inline-block w-2.5 h-2.5 rounded-full"
          style:background-color={calendarSources.colorOf(item.instance.calendarId)}
          aria-hidden="true"
        ></span>
        <span class="shrink-0 w-16 font-mono text-xs text-muted-foreground">
          {item.instance.isAllDay
            ? $_('calendar.agenda.allDayPrefix')
            : timeFmt.format(new Date(item.instance.instanceStartUnix * 1000))}
        </span>
        <span class="flex-1 min-w-0 truncate text-sm text-foreground">
          {item.instance.summary || ''}
        </span>
        <span class="shrink-0 hidden md:inline truncate max-w-[40%] text-xs text-muted-foreground">
          {calendarLabel(item.instance)}
        </span>
      </ListRow>
    {/snippet}

    {#snippet empty()}
      <p class="m-4 text-sm text-muted-foreground">{$_('calendar.agenda.empty')}</p>
    {/snippet}
  </ListPane>
</div>
