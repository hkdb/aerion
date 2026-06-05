<script lang="ts">
  // ViewSwitcher — top toolbar of the calendar pane. Houses the view
  // selector (Month/Week/Day/Agenda), date navigation (<, Today, >),
  // tz indicator, and the Sync button.
  //
  // All four view kinds are wired as of 1F.

  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import ResponsiveSidebarToggle from '$lib/components/kit/ResponsiveSidebarToggle.svelte'
  import TimezonePicker from './TimezonePicker.svelte'
  import { calendarView, type ViewKind } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'

  interface ViewOption {
    kind: ViewKind
    label: string
  }

  const viewOptions = $derived<ViewOption[]>([
    { kind: 'month', label: $_('calendar.viewSwitcher.month') },
    { kind: 'week', label: $_('calendar.viewSwitcher.week') },
    { kind: 'day', label: $_('calendar.viewSwitcher.day') },
    { kind: 'agenda', label: $_('calendar.viewSwitcher.agenda') },
  ])

  // Human-readable title for the current anchor + view, formatted in the
  // user's chosen display timezone (calendarSettings.effectiveTimezone).
  const title = $derived.by(() => {
    const tz = calendarSettings.effectiveTimezone
    const d = calendarView.anchorDate
    const opts: Intl.DateTimeFormatOptions = calendarView.viewKind === 'month'
      ? { month: 'long', year: 'numeric', timeZone: tz }
      : { month: 'short', day: 'numeric', year: 'numeric', timeZone: tz }
    return new Intl.DateTimeFormat(undefined, opts).format(d)
  })

  let syncing = $state(false)
  // Animate whenever this button's own RPC is awaiting OR a background
  // sync (ticker / wake / network-online) is in progress. The local
  // `syncing` flag still gates button-disable so users can't double-click
  // — we only spread the spinner trigger.
  const animateSpin = $derived(syncing || calendarSources.isAnySyncing)

  // "+ Event" button is visible when at least one writable calendar exists.
  // Local + CalDAV (post-Chunk 2) both qualify; Google + Microsoft join in
  // Chunks 3-4 with the same flag derived from the provider's accessRole /
  // canEdit at sync time.
  const hasWritableCalendar = $derived.by(() => {
    for (const src of calendarSources.sources) {
      if (src.writable && (calendarSources.calendarsBySource[src.id] || []).length > 0) {
        return true
      }
    }
    return false
  })

  async function handleSync() {
    if (syncing) return
    syncing = true
    try {
      await calendarSources.syncAll()
    } finally {
      syncing = false
    }
  }

</script>

<div class="flex items-center justify-between gap-2 px-4 py-3 border-b border-border bg-background">
  <!-- Left: view selector + date nav. -->
  <div class="flex items-center gap-2 min-w-0">
    <ResponsiveSidebarToggle />
    <div class="inline-flex rounded-md border border-border overflow-hidden">
      {#each viewOptions as opt (opt.kind)}
        <button
          type="button"
          class="h-9 px-3 text-xs font-medium transition-colors
                 {calendarView.viewKind === opt.kind ? 'bg-primary text-primary-foreground' : 'bg-background hover:bg-muted/40 text-foreground'}"
          onclick={() => calendarView.setViewKind(opt.kind)}
        >
          {opt.label}
        </button>
      {/each}
    </div>

    <div class="inline-flex items-center gap-1 ml-2">
      <button
        type="button"
        class="p-2 rounded-md hover:bg-muted transition-colors"
        title={$_('calendar.viewSwitcher.prev')}
        aria-label={$_('calendar.viewSwitcher.prev')}
        onclick={() => calendarView.goPrev()}
      >
        <Icon icon="mdi:chevron-left" class="w-5 h-5 text-muted-foreground" />
      </button>
      <Button
        size="sm"
        variant="outline"
        onclick={() => calendarView.goToday()}
      >
        {$_('calendar.viewSwitcher.today')}
      </Button>
      <button
        type="button"
        class="p-2 rounded-md hover:bg-muted transition-colors"
        title={$_('calendar.viewSwitcher.next')}
        aria-label={$_('calendar.viewSwitcher.next')}
        onclick={() => calendarView.goNext()}
      >
        <Icon icon="mdi:chevron-right" class="w-5 h-5 text-muted-foreground" />
      </button>
    </div>

    <h2 class="text-sm font-semibold text-foreground ml-2 truncate">{title}</h2>
  </div>

  <!-- Right: tz picker + new event + sync. -->
  <div class="flex items-center gap-2 shrink-0">
    <div class="hidden sm:inline">
      <TimezonePicker />
    </div>
    {#if hasWritableCalendar}
      <Button
        size="sm"
        variant="outline"
        onclick={() => calendarView.requestNewEvent()}
      >
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_('calendar.viewSwitcher.newEvent')}
      </Button>
    {/if}
    <Button
      size="sm"
      variant="outline"
      onclick={handleSync}
      disabled={syncing}
    >
      {#if animateSpin}
        <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
      {:else}
        <Icon icon="mdi:sync" class="w-4 h-4 mr-1" />
      {/if}
      {$_('calendar.viewSwitcher.sync')}
    </Button>
  </div>
</div>

