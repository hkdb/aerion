<script lang="ts">
  // MiniCalendar — the toolbar title as a popover trigger for quick
  // navigation. Three drill levels (days ↔ months ↔ years): the header
  // label drills UP, cells drill DOWN, and the terminal level sets the
  // view anchor and closes. Terminal granularity is view-aware: Month
  // view completes at a month, Week view selects whole weeks in the day
  // grid, Day/Agenda complete at a day. The ‹ › pagers only move the
  // local browse cursor — the live anchor (and thus event fetches) is
  // untouched until something is actually picked.
  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { Popover } from 'bits-ui'
  import { cn } from '$lib/utils'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { toTzDate } from '$extensions/calendar/frontend/lib/tzMath'

  interface Props {
    /** The toolbar title string (already formatted by ViewSwitcher) */
    title: string
  }

  let { title }: Props = $props()

  let open = $state(false)
  type Level = 'days' | 'months' | 'years'
  let level = $state<Level>('days')
  // Browse cursor — where the popover is looking, independent of the live
  // anchor until a pick happens. Reset on every open.
  let cursor = $state<Date>(new Date())

  function handleOpenChange(v: boolean) {
    open = v
    if (v) {
      level = 'days'
      cursor = calendarView.startOfMonth(calendarView.anchorDate)
    }
  }

  // ── Days level ──────────────────────────────────────────────────────
  const weekdayLabels = $derived([
    $_('calendar.month.weekdayShort.sun'),
    $_('calendar.month.weekdayShort.mon'),
    $_('calendar.month.weekdayShort.tue'),
    $_('calendar.month.weekdayShort.wed'),
    $_('calendar.month.weekdayShort.thu'),
    $_('calendar.month.weekdayShort.fri'),
    $_('calendar.month.weekdayShort.sat'),
  ])

  // 6 rows × 7 cols from the grid start, same math as MonthView
  const dayRows = $derived.by(() => {
    const gridStart = calendarView.monthGridStart(cursor)
    const cursorMonth = toTzDate(cursor).getMonth()
    const today = calendarView.startOfDay(new Date()).getTime()
    const anchor = calendarView.startOfDay(calendarView.anchorDate).getTime()
    const rows = []
    for (let r = 0; r < 6; r++) {
      const cells = []
      for (let c = 0; c < 7; c++) {
        const date = calendarView.addDays(gridStart, r * 7 + c)
        cells.push({
          date,
          label: toTzDate(date).getDate(),
          isOtherMonth: toTzDate(date).getMonth() !== cursorMonth,
          isToday: date.getTime() === today,
          isAnchor: date.getTime() === anchor,
        })
      }
      rows.push({ cells, isAnchorWeek: calendarView.weekStart(calendarView.anchorDate).getTime() === cells[0].date.getTime() })
    }
    return rows
  })

  const daysHeader = $derived(
    new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' }).format(cursor)
  )

  // Week view selects by week: the whole row hovers/selects as one unit
  const weekMode = $derived(calendarView.viewKind === 'week')

  function pickDay(d: Date) {
    calendarView.setAnchorDate(d)
    open = false
  }

  // ── Months level ────────────────────────────────────────────────────
  const monthNames = $derived.by(() => {
    const fmt = new Intl.DateTimeFormat(undefined, { month: 'short' })
    return Array.from({ length: 12 }, (_unused, m) => fmt.format(new Date(2000, m, 1)))
  })

  function pickMonth(m: number) {
    const target = new Date(cursor.getFullYear(), m, 1)
    // Month view completes here — other views drill down to pick a day/week
    if (calendarView.viewKind === 'month') {
      calendarView.setAnchorDate(target)
      open = false
      return
    }
    cursor = target
    level = 'days'
  }

  // ── Years level ─────────────────────────────────────────────────────
  const yearPageStart = $derived(Math.floor(cursor.getFullYear() / 12) * 12)

  function pickYear(y: number) {
    cursor = new Date(y, cursor.getMonth(), 1)
    level = 'months'
  }

  // ── Header pager: moves the browse cursor at the current level ──────
  function page(dir: -1 | 1) {
    switch (level) {
      case 'days':
        cursor = calendarView.startOfMonth(calendarView.addMonths(cursor, dir))
        return
      case 'months':
        cursor = new Date(cursor.getFullYear() + dir, cursor.getMonth(), 1)
        return
      case 'years':
        cursor = new Date(cursor.getFullYear() + dir * 12, cursor.getMonth(), 1)
    }
  }

  function drillUp() {
    if (level === 'days') {
      level = 'months'
      return
    }
    if (level === 'months') {
      level = 'years'
    }
  }
</script>

<Popover.Root bind:open onOpenChange={handleOpenChange}>
  <Popover.Trigger
    class="flex items-center gap-1 ml-2 min-w-0 rounded-md px-1.5 py-0.5 hover:bg-muted transition-colors"
    aria-label={$_('calendar.viewSwitcher.pickDate')}
    title={$_('calendar.viewSwitcher.pickDate')}
  >
    <h2 class="text-sm font-semibold text-foreground truncate">{title}</h2>
    <Icon icon="mdi:chevron-down" class="w-4 h-4 text-muted-foreground shrink-0" />
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      side="bottom"
      align="start"
      sideOffset={4}
      class={cn(
        'z-50 w-[280px] rounded-md border bg-popover p-2 text-popover-foreground shadow-md',
        'data-[state=open]:animate-in data-[state=closed]:animate-out',
        'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
        'data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95',
        'data-[side=bottom]:slide-in-from-top-2'
      )}
    >
      <!-- Header: ‹ label › — label click drills up -->
      <div class="flex items-center justify-between mb-1">
        <button
          type="button"
          class="p-1.5 rounded-md hover:bg-muted transition-colors"
          aria-label={$_('calendar.viewSwitcher.prev')}
          onclick={() => page(-1)}
        >
          <Icon icon="mdi:chevron-left" class="w-4 h-4 text-muted-foreground" />
        </button>
        <button
          type="button"
          class="text-sm font-semibold rounded-md px-2 py-1 transition-colors {level === 'years' ? 'cursor-default' : 'hover:bg-muted'}"
          onclick={drillUp}
        >
          {#if level === 'days'}
            {daysHeader}
          {:else if level === 'months'}
            {cursor.getFullYear()}
          {:else}
            {yearPageStart}–{yearPageStart + 11}
          {/if}
        </button>
        <button
          type="button"
          class="p-1.5 rounded-md hover:bg-muted transition-colors"
          aria-label={$_('calendar.viewSwitcher.next')}
          onclick={() => page(1)}
        >
          <Icon icon="mdi:chevron-right" class="w-4 h-4 text-muted-foreground" />
        </button>
      </div>

      {#if level === 'days'}
        <div class="grid grid-cols-7 mb-1">
          {#each weekdayLabels as wd (wd)}
            <div class="text-center text-[10px] font-medium text-muted-foreground py-0.5">{wd}</div>
          {/each}
        </div>
        {#each dayRows as row, r (r)}
          <div
            class="grid grid-cols-7 rounded-md
                   {weekMode ? 'hover:bg-muted transition-colors' : ''}
                   {weekMode && row.isAnchorWeek ? 'bg-primary/20' : ''}"
          >
            {#each row.cells as cell (cell.date.getTime())}
              <button
                type="button"
                class="h-8 text-xs rounded-md transition-colors
                       {weekMode ? '' : 'hover:bg-muted'}
                       {cell.isOtherMonth ? 'text-muted-foreground/50' : 'text-foreground'}
                       {!weekMode && cell.isAnchor ? 'bg-primary text-primary-foreground hover:bg-primary' : ''}
                       {cell.isToday && !(cell.isAnchor && !weekMode) ? 'ring-1 ring-inset ring-primary' : ''}"
                onclick={() => pickDay(cell.date)}
              >
                {cell.label}
              </button>
            {/each}
          </div>
        {/each}
      {:else if level === 'months'}
        <div class="grid grid-cols-4 gap-1">
          {#each monthNames as name, m (m)}
            <button
              type="button"
              class="h-9 text-xs rounded-md hover:bg-muted transition-colors
                     {m === toTzDate(calendarView.anchorDate).getMonth() && cursor.getFullYear() === calendarView.anchorDate.getFullYear() ? 'bg-primary text-primary-foreground hover:bg-primary' : 'text-foreground'}"
              onclick={() => pickMonth(m)}
            >
              {name}
            </button>
          {/each}
        </div>
      {:else}
        <div class="grid grid-cols-4 gap-1">
          {#each Array.from({ length: 12 }, (_unused, i) => yearPageStart + i) as y (y)}
            <button
              type="button"
              class="h-9 text-xs rounded-md hover:bg-muted transition-colors
                     {y === calendarView.anchorDate.getFullYear() ? 'bg-primary text-primary-foreground hover:bg-primary' : 'text-foreground'}"
              onclick={() => pickYear(y)}
            >
              {y}
            </button>
          {/each}
        </div>
      {/if}
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
