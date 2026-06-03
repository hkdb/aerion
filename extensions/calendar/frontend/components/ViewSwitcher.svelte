<script lang="ts">
  // ViewSwitcher — top toolbar of the calendar pane. Houses the view
  // selector (Month/Week/Day/Agenda), date navigation (<, Today, >),
  // tz indicator, and the Sync button.
  //
  // All four view kinds are wired as of 1F.

  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { calendarView, type ViewKind } from '$extensions/calendar/frontend/stores/calendarView.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'

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

  // Browser's resolved IANA timezone, for the tz indicator.
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  // Human-readable title for the current anchor + view. Phase 1D only
  // renders month — other views use simpler labels.
  const title = $derived.by(() => {
    const d = calendarView.anchorDate
    if (calendarView.viewKind === 'month') {
      return d.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })
    }
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
  })

  let syncing = $state(false)

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

<div class="flex items-center justify-between gap-2 px-3 py-2 border-b border-border bg-background">
  <!-- Left: view selector + date nav. -->
  <div class="flex items-center gap-2 min-w-0">
    <div class="inline-flex rounded-md border border-border overflow-hidden">
      {#each viewOptions as opt (opt.kind)}
        <button
          type="button"
          class="px-2.5 py-1 text-xs font-medium transition-colors
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
        class="p-1 rounded hover:bg-muted/40"
        title={$_('calendar.viewSwitcher.prev')}
        aria-label={$_('calendar.viewSwitcher.prev')}
        onclick={() => calendarView.goPrev()}
      >
        <Icon icon="mdi:chevron-left" class="w-4 h-4" />
      </button>
      <Button
        size="sm"
        variant="outline"
        class="h-7 px-2 text-xs"
        onclick={() => calendarView.goToday()}
      >
        {$_('calendar.viewSwitcher.today')}
      </Button>
      <button
        type="button"
        class="p-1 rounded hover:bg-muted/40"
        title={$_('calendar.viewSwitcher.next')}
        aria-label={$_('calendar.viewSwitcher.next')}
        onclick={() => calendarView.goNext()}
      >
        <Icon icon="mdi:chevron-right" class="w-4 h-4" />
      </button>
    </div>

    <h2 class="text-sm font-semibold text-foreground ml-2 truncate">{title}</h2>
  </div>

  <!-- Right: tz indicator + sync. -->
  <div class="flex items-center gap-2 shrink-0">
    <span class="text-xs text-muted-foreground hidden sm:inline">
      {$_('calendar.viewSwitcher.tzLabel', { values: { tz } })}
    </span>
    <Button
      size="sm"
      variant="outline"
      class="h-7 px-2 text-xs"
      onclick={handleSync}
      disabled={syncing}
    >
      {#if syncing}
        <Icon icon="mdi:loading" class="w-3.5 h-3.5 mr-1 animate-spin" />
      {:else}
        <Icon icon="mdi:sync" class="w-3.5 h-3.5 mr-1" />
      {/if}
      {$_('calendar.viewSwitcher.sync')}
    </Button>
  </div>
</div>
