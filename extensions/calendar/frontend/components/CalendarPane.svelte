<script lang="ts">
  // CalendarPane — the Calendar extension's root component, mounted by
  // App.svelte when `getActiveExtension() === 'calendar'`. Composes the
  // sidebar + the active view body (Month / Week / Day / Agenda), plus
  // the Add-CalDAV dialog. Phase 1D wires only the Month view; the
  // others render a "coming soon" placeholder until 1F.
  //
  // Lazy data load: `calendarSources.load()` runs onMount. The events
  // store fetches the visible range via a $effect whenever the source
  // visibility set OR the view window changes. The Wails event
  // `calendar:sync-complete` (emitted by the host syncer) triggers a
  // refetch of the current window without changing view state.

  import { onMount, onDestroy } from 'svelte'
  import PaneLayout from '$lib/components/kit/PaneLayout.svelte'
  import DetailOverlay from '$lib/components/kit/DetailOverlay.svelte'
  import CalendarSidebar from './CalendarSidebar.svelte'
  import ViewSwitcher from './ViewSwitcher.svelte'
  import MonthView from './views/MonthView.svelte'
  import WeekView from './views/WeekView.svelte'
  import DayView from './views/DayView.svelte'
  import AgendaView from './views/AgendaView.svelte'
  import EventDetail from './EventDetail.svelte'
  import AddCalDAVSourceDialog from './AddCalDAVSourceDialog.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { registerExtensionShortcut } from '$lib/stores/extensionShortcuts.svelte'
  import { openExtensionSettings } from '$lib/stores/extensionRegistry.svelte'
  import { KEY } from '$extensions/calendar/frontend/keyboard/shortcuts'

  let showAddSource = $state(false)

  // Subscribe to `calendar:sync-complete` once, on mount. The handler
  // refetches whatever window is currently active — closures captured
  // here read the latest store state at fire time.
  onMount(() => {
    void calendarSources.load()
    events.initSubscription(() => ({
      calendarIDs: calendarSources.visibleCalendarIDs,
      fromUnix: calendarView.visibleRange.fromUnix,
      toUnix: calendarView.visibleRange.toUnix,
    }))
  })

  // Auto-refetch events whenever the visible calendar set OR the visible
  // window changes. Uses fetchRange's lastFetchKey dedup so rapid state
  // changes during navigation don't pile up redundant Wails calls.
  $effect(() => {
    const ids = calendarSources.visibleCalendarIDs
    const range = calendarView.visibleRange
    void events.fetchRange(ids, range.fromUnix, range.toUnix)
  })

  // Keyboard shortcuts registered via the extension-shortcut registry. The
  // host's global handler routes these to us only when Calendar is the
  // active rail pane — `t` etc. stay free for mail.
  const unregToday = registerExtensionShortcut('calendar', KEY.CALENDAR_TODAY, () => {
    calendarView.goToday()
  })
  const unregPrev = registerExtensionShortcut('calendar', KEY.CALENDAR_PREV, () => {
    calendarView.goPrev()
  })
  const unregNext = registerExtensionShortcut('calendar', KEY.CALENDAR_NEXT, () => {
    calendarView.goNext()
  })
  const unregSync = registerExtensionShortcut('calendar', KEY.CALENDAR_SYNC, () => {
    void calendarSources.syncAll()
  })
  const unregFocus = registerExtensionShortcut('calendar', KEY.CALENDAR_FOCUS_TOGGLE, () => {
    calendarView.toggleEventFocus()
  })
  onDestroy(() => {
    unregToday()
    unregPrev()
    unregNext()
    unregSync()
    unregFocus()
  })

  function openSettings() {
    openExtensionSettings('calendar')
  }

  // Title shown in the overlay header (responsive back-bar / focused mode).
  // Pulled from the visible-window instance cache so we don't fetch twice;
  // EventDetail does its own Calendar_GetEvent for the full record.
  const overlayTitle = $derived.by(() => {
    const id = calendarView.selectedEventId
    if (id === null) return ''
    for (const inst of events.instances) {
      if (inst.id === id) return inst.summary || ''
    }
    return ''
  })
</script>

<PaneLayout>
  <CalendarSidebar onAddSource={() => { showAddSource = true }} onOpenSettings={openSettings} />
  <div class="flex-1 flex flex-col min-w-0 bg-background">
    <ViewSwitcher />
    {#if calendarView.viewKind === 'month'}<MonthView />{/if}
    {#if calendarView.viewKind === 'week'}<WeekView />{/if}
    {#if calendarView.viewKind === 'day'}<DayView />{/if}
    {#if calendarView.viewKind === 'agenda'}<AgendaView />{/if}
  </div>
</PaneLayout>

<DetailOverlay
  open={calendarView.selectedEventId !== null}
  focused={calendarView.eventFocusMode === 'event'}
  title={overlayTitle}
  onClose={() => calendarView.selectEvent(null)}
  onToggleFocus={() => calendarView.toggleEventFocus()}
>
  {#snippet children()}
    <EventDetail eventId={calendarView.selectedEventId} />
  {/snippet}
</DetailOverlay>

<AddCalDAVSourceDialog bind:open={showAddSource} />
