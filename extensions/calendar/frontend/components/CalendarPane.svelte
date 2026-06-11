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
  import { _ } from 'svelte-i18n'
  import PaneLayout from '$lib/components/kit/PaneLayout.svelte'
  import DetailOverlay from '$lib/components/kit/DetailOverlay.svelte'
  import CalendarSidebar from './CalendarSidebar.svelte'
  import ViewSwitcher from './ViewSwitcher.svelte'
  import MonthView from './views/MonthView.svelte'
  import WeekView from './views/WeekView.svelte'
  import DayView from './views/DayView.svelte'
  import AgendaView from './views/AgendaView.svelte'
  import EventDetail from './EventDetail.svelte'
  import EventComposerDialog from './EventComposerDialog.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarView } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { events } from '$extensions/calendar/frontend/stores/events.svelte'
  import { registerExtensionShortcut } from '$lib/stores/extensionShortcuts.svelte'
  import { openExtensionSettings } from '$lib/stores/extensionRegistry.svelte'
  import { consumePendingDeepLink } from '$lib/stores/extensionDeepLink.svelte'
  import { KEY } from '$extensions/calendar/frontend/keyboard/shortcuts'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { EventsOn } from '$wailsjs/runtime/runtime.js'

  // Subscribe to `calendar:sync-complete` once, on mount.
  //
  // Also drains any pending deep link the host stashed before mounting
  // us (e.g., the user clicked a VALARM notification while Calendar wasn't
  // the active rail tab — App.svelte set the pending link + switched tab,
  // and we open the matching event here).
  onMount(() => {
    void calendarSources.load()
    events.initSubscription(() => ({
      calendarIDs: calendarSources.visibleCalendarIDs,
      fromUnix: calendarView.visibleRange.fromUnix,
      toUnix: calendarView.visibleRange.toUnix,
    }))
    // Phase 2 Chunk 5: the host syncer's pending-write queue drains on
    // network-online / wake / sync tick. When it hits a 412 conflict, it
    // drops the pending row and publishes `calendar:write-conflict` —
    // surface a toast so the user knows to re-edit.
    EventsOn('calendar:write-conflict', () => {
      toasts.error($_('calendar.write.conflict'))
    })
    EventsOn('calendar:write-queued', () => {
      toasts.info($_('calendar.write.queued'))
    })

    // Initial drain: a notification click that switched the rail to us
    // stashed its path in the buffer before our mount. Handle it now.
    handleCalendarDeepLink(consumePendingDeepLink('calendar'))

    // Repeat-click subscription: when a calendar notification fires while
    // we're already the active rail tab, setActiveExtension('calendar')
    // is a no-op and onMount won't re-run. Listen for the host's
    // extension-open event here so we still navigate to the new event.
    EventsOn('extension:open', (data: { extensionId: string; path: string }) => {
      if (data.extensionId !== 'calendar') return
      // Drain the buffer so a future remount doesn't replay this path.
      consumePendingDeepLink('calendar')
      handleCalendarDeepLink(data.path)
    })
  })

  function handleCalendarDeepLink(path: string | null | undefined) {
    if (!path) return
    const prefix = '/event/'
    if (!path.startsWith(prefix)) return
    const eventID = path.slice(prefix.length)
    if (eventID !== '') calendarView.selectEvent(eventID)
  }

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
  const unregSyncAll = registerExtensionShortcut('calendar', KEY.CALENDAR_SYNC_ALL, () => {
    void calendarSources.syncAll()
  })
  const unregNewEvent = registerExtensionShortcut('calendar', KEY.CALENDAR_NEW_EVENT, () => {
    calendarView.requestNewEvent()
  })
  const unregFocus = registerExtensionShortcut('calendar', KEY.CALENDAR_FOCUS_TOGGLE, () => {
    calendarView.toggleEventFocus()
  })
  const unregViewMonth = registerExtensionShortcut('calendar', KEY.CALENDAR_VIEW_MONTH, () => {
    calendarView.setViewKind('month')
  })
  const unregViewWeek = registerExtensionShortcut('calendar', KEY.CALENDAR_VIEW_WEEK, () => {
    calendarView.setViewKind('week')
  })
  const unregViewDay = registerExtensionShortcut('calendar', KEY.CALENDAR_VIEW_DAY, () => {
    calendarView.setViewKind('day')
  })
  const unregViewAgenda = registerExtensionShortcut('calendar', KEY.CALENDAR_VIEW_AGENDA, () => {
    calendarView.setViewKind('agenda')
  })
  onDestroy(() => {
    unregToday()
    unregPrev()
    unregNext()
    unregSync()
    unregSyncAll()
    unregNewEvent()
    unregFocus()
    unregViewMonth()
    unregViewWeek()
    unregViewDay()
    unregViewAgenda()
  })

  // Centralized create-mode composer mount. Single source of truth for
  // the "+ Event" button, empty-slot click, and Ctrl/Cmd+N — all three
  // route through calendarView.requestNewEvent(). Save refetches the
  // currently-visible event window so the new event renders without a
  // separate sync-complete cycle.
  function refreshAfterSave() {
    void events.fetchRange(
      calendarSources.visibleCalendarIDs,
      calendarView.visibleRange.fromUnix,
      calendarView.visibleRange.toUnix,
    )
  }

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
  <CalendarSidebar onOpenSettings={openSettings} />
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

<!-- Single create-mode composer mount. All three triggers (toolbar +
     Event button, empty-slot click in TimelineView, Ctrl/Cmd+N) flow
     through calendarView.requestNewEvent(); EventDetail's edit-mode
     mount is independent and stays local to that component. -->
<EventComposerDialog
  bind:open={calendarView.composerOpen}
  mode="create"
  defaultStart={calendarView.composerDefaultStart}
  onSaved={refreshAfterSave}
/>

