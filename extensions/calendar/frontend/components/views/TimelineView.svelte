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
  // Click an event → calendarView.selectEvent(instance.id). Click an empty
  // spot in a day column → 15-min slot highlights and the event composer
  // opens with `defaultStart` snapped to that slot.

  import { onMount, onDestroy } from 'svelte'
  import { _ } from 'svelte-i18n'
  import EventCard from '$extensions/calendar/frontend/components/EventCard.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'
  import { toTzDate, fromTzDate } from '$extensions/calendar/frontend/lib/tzMath'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { Calendar_UpdateEvent } from '$wailsjs/go/app/App.js'
  import SendInvitationsDialog from '$extensions/calendar/frontend/components/SendInvitationsDialog.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    /** Local-time dates to render, one per column. Length 1 for DayView, 7 for WeekView. */
    dates: Date[]
  }

  const { dates }: Props = $props()

  const HOUR_PX = 48
  const DAY_PX = HOUR_PX * 24

  // Hour labels: pure 00..23 in the user's chosen tz. Use a UTC reference
  // date and format in UTC so the strings stay DST-safe — we only want the
  // hour-of-day labels for the column gutter, not real timed values.
  const hourLabels = $derived.by(() => {
    const fmt = new Intl.DateTimeFormat(undefined, {
      hour: '2-digit', hour12: false, timeZone: 'UTC',
    })
    const out: string[] = []
    for (let h = 0; h < 24; h++) {
      out.push(fmt.format(new Date(Date.UTC(2020, 0, 1, h, 0, 0))))
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

  function isSameDay(a: Date, b: Date): boolean {
    // Tz-aware: same calendar-day in the user's chosen display tz.
    const za = toTzDate(a)
    const zb = toTzDate(b)
    return za.getFullYear() === zb.getFullYear()
      && za.getMonth() === zb.getMonth()
      && za.getDate() === zb.getDate()
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

  const nowMinutes = $derived.by(() => {
    const z = toTzDate(nowDate)
    return z.getHours() * 60 + z.getMinutes()
  })
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
    const zNow = toTzDate(new Date())
    let targetPx = HOUR_PX * 8 // default 8 AM
    if (todayColIdx >= 0 && zNow.getHours() >= 6) {
      const m = zNow.getHours() * 60 + zNow.getMinutes()
      targetPx = Math.max(0, (m * HOUR_PX / 60) - HOUR_PX)
    }
    scrollRef.scrollTop = targetPx
  })

  // --- Click handlers ---------------------------------------------------------

  function onEventClick(inst: backend.EventInstance) {
    calendarView.selectEvent(inst.id)
  }

  // --- Click empty timeslot → open composer at 15-min snapped slot ------------
  //
  // The composer dialog itself is now mounted centrally in CalendarPane.
  // We keep the per-cell highlight state local because it's a TimelineView
  // visual artifact (the colored band on the clicked slot); it tears down
  // via a $effect that watches the global composer's open state.

  let highlightedSlot = $state<{ colIdx: number; startMinute: number } | null>(null)

  function onColumnClick(colIdx: number, e: MouseEvent) {
    // Event blocks are absolute-positioned <button> children of the column.
    // Skip if the click hit one — its own onclick handles event selection.
    const target = e.target as HTMLElement
    if (target.closest('button')) return

    const col = e.currentTarget as HTMLElement
    const rect = col.getBoundingClientRect()
    const localY = e.clientY - rect.top
    const minuteOfDay = (localY / DAY_PX) * 1440
    const snapped = Math.max(0, Math.min(1425, Math.floor(minuteOfDay / 15) * 15))

    // dates[colIdx] is midnight-in-tz as a real UTC Date. Round-trip through
    // toTzDate / fromTzDate so DST transitions stay correct.
    const wall = toTzDate(dates[colIdx])
    wall.setHours(Math.floor(snapped / 60), snapped % 60, 0, 0)
    const startUtc = fromTzDate(wall)

    highlightedSlot = { colIdx, startMinute: snapped }
    calendarView.requestNewEvent({ defaultStart: startUtc })
  }

  function clearHighlight() {
    highlightedSlot = null
  }

  // Composer closed (save or cancel) → drop the highlight. Initial pass
  // with composerOpen=false is a harmless no-op clear.
  $effect(() => {
    if (!calendarView.composerOpen) clearHighlight()
  })

  async function refreshEventsAsync() {
    await events.fetchRange(
      calendarSources.visibleCalendarIDs,
      calendarView.visibleRange.fromUnix,
      calendarView.visibleRange.toUnix,
    )
  }

  // --- Drag-to-move / drag-to-resize ------------------------------------------
  //
  // Three grip zones per event block: 6px top (resize-start), 6px bottom
  // (resize-end), remaining body (move). Recurring events skip the gesture
  // entirely — the dialog path (RecurrenceScopeDialog → composer) handles
  // them with proper scope semantics.
  //
  // Click vs drag disambiguation via a 4-pixel-or-cross-column movement
  // threshold + a wasDragged flag that suppresses the synthetic click event
  // browsers fire after pointerup. Below the threshold, the existing
  // onclick path runs and opens EventDetail as before.

  type DragMode = 'move' | 'resize-start' | 'resize-end'

  type DragState = {
    instanceId: string
    eventId: string
    mode: DragMode
    originColIdx: number
    originStartUnix: number
    originEndUnix: number
    startClientX: number
    startClientY: number
    columnWidth: number
    currentColIdx: number
    currentDeltaMinutes: number
    movedPastThreshold: boolean
    // Snapshot of master fields we need to round-trip through
    // Calendar_UpdateEvent without losing summary / description / location.
    masterSummary: string
    masterDescription: string
    masterLocation: string
    masterCalendarID: string
    masterIsAllDay: boolean
    masterTZName: string
    // Attendees + organizer snapshot so drag-drop preserves them across
    // the save (without these, the backend's updateAllAndPush wipes the
    // attendee list because it always overwrites ev.Attendees from in.Attendees).
    masterAttendees: backend.AttendeeInput[]
    masterOrganizer: backend.OrganizerInput | null
    // sourceKind for the SendInvitationsDialog provider note.
    masterSourceKind: 'google' | 'microsoft' | 'caldav-server' | 'caldav-none' | 'local' | ''
  }

  let dragState = $state<DragState | null>(null)

  // Pending dragState held while SendInvitationsDialog is open — once the
  // user picks Send / Don't send, we fire performDragSave with the
  // chosen sendUpdates value. Cancel just clears dragState (visual snap
  // back to original position via the block.instance fallback).
  let pendingDragSave = $state<{ ds: DragState; newStartUnix: number; newEndUnix: number } | null>(null)
  let sendInvitationsOpen = $state(false)

  // sourceKind for the dragged event's calendar — used by the dialog's
  // provider note (matches EventComposerDialog's derivation logic).
  function sourceKindOf(calendarId: string): DragState['masterSourceKind'] {
    for (const src of calendarSources.sources) {
      const cals = calendarSources.calendarsBySource[src.id] || []
      for (const cal of cals) {
        if (cal.id !== calendarId) continue
        switch (src.type) {
          case 'google':    return 'google'
          case 'microsoft': return 'microsoft'
          case 'local':     return 'local'
          case 'caldav':    return src.itipMode === 'none' ? 'caldav-none' : 'caldav-server'
        }
        return ''
      }
    }
    return ''
  }
  let wasDragged = $state(false)
  let saving = $state(false)

  const RESIZE_GRIP_PX = 6
  const DRAG_THRESHOLD_PX = 4

  function detectMode(relY: number, height: number): DragMode {
    if (relY < RESIZE_GRIP_PX) return 'resize-start'
    if (relY > height - RESIZE_GRIP_PX) return 'resize-end'
    return 'move'
  }

  function onBlockPointerDown(block: TimedBlock, colIdx: number, e: PointerEvent) {
    // Recurring events use the dialog path (RecurrenceScopeDialog →
    // composer). Drag would need to fire a modal mid-gesture, which is
    // a bad UX. Let the click bubble naturally.
    if (block.instance.rruleText) return

    // Only primary button (left mouse / single finger).
    if (e.button !== 0) return

    const btn = e.currentTarget as HTMLButtonElement
    const btnRect = btn.getBoundingClientRect()
    const relY = e.clientY - btnRect.top
    const mode = detectMode(relY, btnRect.height)

    // Column rect for cross-column drift calculations.
    const colEl = btn.closest('[data-day-col]') as HTMLElement | null
    if (!colEl) return
    const colRect = colEl.getBoundingClientRect()

    btn.setPointerCapture(e.pointerId)
    e.preventDefault()

    dragState = {
      instanceId: block.instance.id,
      eventId: block.instance.id,
      mode,
      originColIdx: colIdx,
      originStartUnix: block.instance.instanceStartUnix,
      originEndUnix: block.instance.instanceEndUnix,
      startClientX: e.clientX,
      startClientY: e.clientY,
      columnWidth: colRect.width,
      currentColIdx: colIdx,
      currentDeltaMinutes: 0,
      movedPastThreshold: false,
      masterSummary: block.instance.summary ?? '',
      masterDescription: block.instance.description ?? '',
      masterLocation: block.instance.location ?? '',
      masterCalendarID: block.instance.calendarId,
      masterIsAllDay: !!block.instance.isAllDay,
      masterTZName: block.instance.tzName ?? '',
      // Snapshot attendees + organizer so the save preserves them. Drop
      // server-side fields like scheduleStatus when remapping Attendee →
      // AttendeeInput (those are output-only and would break Wails
      // serialization).
      masterAttendees: (block.instance.attendees ?? []).map(a => ({
        email: a.email,
        cn: a.cn ?? '',
        partStat: a.partStat ?? 'NEEDS-ACTION',
        role: a.role ?? 'REQ-PARTICIPANT',
        rsvp: a.rsvp ?? false,
        cuType: a.cuType ?? '',
        delegate: a.delegate ?? '',
      })) as unknown as backend.AttendeeInput[],
      masterOrganizer: block.instance.organizer
        ? { email: block.instance.organizer.email, cn: block.instance.organizer.cn ?? '' }
        : null,
      masterSourceKind: sourceKindOf(block.instance.calendarId),
    }
  }

  function onWindowPointerMove(e: PointerEvent) {
    const ds = dragState
    if (!ds) return
    // Once the save has been claimed (pointerup → persistDrag in flight),
    // the snapped position is locked in. Ignore further pointermove events
    // so the block stops tracking the cursor while we wait for the refetch.
    if (saving) return

    const deltaY = e.clientY - ds.startClientY
    const rawMin = (deltaY / DAY_PX) * 1440
    const snapped = Math.round(rawMin / 15) * 15

    // Column drift only for move mode + WeekView (multi-column).
    let newColIdx = ds.originColIdx
    if (ds.mode === 'move' && dates.length > 1) {
      const colEls = document.querySelectorAll<HTMLElement>('[data-day-col]')
      for (let i = 0; i < colEls.length; i++) {
        const r = colEls[i].getBoundingClientRect()
        if (e.clientX >= r.left && e.clientX <= r.right) {
          const idx = Number(colEls[i].dataset.dayCol)
          if (!Number.isNaN(idx)) newColIdx = idx
          break
        }
      }
    }

    const moved =
      Math.abs(deltaY) > DRAG_THRESHOLD_PX ||
      newColIdx !== ds.originColIdx

    dragState = {
      ...ds,
      currentColIdx: newColIdx,
      currentDeltaMinutes: snapped,
      movedPastThreshold: ds.movedPastThreshold || moved,
    }
  }

  function onWindowPointerUp(_e: PointerEvent) {
    const ds = dragState
    if (!ds) return

    // Click without drag — clear and let the native click → onclick path fire.
    if (!ds.movedPastThreshold) {
      dragState = null
      return
    }

    // Drag occurred. Claim the save synchronously via the `saving` flag so a
    // duplicate pointerup or pointercancel can't re-fire persistDrag while
    // we're mid-save. dragState stays set until the save resolves — the
    // block continues to render at its snapped target position throughout.
    if (saving) return
    saving = true
    wasDragged = true
    void persistDrag(ds)
  }

  async function persistDrag(ds: DragState) {
    let newStartUnix = ds.originStartUnix
    let newEndUnix = ds.originEndUnix

    switch (ds.mode) {
      case 'move': {
        const minuteShift = ds.currentDeltaMinutes * 60
        newStartUnix += minuteShift
        newEndUnix += minuteShift
        const dayDelta = ds.currentColIdx - ds.originColIdx
        if (dayDelta !== 0) {
          newStartUnix += dayDelta * 86400
          newEndUnix += dayDelta * 86400
        }
        break
      }
      case 'resize-start': {
        newStartUnix += ds.currentDeltaMinutes * 60
        if (newStartUnix > newEndUnix - 15 * 60) {
          newStartUnix = newEndUnix - 15 * 60
        }
        break
      }
      case 'resize-end': {
        newEndUnix += ds.currentDeltaMinutes * 60
        if (newEndUnix < newStartUnix + 15 * 60) {
          newEndUnix = newStartUnix + 15 * 60
        }
        break
      }
    }

    // If the event has attendees, intercept with SendInvitationsDialog
    // (same UX as Edit-via-composer). Cancel reverts the visual snap.
    // Otherwise save directly.
    if (ds.masterAttendees.length > 0) {
      pendingDragSave = { ds, newStartUnix, newEndUnix }
      sendInvitationsOpen = true
      return
    }
    await performDragSave(ds, newStartUnix, newEndUnix, 'all')
  }

  async function performDragSave(
    ds: DragState,
    newStartUnix: number,
    newEndUnix: number,
    sendUpdates: string,
  ) {
    try {
      await Calendar_UpdateEvent({
        eventId: ds.eventId,
        calendarId: ds.masterCalendarID,
        summary: ds.masterSummary,
        description: ds.masterDescription,
        location: ds.masterLocation,
        dtstartUnix: newStartUnix,
        dtendUnix: newEndUnix,
        isAllDay: ds.masterIsAllDay,
        // Preserve the event's anchor tz across a drag — moving an LA-anchored
        // event must not silently re-label it as UTC just because the drag
        // touchpoint happens to live in the user's effective-tz grid.
        tz: ds.masterTZName || undefined,
        // Preserve attendees + organizer across the drag. Without these,
        // the backend's updateAllAndPush would wipe the attendee list
        // (in.Attendees absent → in.Attendees is nil → backend overwrites
        // ev.Attendees with nil).
        attendees: ds.masterAttendees,
        organizer: ds.masterOrganizer ?? undefined,
        sendUpdates: ds.masterAttendees.length > 0 ? sendUpdates : undefined,
      } as unknown as backend.EventUpdateInput, 'all')
      // Wait for the events store to reflect the new state BEFORE clearing
      // dragState — otherwise the block would briefly snap back to its old
      // position (from the stale block.instance) before the refetch lands.
      await refreshEventsAsync()
    } catch (err) {
      // On failure: dragState clears (in finally) → block uses
      // block.instance position which is the ORIGINAL (since no commit
      // happened) → natural snap-back to where the user grabbed it.
      toasts.error($_('calendar.drag.errorSave', { values: { message: String(err) } }))
    } finally {
      saving = false
      dragState = null
      pendingDragSave = null
    }
  }

  async function onSendInvitationsConfirm(sendUpdates: string) {
    const pending = pendingDragSave
    if (!pending) return
    await performDragSave(pending.ds, pending.newStartUnix, pending.newEndUnix, sendUpdates)
  }

  function onSendInvitationsCancel() {
    // User backed out of the move — revert the visual snap. Clearing
    // dragState makes the block render at block.instance's original
    // position (no commit happened, so the master event still has the
    // old times).
    saving = false
    dragState = null
    pendingDragSave = null
  }

  // Hover-cursor handler: switches between grab (body) and ns-resize
  // (top/bottom 6px zones) so the user can see which gesture a click+drag
  // will produce. Runs only when not actively dragging.
  function onBlockHoverMove(block: TimedBlock, e: PointerEvent) {
    if (dragState) return
    if (block.instance.rruleText) return
    const btn = e.currentTarget as HTMLButtonElement
    const rect = btn.getBoundingClientRect()
    const relY = e.clientY - rect.top
    const mode = detectMode(relY, rect.height)
    const cursor = mode === 'move' ? 'grab' : 'ns-resize'
    if (btn.style.cursor !== cursor) {
      btn.style.cursor = cursor
    }
  }

  function onBlockClick(inst: backend.EventInstance, e: MouseEvent) {
    // Drag-occurred click: synthetic click fires after pointerup; suppress
    // it so we don't accidentally open the detail view after a successful
    // drag. Reset the flag for the next interaction.
    if (wasDragged) {
      e.preventDefault()
      e.stopPropagation()
      wasDragged = false
      return
    }
    onEventClick(inst)
  }

  // Derived visual offsets for the block currently being dragged. Returns
  // null when the block isn't the drag target OR the user hasn't moved
  // past the threshold yet (no visual feedback below the click/drag
  // disambiguation threshold).
  function dragOffsetsFor(block: TimedBlock): { top: number; height: number; xPx: number } | null {
    const ds = dragState
    if (!ds) return null
    if (ds.instanceId !== block.instance.id) return null
    if (!ds.movedPastThreshold) return null

    const minutePx = HOUR_PX / 60
    const deltaPx = ds.currentDeltaMinutes * minutePx

    // Base top/height from the block's current pct (relative to DAY_PX).
    const baseTopPx = (block.topPct / 100) * DAY_PX
    const baseHeightPx = (block.heightPct / 100) * DAY_PX

    let topPx = baseTopPx
    let heightPx = baseHeightPx
    let xPx = 0

    switch (ds.mode) {
      case 'move':
        topPx = baseTopPx + deltaPx
        xPx = (ds.currentColIdx - ds.originColIdx) * ds.columnWidth
        break
      case 'resize-start':
        topPx = baseTopPx + deltaPx
        heightPx = baseHeightPx - deltaPx
        if (heightPx < 14) {
          heightPx = 14
          topPx = baseTopPx + baseHeightPx - 14
        }
        break
      case 'resize-end':
        heightPx = baseHeightPx + deltaPx
        if (heightPx < 14) heightPx = 14
        break
    }

    return { top: topPx, height: heightPx, xPx }
  }
</script>

<svelte:window
  onpointermove={onWindowPointerMove}
  onpointerup={onWindowPointerUp}
  onpointercancel={onWindowPointerUp}
/>

<div class="flex-1 flex flex-col min-h-0 bg-background">
  <!-- Day header row: weekday + day number per column -->
  <div
    class="grid border-b border-border bg-muted/20 shrink-0"
    style:grid-template-columns="60px repeat({dates.length}, 1fr)"
  >
    <div></div>
    {#each dates as date, i (i)}
      {@const isToday = isSameDay(date, new Date())}
      {@const zd = toTzDate(date)}
      <div class="px-2 py-1 text-center border-l border-border">
        <div class="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
          {weekdayShort[zd.getDay()]}
        </div>
        <div class="inline-flex items-center justify-center w-6 h-6 mt-0.5 text-sm
                    {isToday ? 'rounded-full bg-primary text-primary-foreground' : 'text-foreground'}">
          {zd.getDate()}
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
        <div
          class="relative border-l border-border cursor-pointer"
          data-day-col={colIdx}
          onclick={(e) => onColumnClick(colIdx, e)}
          onkeydown={() => {}}
          role="button"
          tabindex="-1"
        >
          <!-- Hour gridlines -->
          {#each hourLabels as _label, h (h)}
            <div
              class="border-b border-border/40"
              style:height={`${HOUR_PX}px`}
            ></div>
          {/each}

          <!-- Click-to-create highlight: 15-min band at clicked slot -->
          {#if highlightedSlot?.colIdx === colIdx}
            <div
              class="pointer-events-none absolute left-0 right-0 bg-primary/15 border border-primary/40 rounded-sm"
              style:top={`${(highlightedSlot.startMinute / 1440) * 100}%`}
              style:height={`${(15 / 1440) * 100}%`}
            ></div>
          {/if}

          <!-- Timed event blocks (absolute, %-based vertical positioning).
               During drag, the targeted block switches to px-based top/height
               + an X translate so it can cross columns in WeekView. -->
          {#each timedByDay[colIdx] as block (block.instance.id)}
            {@const dragOff = dragOffsetsFor(block)}
            {@const isRecurring = !!block.instance.rruleText}
            <button
              type="button"
              class="absolute rounded text-[11px] text-foreground text-left
                     px-1 py-0.5 overflow-hidden
                     hover:brightness-110 transition-[filter]"
              class:cursor-pointer={isRecurring}
              class:cursor-grab={!isRecurring && !dragOff}
              class:cursor-grabbing={!isRecurring && dragOff && dragState?.mode === 'move'}
              class:cursor-ns-resize={!isRecurring && dragOff && (dragState?.mode === 'resize-start' || dragState?.mode === 'resize-end')}
              style:top={dragOff ? `${dragOff.top}px` : `${block.topPct}%`}
              style:height={dragOff ? `${dragOff.height}px` : `${block.heightPct}%`}
              style:left={`calc(${block.leftPct}% + 2px)`}
              style:width={`calc(${block.widthPct}% - 4px)`}
              style:background-color={`color-mix(in srgb, ${block.color} 25%, transparent)`}
              style:border-left={`3px solid ${block.color}`}
              style:transform={dragOff ? `translateX(${dragOff.xPx}px)` : undefined}
              style:opacity={dragOff ? 0.85 : undefined}
              style:z-index={dragOff ? 50 : undefined}
              style:touch-action={isRecurring ? undefined : 'none'}
              title={isRecurring
                ? `${block.instance.summary} — ${$_('calendar.drag.recurringHint')}`
                : block.instance.summary}
              onpointerdown={(e) => onBlockPointerDown(block, colIdx, e)}
              onpointermove={(e) => onBlockHoverMove(block, e)}
              onclick={(e) => onBlockClick(block.instance, e)}
            >
              <div class="font-mono text-[10px] text-muted-foreground leading-tight">
                {new Intl.DateTimeFormat(undefined, {
                  hour: '2-digit', minute: '2-digit', hour12: false,
                  timeZone: calendarSettings.effectiveTimezone,
                }).format(new Date(block.instance.instanceStartUnix * 1000))}
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

<SendInvitationsDialog
  bind:open={sendInvitationsOpen}
  attendeeCount={pendingDragSave?.ds.masterAttendees.length ?? 0}
  sourceKind={pendingDragSave?.ds.masterSourceKind ?? ''}
  onConfirm={onSendInvitationsConfirm}
  onCancel={onSendInvitationsCancel}
/>
