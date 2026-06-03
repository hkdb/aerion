<script lang="ts">
  // TimelineView — shared N-column hour-grid used by WeekView (N=7) and
  // DayView (N=1). Renders:
  //   - Day header row (sticky)         : weekday + day number, "today" circle
  //   - All-day band (sticky)           : all-day events + multi-day timed events
  //   - Scrollable hour body (24 rows)  : timed single-day events with
  //                                       absolute positioning + lane-packed
  //                                       overlap collision rendering
  //   - Now-line                         : 1px destructive-colored line at the
  //                                       current minute in today's column,
  //                                       refreshed every 60s
  //
  // Click an event → calendarView.selectEvent(instance.id). Click empty
  // timeslot is a no-op (composer is Phase 3).

  import { onMount, onDestroy } from 'svelte'
  import { _ } from 'svelte-i18n'
  import EventCard from '$extensions/calendar/frontend/components/EventCard.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    /** Local-time dates to render, one per column. Length 1 for DayView, 7 for WeekView. */
    dates: Date[]
  }

  const { dates }: Props = $props()

  const HOUR_PX = 48
  const DAY_PX = HOUR_PX * 24

  const hourLabels = $derived.by(() => {
    const fmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', hour12: false })
    const out: string[] = []
    const d = new Date()
    for (let h = 0; h < 24; h++) {
      d.setHours(h, 0, 0, 0)
      out.push(fmt.format(d))
    }
    return out
  })

  const weekdayShort = $derived([
    $_('calendar.month.weekdayShort.sun'),
    $_('calendar.month.weekdayShort.mon'),
    $_('calendar.month.weekdayShort.tue'),
    $_('calendar.month.weekdayShort.wed'),
    $_('calendar.month.weekdayShort.thu'),
    $_('calendar.month.weekdayShort.fri'),
    $_('calendar.month.weekdayShort.sat'),
  ])

  const todayMs = $derived.by(() => {
    const t = new Date()
    t.setHours(0, 0, 0, 0)
    return t.getTime()
  })

  function isSameDay(a: Date, b: Date): boolean {
    return a.getFullYear() === b.getFullYear()
      && a.getMonth() === b.getMonth()
      && a.getDate() === b.getDate()
  }

  // --- Categorise events into all-day-band vs hour-grid -----------------------

  type TimedBlock = {
    instance: backend.EventInstance
    color: string
    topPct: number      // 0..100
    heightPct: number   // 0..100
    leftPct: number     // 0..100 (within day column)
    widthPct: number    // 0..100
  }

  type BandBlock = {
    instance: backend.EventInstance
    color: string
    startColIdx: number   // first visible day
    endColIdx: number     // last visible day (inclusive)
    laneIdx: number       // vertical row in band
    continuesLeft: boolean
    continuesRight: boolean
  }

  /**
   * Multi-day timed events go in the all-day band (with continuation arrows
   * at week boundaries). Single-day timed events go in the hour grid.
   * All-day events always go in the band.
   */
  function isBandEvent(inst: backend.EventInstance): boolean {
    if (inst.isAllDay) return true
    const span = inst.instanceEndUnix - inst.instanceStartUnix
    return span > 86400  // > 24h → multi-day
  }

  // Band events laid out across `dates`, packed into vertical lanes.
  const bandBlocks = $derived.by<BandBlock[]>(() => {
    if (dates.length === 0) return []
    const dayStartsUnix = dates.map(d => Math.floor(d.getTime() / 1000))
    const lastDayEndUnix = dayStartsUnix[dayStartsUnix.length - 1] + 86400

    // Collect overlapping band events for the visible window.
    const candidates: BandBlock[] = []
    for (const inst of events.instances) {
      if (!isBandEvent(inst)) continue
      if (inst.instanceEndUnix <= dayStartsUnix[0]) continue
      if (inst.instanceStartUnix >= lastDayEndUnix) continue

      let startColIdx = 0
      let endColIdx = dates.length - 1
      let continuesLeft = false
      let continuesRight = false

      // Find first visible day this event overlaps.
      for (let i = 0; i < dates.length; i++) {
        const dayEnd = dayStartsUnix[i] + 86400
        if (inst.instanceStartUnix < dayEnd) {
          startColIdx = i
          continuesLeft = inst.instanceStartUnix < dayStartsUnix[0]
          break
        }
      }
      // Find last visible day this event overlaps.
      for (let i = dates.length - 1; i >= 0; i--) {
        if (inst.instanceEndUnix > dayStartsUnix[i]) {
          endColIdx = i
          continuesRight = inst.instanceEndUnix > lastDayEndUnix
          break
        }
      }

      candidates.push({
        instance: inst,
        color: calendarSources.colorOf(inst.calendarId),
        startColIdx,
        endColIdx,
        laneIdx: 0,
        continuesLeft,
        continuesRight,
      })
    }

    // Sort by start col then by start time for stable lane assignment.
    candidates.sort((a, b) => {
      if (a.startColIdx !== b.startColIdx) return a.startColIdx - b.startColIdx
      return a.instance.instanceStartUnix - b.instance.instanceStartUnix
    })

    // Greedy lane assignment: per lane, track the rightmost-occupied col.
    const laneRightmostCol: number[] = []
    for (const block of candidates) {
      let assigned = -1
      for (let lane = 0; lane < laneRightmostCol.length; lane++) {
        if (laneRightmostCol[lane] < block.startColIdx) {
          assigned = lane
          break
        }
      }
      if (assigned < 0) {
        assigned = laneRightmostCol.length
        laneRightmostCol.push(-1)
      }
      block.laneIdx = assigned
      laneRightmostCol[assigned] = block.endColIdx
    }
    return candidates
  })

  const bandLaneCount = $derived(bandBlocks.length === 0
    ? 0
    : Math.max(...bandBlocks.map(b => b.laneIdx)) + 1)

  // Per-column timed-event blocks (hour grid), with lane-packed collision.
  function timedBlocksForDay(date: Date): TimedBlock[] {
    const dayStartUnix = Math.floor(date.getTime() / 1000)
    const dayEndUnix = dayStartUnix + 86400

    const list: backend.EventInstance[] = []
    for (const inst of events.instances) {
      if (isBandEvent(inst)) continue
      if (inst.instanceEndUnix <= dayStartUnix) continue
      if (inst.instanceStartUnix >= dayEndUnix) continue
      list.push(inst)
    }
    if (list.length === 0) return []

    // Sort by start (ties: longer first → fatter events get earlier lanes).
    list.sort((a, b) => {
      if (a.instanceStartUnix !== b.instanceStartUnix) {
        return a.instanceStartUnix - b.instanceStartUnix
      }
      return b.instanceEndUnix - a.instanceEndUnix
    })

    // Greedy lane assignment by overlap.
    type LanedEvent = { inst: backend.EventInstance; lane: number }
    const laneEndUnix: number[] = []
    const laned: LanedEvent[] = []
    for (const inst of list) {
      let assigned = -1
      for (let lane = 0; lane < laneEndUnix.length; lane++) {
        if (laneEndUnix[lane] <= inst.instanceStartUnix) {
          assigned = lane
          break
        }
      }
      if (assigned < 0) {
        assigned = laneEndUnix.length
        laneEndUnix.push(0)
      }
      laned.push({ inst, lane: assigned })
      laneEndUnix[assigned] = Math.max(laneEndUnix[assigned], inst.instanceEndUnix)
    }

    // Compute laneCount per overlap-group (transitively-overlapping events).
    // Sweep through laned (which is start-sorted): when current event starts
    // after the running-max-end-of-group, close the group.
    const groupIdx: number[] = new Array(laned.length).fill(0)
    let currentGroup = 0
    let groupMaxEnd = 0
    for (let i = 0; i < laned.length; i++) {
      if (i === 0 || laned[i].inst.instanceStartUnix >= groupMaxEnd) {
        currentGroup = i === 0 ? 0 : currentGroup + 1
        groupMaxEnd = laned[i].inst.instanceEndUnix
      }
      groupIdx[i] = currentGroup
      if (laned[i].inst.instanceEndUnix > groupMaxEnd) {
        groupMaxEnd = laned[i].inst.instanceEndUnix
      }
    }
    const groupLaneCount: Record<number, number> = {}
    for (let i = 0; i < laned.length; i++) {
      const g = groupIdx[i]
      groupLaneCount[g] = Math.max(groupLaneCount[g] ?? 0, laned[i].lane + 1)
    }

    // Build TimedBlock array with %-based positioning.
    const out: TimedBlock[] = []
    for (let i = 0; i < laned.length; i++) {
      const { inst, lane } = laned[i]
      const startSec = Math.max(inst.instanceStartUnix, dayStartUnix)
      const endSec = Math.min(inst.instanceEndUnix, dayEndUnix)
      const startMin = (startSec - dayStartUnix) / 60
      const endMin = (endSec - dayStartUnix) / 60
      const lanes = groupLaneCount[groupIdx[i]]
      out.push({
        instance: inst,
        color: calendarSources.colorOf(inst.calendarId),
        topPct: (startMin / 1440) * 100,
        heightPct: Math.max(((endMin - startMin) / 1440) * 100, 1.2),
        leftPct: (lane / lanes) * 100,
        widthPct: (1 / lanes) * 100,
      })
    }
    return out
  }

  const timedByDay = $derived(dates.map(d => timedBlocksForDay(d)))

  // --- Now-line ---------------------------------------------------------------

  let nowDate = $state(new Date())
  let nowTimer: ReturnType<typeof setInterval> | null = null

  onMount(() => {
    nowTimer = setInterval(() => { nowDate = new Date() }, 60_000)
  })
  onDestroy(() => {
    if (nowTimer !== null) clearInterval(nowTimer)
  })

  const nowMinutes = $derived(nowDate.getHours() * 60 + nowDate.getMinutes())
  const nowTopPct = $derived((nowMinutes / 1440) * 100)
  const todayColIdx = $derived.by(() => {
    for (let i = 0; i < dates.length; i++) {
      if (isSameDay(dates[i], nowDate)) return i
    }
    return -1
  })

  // --- Initial scroll ---------------------------------------------------------

  let scrollRef = $state<HTMLDivElement | null>(null)

  onMount(() => {
    if (!scrollRef) return
    const now = new Date()
    let targetPx = HOUR_PX * 8 // default 8 AM
    if (todayColIdx >= 0 && now.getHours() >= 6) {
      const m = now.getHours() * 60 + now.getMinutes()
      targetPx = Math.max(0, (m * HOUR_PX / 60) - HOUR_PX)
    }
    scrollRef.scrollTop = targetPx
  })

  // --- Click handlers ---------------------------------------------------------

  function onEventClick(inst: backend.EventInstance) {
    calendarView.selectEvent(inst.id)
  }
</script>

<div class="flex-1 flex flex-col min-h-0 bg-background">
  <!-- Day header row: weekday + day number per column -->
  <div
    class="grid border-b border-border bg-muted/20 shrink-0"
    style:grid-template-columns="60px repeat({dates.length}, 1fr)"
  >
    <div></div>
    {#each dates as date, i (i)}
      {@const isToday = isSameDay(date, new Date())}
      <div class="px-2 py-1 text-center border-l border-border">
        <div class="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
          {weekdayShort[date.getDay()]}
        </div>
        <div class="inline-flex items-center justify-center w-6 h-6 mt-0.5 text-sm
                    {isToday ? 'rounded-full bg-primary text-primary-foreground' : 'text-foreground'}">
          {date.getDate()}
        </div>
      </div>
    {/each}
  </div>

  <!-- All-day band: row per lane, columns aligned with day columns -->
  {#if bandLaneCount > 0}
    <div
      class="grid border-b border-border bg-background shrink-0 py-1 gap-y-0.5"
      style:grid-template-columns="60px repeat({dates.length}, 1fr)"
      style:grid-template-rows={`repeat(${bandLaneCount}, minmax(20px, auto))`}
    >
      <!-- Left gutter label -->
      <div
        class="text-[10px] text-muted-foreground text-right pr-1 self-center"
        style:grid-row={`1 / span ${bandLaneCount}`}
      >
        {$_('calendar.timeline.allDay')}
      </div>
      {#each bandBlocks as block (block.instance.id)}
        <div
          class="px-0.5"
          style:grid-row={`${block.laneIdx + 1}`}
          style:grid-column={`${block.startColIdx + 2} / ${block.endColIdx + 3}`}
        >
          <EventCard
            instance={block.instance}
            color={block.color}
            continuesLeft={block.continuesLeft}
            continuesRight={block.continuesRight}
            onclick={() => onEventClick(block.instance)}
          />
        </div>
      {/each}
    </div>
  {/if}

  <!-- Scrollable hour body -->
  <div bind:this={scrollRef} class="flex-1 overflow-y-auto">
    <div
      class="grid relative"
      style:grid-template-columns="60px repeat({dates.length}, 1fr)"
      style:height={`${DAY_PX}px`}
    >
      <!-- Hour label column -->
      <div class="border-r border-border">
        {#each hourLabels as label, h (h)}
          <div
            class="flex items-start justify-end pr-1 text-[10px] text-muted-foreground border-b border-border/40"
            style:height={`${HOUR_PX}px`}
          >
            <span class="-translate-y-1/2">{label}</span>
          </div>
        {/each}
      </div>

      <!-- Day columns -->
      {#each dates as date, colIdx (colIdx)}
        <div class="relative border-l border-border">
          <!-- Hour gridlines -->
          {#each hourLabels as _label, h (h)}
            <div
              class="border-b border-border/40"
              style:height={`${HOUR_PX}px`}
            ></div>
          {/each}

          <!-- Timed event blocks (absolute, %-based vertical positioning) -->
          {#each timedByDay[colIdx] as block (block.instance.id)}
            <button
              type="button"
              class="absolute rounded text-[11px] text-foreground text-left
                     px-1 py-0.5 overflow-hidden cursor-pointer
                     hover:brightness-110 transition-[filter]"
              style:top={`${block.topPct}%`}
              style:height={`${block.heightPct}%`}
              style:left={`calc(${block.leftPct}% + 2px)`}
              style:width={`calc(${block.widthPct}% - 4px)`}
              style:background-color={`color-mix(in srgb, ${block.color} 25%, transparent)`}
              style:border-left={`3px solid ${block.color}`}
              title={block.instance.summary}
              onclick={() => onEventClick(block.instance)}
            >
              <div class="font-mono text-[10px] text-muted-foreground leading-tight">
                {new Date(block.instance.instanceStartUnix * 1000).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })}
              </div>
              <div class="truncate leading-tight">
                {block.instance.summary || '(no title)'}
              </div>
            </button>
          {/each}

          <!-- Now-line, only in today's column -->
          {#if colIdx === todayColIdx}
            <div
              class="absolute left-0 right-0 z-10 pointer-events-none"
              style:top={`${nowTopPct}%`}
              aria-label={$_('calendar.timeline.nowLabel')}
            >
              <div class="h-px bg-destructive"></div>
              <div class="absolute -left-1 -top-1 w-2 h-2 rounded-full bg-destructive"></div>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  </div>
</div>
