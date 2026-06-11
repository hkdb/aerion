<script lang="ts">
  // CalendarSidebar — left sidebar listing sources + their calendars.
  // Issue #118 pattern: one section per source, per-calendar rows with
  // checkbox + color swatch + truncated name. Bottom strip (anchored via
  // SidebarFrame's footer slot): last-sync indicator + settings cog.
  //
  // NOT built on kit SourceSidebar because calendar's semantics are
  // multi-toggle visibility, not single-source-select. Built on the
  // lower-level kit `SidebarFrame` which owns container chrome + responsive
  // overlay + title + body/footer slots; visual parity with contacts'
  // sidebar (and any future extension sidebar) is automatic.

  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import SidebarFrame from '$lib/components/kit/SidebarFrame.svelte'
  import SidebarFooter from '$lib/components/kit/SidebarFooter.svelte'
  import AddCalendarMenu from './AddCalendarMenu.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { onMount, onDestroy } from 'svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    onOpenSettings?: () => void
  }

  let { onOpenSettings }: Props = $props()

  // Sections derived from the sources store. Empty when no sources are
  // configured yet — the bottom-strip "Add CalDAV" button is the entry point.
  const sections = $derived.by(() => {
    const out: { source: backend.Source; calendars: backend.Calendar[] }[] = []
    for (const src of calendarSources.sources) {
      const cals = calendarSources.calendarsBySource[src.id] || []
      out.push({ source: src, calendars: cals })
    }
    return out
  })

  // Reactive `now` so the elapsed-time label refreshes every 60s without
  // needing a sync event. Direct Date.now() inside the $derived would be
  // captured at first read and stay frozen until another reactive dep
  // changes.
  let now = $state(Date.now())
  let nowTickId: ReturnType<typeof setInterval> | null = null
  onMount(() => {
    nowTickId = setInterval(() => { now = Date.now() }, 60_000)
  })
  onDestroy(() => {
    if (nowTickId !== null) clearInterval(nowTickId)
  })

  // Friendly last-sync label for the bottom strip. Picks the most-recent
  // last_synced_at across sources; "never" if none have synced yet.
  const lastSyncedLabel = $derived.by(() => {
    let latest = 0
    for (const s of calendarSources.sources) {
      if (s.lastSyncedAt > latest) latest = s.lastSyncedAt
    }
    if (latest === 0) return $_('calendar.sidebar.lastSyncNever')
    const elapsedSec = Math.floor(now / 1000) - latest
    if (elapsedSec < 60) return $_('calendar.sidebar.lastSync', { values: { time: 'just now' } })
    if (elapsedSec < 3600) return $_('calendar.sidebar.lastSync', { values: { time: `${Math.floor(elapsedSec / 60)}m ago` } })
    if (elapsedSec < 86400) return $_('calendar.sidebar.lastSync', { values: { time: `${Math.floor(elapsedSec / 3600)}h ago` } })
    return $_('calendar.sidebar.lastSync', { values: { time: `${Math.floor(elapsedSec / 86400)}d ago` } })
  })

  // Phase-aware label when a sync is in flight. Started → "Syncing Murena…";
  // progress → "Syncing Murena · Personal (2/4)". Falls back to a bare
  // "Syncing…" when the source name isn't yet known (sync-started raced
  // ahead of the source-name lookup, extremely rare).
  const syncingLabel = $derived.by(() => {
    const s = calendarSources.currentSyncState
    if (!s) return ''
    if (s.phase === 'progress' && s.calendarName && s.totalCalendars) {
      return $_('calendar.sidebar.syncingCalendar', { values: {
        source: s.sourceName,
        name: s.calendarName,
        n: s.currentCalendar ?? 0,
        total: s.totalCalendars,
      }})
    }
    if (s.sourceName) {
      return $_('calendar.sidebar.syncing', { values: { name: s.sourceName } })
    }
    return $_('calendar.sidebar.syncingGeneric')
  })

  const currentError = $derived(calendarSources.currentErrorState?.errorMessage ?? '')

  function toggleVisibility(cal: backend.Calendar) {
    void calendarSources.setVisible(cal.id, !cal.visible)
  }
</script>

<SidebarFrame title={$_('calendar.sidebar.title')}>
  {#snippet body()}
    <div class="py-2">
      {#if sections.length === 0}
        <p class="px-4 py-3 text-xs text-muted-foreground">
          {$_('calendar.sidebar.noCalendars')}
        </p>
      {/if}

      {#each sections as section (section.source.id)}
        <div class="mb-3">
          <div class="px-4 mb-1 text-[11px] uppercase tracking-wider text-muted-foreground truncate">
            {section.source.name}
          </div>
          {#each section.calendars as cal (cal.id)}
            <div
              class="group flex items-center gap-2 mx-2 px-2 py-1.5 rounded-md cursor-pointer text-sm
                     hover:bg-muted/40 transition-colors"
              role="button"
              tabindex="0"
              onclick={() => toggleVisibility(cal)}
              onkeydown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  toggleVisibility(cal)
                }
              }}
            >
              <input
                type="checkbox"
                class="shrink-0 cursor-pointer accent-primary"
                checked={cal.visible}
                onclick={(e) => { e.stopPropagation(); toggleVisibility(cal) }}
                aria-label={cal.displayName}
              />
              <span
                class="shrink-0 inline-block w-2.5 h-2.5 rounded-full"
                style:background-color={calendarSources.colorOf(cal.id)}
                aria-hidden="true"
              ></span>
              <span class="flex-1 min-w-0 truncate text-foreground">{cal.displayName}</span>
            </div>
          {/each}
        </div>
      {/each}

      <!-- "+ Add Calendar" inline-expanding picker. Owns its own dialogs
           for Local + CalDAV; Phase 2 entries appear disabled until wired. -->
      <AddCalendarMenu />
    </div>
  {/snippet}

  {#snippet footer()}
    <SidebarFooter>
      {#snippet leading()}
        {#if calendarSources.isAnySyncing}
          <Icon icon="mdi:sync" class="w-4 h-4 shrink-0 animate-spin" />
          <span class="truncate">{syncingLabel}</span>
        {/if}
        {#if !calendarSources.isAnySyncing && currentError !== ''}
          <Icon icon="mdi:alert-circle" class="w-4 h-4 shrink-0 text-destructive" />
          <span class="truncate text-destructive" title={currentError}>
            {$_('calendar.sidebar.syncError')}
          </span>
        {/if}
        {#if !calendarSources.isAnySyncing && currentError === ''}
          <Icon icon="mdi:sync" class="w-4 h-4 shrink-0" />
          <span class="truncate">{lastSyncedLabel}</span>
        {/if}
      {/snippet}
      {#snippet trailing()}
        <button
          class="p-1 rounded hover:bg-muted/40"
          title={$_('calendar.sidebar.settings')}
          onclick={() => onOpenSettings?.()}
          type="button"
          aria-label={$_('calendar.sidebar.settings')}
        >
          <Icon icon="mdi:cog-outline" class="w-4 h-4" />
        </button>
      {/snippet}
    </SidebarFooter>
  {/snippet}
</SidebarFrame>
