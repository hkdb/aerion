<script lang="ts">
  // CalendarSettingsDialog — Calendar extension's settings.
  //
  // Two sections:
  //   1. Sources — per-source row with sync-interval picker, Sync Now,
  //      Delete, last-sync info. Add CalDAV button at the bottom.
  //   2. Alarms — informational copy (per-source toggles deferred).

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import * as Select from '$lib/components/ui/select'
  import { Button } from '$lib/components/ui/button'
  import ConfirmDialog from '$lib/components/kit/ConfirmDialog.svelte'
  import ColorPicker from '$lib/components/kit/ColorPicker.svelte'
  import OAuthCredsSlotEditor from '$lib/components/kit/OAuthCredsSlotEditor.svelte'
  import AddLocalCalendarDialog from './AddLocalCalendarDialog.svelte'
  import AddGoogleCalendarDialog from './AddGoogleCalendarDialog.svelte'
  import AddMicrosoftCalendarDialog from './AddMicrosoftCalendarDialog.svelte'
  import Icon from '@iconify/svelte'
  import { toasts } from '$lib/stores/toast'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'
  import AddCalDAVSourceDialog from './AddCalDAVSourceDialog.svelte'
  // @ts-ignore - wailsjs bindings
  import { Calendar_SetSyncInterval, Calendar_DeleteCalendar, Calendar_SetOrganizerIdentity, Calendar_ReprobeCalDAVOrganizerIdentities, Calendar_RenameSource } from '$wailsjs/go/app/App.js'
  import { Input } from '$lib/components/ui/input'
  import TimezonePicker from './TimezonePicker.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    open: boolean
    onClose?: () => void
  }

  let { open = $bindable(false), onClose }: Props = $props()

  // Add-source dialog opens from inside this one.
  let showAddSource = $state(false)
  let showAddLocalCalendar = $state(false)
  let showAddGoogle = $state(false)
  let showAddMicrosoft = $state(false)
  let deleteCalendarTarget = $state<{ id: string; displayName: string } | null>(null)
  let deletingCalendar = $state(false)

  // Local calendars surface separately from CalDAV sources — they don't
  // have sync intervals or Sync Now buttons. Derived for clean rendering.
  const localCalendars = $derived.by(() => {
    const out: { sourceId: string; id: string; displayName: string }[] = []
    for (const src of calendarSources.sources) {
      if (src.type !== 'local') continue
      for (const cal of calendarSources.calendarsBySource[src.id] || []) {
        out.push({ sourceId: src.id, id: cal.id, displayName: cal.displayName })
      }
    }
    return out
  })

  // Remote sources — every non-local source surfaces in the "Sources"
  // section. Local calendars have their own section above. Per-source
  // controls (sync interval, sync now, delete) apply uniformly; only the
  // CalDAV-specific organizer-email row is gated on type in the template.
  const remoteSources = $derived(
    calendarSources.sources.filter(s => s.type !== 'local')
  )

  // Defaults section. We expose:
  //   - One global Select listing all writable calendars (grouped by source).
  //   - One per-source Select for each writable source — provider default.
  // Both bind into calendarSettings; stale entries return '' via the store's
  // pruning so the trigger label gracefully falls back to "not set".
  const writableSources = $derived(
    calendarSources.sources.filter(s => s.writable)
  )

  interface GlobalDefaultOption {
    value: string         // calendar ID
    label: string         // "Personal · Murena"
  }

  const globalDefaultOptions = $derived.by<GlobalDefaultOption[]>(() => {
    const out: GlobalDefaultOption[] = []
    for (const src of writableSources) {
      const cals = (calendarSources.calendarsBySource[src.id] || []).filter(c => c.writable !== false)
      for (const cal of cals) {
        out.push({ value: cal.id, label: `${cal.displayName} · ${src.name}` })
      }
    }
    return out
  })

  function globalDefaultLabel(): string {
    const id = calendarSettings.globalDefaultCalendarId
    if (!id) return $_('calendar.add.globalDefaultUnset')
    const match = globalDefaultOptions.find(o => o.value === id)
    if (match) return match.label
    return $_('calendar.add.globalDefaultUnset')
  }

  function providerDefaultLabelFor(src: backend.Source): string {
    const id = calendarSettings.providerDefaultFor(src.id)
    if (!id) return $_('calendar.add.globalDefaultUnset')
    const cal = (calendarSources.calendarsBySource[src.id] || []).find(c => c.id === id)
    return cal?.displayName ?? $_('calendar.add.globalDefaultUnset')
  }

  function writableCalsFor(sourceId: string) {
    return (calendarSources.calendarsBySource[sourceId] || []).filter(c => c.writable !== false)
  }

  // Per-row state — pending delete + per-source spinner for Sync Now.
  let deleteTarget = $state<backend.Source | null>(null)
  let deleting = $state(false)
  let syncingSourceID = $state<string | null>(null)

  // Per-source organizer-email edits. Keyed by source.id; populated when
  // the user starts typing so we don't track every CalDAV row in memory
  // up front. Saving applies + clears the local entry.
  let organizerEdits = $state<Record<string, string>>({})
  let reprobingSourceID = $state<string | null>(null)

  // Per-source rename state. `renamingId` is the source currently in
  // edit mode (only one at a time); `renameDraft` holds the in-progress
  // text. Esc cancels; Enter / blur commits.
  let renamingId = $state<string | null>(null)
  let renameDraft = $state('')

  function startRename(src: backend.Source) {
    renamingId = src.id
    renameDraft = src.name
  }

  function cancelRename() {
    renamingId = null
    renameDraft = ''
  }

  async function commitRename(src: backend.Source) {
    const next = renameDraft.trim()
    if (next === '' || next === src.name) {
      cancelRename()
      return
    }
    try {
      await Calendar_RenameSource(src.id, next)
      await calendarSources.load()
      toasts.success($_('calendar.settings.renameToastSuccess', { values: { name: next } }))
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(msg)
    } finally {
      cancelRename()
    }
  }

  // Collapsible-section state. Object keyed by section id; entries
  // default to false (collapsed) so the user can scan all category
  // titles at once and drill into one. Toggle persists only for the
  // dialog's lifetime — re-opening the dialog re-collapses everything,
  // which keeps the discovery / scanning experience intentional.
  let expanded = $state<Record<string, boolean>>({
    local: false,
    sources: false,
    defaults: false,
    timezone: false,
    oauth: false,
  })

  function toggle(key: string) {
    expanded[key] = !expanded[key]
    expanded = { ...expanded }
  }

  function organizerDisplay(src: backend.Source): string {
    if (organizerEdits[src.id] !== undefined) return organizerEdits[src.id]
    const list = src.organizerIdentities ?? []
    return list[0] ?? ''
  }

  function organizerLooksValid(value: string): boolean {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim())
  }

  async function handleSaveOrganizer(src: backend.Source) {
    const value = (organizerEdits[src.id] ?? '').trim()
    if (value !== '' && !organizerLooksValid(value)) {
      toasts.error($_('calendar.add.organizerEmailInvalid'))
      return
    }
    try {
      await Calendar_SetOrganizerIdentity(src.id, value)
      toasts.success($_('calendar.settings.organizerEmailSaved'))
      delete organizerEdits[src.id]
      organizerEdits = { ...organizerEdits }
      await calendarSources.load()
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(msg)
    }
  }

  async function handleReprobeOrganizer(src: backend.Source) {
    if (reprobingSourceID !== null) return
    reprobingSourceID = src.id
    try {
      const count = await Calendar_ReprobeCalDAVOrganizerIdentities(src.id)
      if (count > 0) {
        toasts.success(
          $_('calendar.settings.organizerEmailReprobed', { values: { count } }),
        )
        delete organizerEdits[src.id]
        organizerEdits = { ...organizerEdits }
        await calendarSources.load()
        return
      }
      toasts.warning($_('calendar.settings.organizerEmailReprobeEmpty'))
      await calendarSources.load()
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(msg)
    } finally {
      reprobingSourceID = null
    }
  }

  // Sync interval options (mirrors api.go's validSyncIntervals).
  const INTERVAL_VALUES = [5, 15, 30, 60, 120, 240, 720]

  function formatIntervalLabel(n: number): string {
    if (n < 60) return $_('calendar.settings.intervalMinutes', { values: { n } })
    return $_('calendar.settings.intervalHours', { values: { n: n / 60 } })
  }

  // Relative last-sync time. Uses chosen tz only for label fidelity; the
  // relative-time logic is tz-agnostic.
  function lastSyncLabel(src: backend.Source): string {
    if (!src.lastSyncedAt || src.lastSyncedAt === 0) {
      return $_('calendar.settings.lastSyncNever')
    }
    const elapsed = Math.floor(Date.now() / 1000) - src.lastSyncedAt
    let when: string
    if (elapsed < 60) when = 'just now'
    else if (elapsed < 3600) when = `${Math.floor(elapsed / 60)}m`
    else if (elapsed < 86400) when = `${Math.floor(elapsed / 3600)}h`
    else when = `${Math.floor(elapsed / 86400)}d`
    return $_('calendar.settings.lastSync', { values: { time: when } })
  }

  function calendarCountLabel(src: backend.Source): string {
    const n = calendarSources.calendarsBySource[src.id]?.length ?? 0
    if (n === 1) return $_('calendar.settings.calendarCountOne')
    return $_('calendar.settings.calendarCount', { values: { count: n } })
  }

  async function handleIntervalChange(source: backend.Source, value: string) {
    const minutes = Number(value)
    if (!Number.isFinite(minutes) || minutes <= 0) return
    try {
      await Calendar_SetSyncInterval(source.id, minutes)
      toasts.success($_('calendar.settings.intervalToastSuccess', { values: { name: source.name } }))
      void calendarSources.load()
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error($_('calendar.settings.intervalToastError', { values: { message: msg } }))
    }
  }

  async function handleSyncNow(source: backend.Source) {
    if (syncingSourceID !== null) return
    syncingSourceID = source.id
    try {
      await calendarSources.syncSource(source.id)
    } finally {
      syncingSourceID = null
    }
  }

  async function handleConfirmDelete() {
    if (!deleteTarget) return
    deleting = true
    try {
      await calendarSources.deleteSource(deleteTarget.id)
      toasts.success($_('calendar.toast.sourceDeleted', { values: { name: deleteTarget.name } }))
      deleteTarget = null
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(msg)
    } finally {
      deleting = false
    }
  }

  async function handleConfirmDeleteCalendar() {
    if (!deleteCalendarTarget) return
    deletingCalendar = true
    const name = deleteCalendarTarget.displayName
    try {
      await Calendar_DeleteCalendar(deleteCalendarTarget.id)
      await calendarSources.load()
      toasts.success($_('calendar.settings.calendarDeleted', { values: { name } }))
      deleteCalendarTarget = null
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(msg)
    } finally {
      deletingCalendar = false
    }
  }

  function close() {
    open = false
    onClose?.()
  }

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      void calendarSources.load()
      return () => dialogGuardClose()
    }
  })

  // The currently-bound interval per source is a derived map so each
  // Select.Root has a stable bound value.
  function currentInterval(src: backend.Source): string {
    const m = src.syncIntervalMin ?? 15
    if (!INTERVAL_VALUES.includes(m)) return '15'
    return String(m)
  }

  // Reading $locale at template time below already binds reactivity for the
  // formatter calls; no local derived needed.
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>{$_('calendar.settings.title')}</Dialog.Title>
    </Dialog.Header>

    <div class="space-y-3 mt-2 max-h-[60vh] overflow-y-auto pr-1">
      <!-- Local calendars section -->
      <section class="border border-border rounded-md">
        <button
          type="button"
          class="w-full flex items-center justify-between p-3 text-left hover:bg-muted/30 rounded-md"
          onclick={() => toggle('local')}
          aria-expanded={expanded.local}
        >
          <h3 class="text-sm font-semibold text-foreground">
            {$_('calendar.settings.localCalendarsSection')}
          </h3>
          <Icon icon={expanded.local ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="w-4 h-4 text-muted-foreground" />
        </button>
        {#if expanded.local}
        <div class="space-y-2 px-3 pb-3 pt-1">

        {#if localCalendars.length === 0}
          <p class="text-sm text-muted-foreground py-2">
            {$_('calendar.settings.noLocalCalendars')}
          </p>
        {/if}

        {#each localCalendars as cal (cal.id)}
          <div class="flex items-center gap-3 p-3 border border-border rounded-md">
            <ColorPicker
              value={calendarSources.colorOfHex(cal.id)}
              onchange={(hex) => { void calendarSources.setColor(cal.id, hex) }}
            />
            <span class="flex-1 min-w-0 truncate text-sm text-foreground">
              {cal.displayName}
            </span>
            <Button
              variant="ghost"
              size="sm"
              class="h-8 text-destructive hover:text-destructive shrink-0"
              onclick={() => { deleteCalendarTarget = cal }}
              aria-label={$_('calendar.common.delete')}
            >
              <Icon icon="mdi:delete-outline" class="w-3.5 h-3.5" />
            </Button>
          </div>
        {/each}

        <Button
          variant="outline"
          size="sm"
          class="mt-2"
          onclick={() => { showAddLocalCalendar = true }}
        >
          <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
          {$_('calendar.settings.addLocalCalendar')}
        </Button>
        </div>
        {/if}
      </section>

      <!-- Sources section — CalDAV + Google + Microsoft sources. Local
           calendars live in their own section above. -->
      <section class="border border-border rounded-md">
        <button
          type="button"
          class="w-full flex items-center justify-between p-3 text-left hover:bg-muted/30 rounded-md"
          onclick={() => toggle('sources')}
          aria-expanded={expanded.sources}
        >
          <h3 class="text-sm font-semibold text-foreground">
            {$_('calendar.settings.sourcesSection')}
          </h3>
          <Icon icon={expanded.sources ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="w-4 h-4 text-muted-foreground" />
        </button>
        {#if expanded.sources}
        <div class="space-y-2 px-3 pb-3 pt-1">

        {#if remoteSources.length === 0}
          <p class="text-sm text-muted-foreground py-3">
            {$_('calendar.settings.noSources')}
          </p>
        {/if}

        {#each remoteSources as src (src.id)}
          <div class="flex flex-col gap-2 p-3 border border-border rounded-md">
            <!-- Source header row -->
            <div class="flex items-start justify-between gap-3">
              <div class="flex-1 min-w-0">
                {#if renamingId === src.id}
                  <div class="flex items-center gap-1">
                    <Input
                      type="text"
                      class="h-8 text-sm flex-1 min-w-0"
                      bind:value={renameDraft}
                      autofocus
                      onkeydown={(e) => {
                        if (e.key === 'Enter') { e.preventDefault(); void commitRename(src) }
                        if (e.key === 'Escape') { e.preventDefault(); cancelRename() }
                      }}
                      onblur={() => void commitRename(src)}
                    />
                  </div>
                {/if}
                {#if renamingId !== src.id}
                  <div class="flex items-center gap-1 min-w-0">
                    <span class="text-sm font-medium text-foreground truncate">{src.name}</span>
                    <Button
                      variant="ghost"
                      size="sm"
                      class="h-6 w-6 p-0 shrink-0"
                      onclick={() => startRename(src)}
                      title={$_('calendar.settings.renameTitle')}
                      aria-label={$_('calendar.settings.renameTitle')}
                    >
                      <Icon icon="mdi:pencil-outline" class="w-3.5 h-3.5" />
                    </Button>
                  </div>
                {/if}
                <div class="text-xs text-muted-foreground mt-0.5">
                  {calendarCountLabel(src)} · {lastSyncLabel(src)}
                </div>
                {#if src.lastError}
                  <div class="flex items-start gap-1 text-xs text-destructive mt-1 min-w-0" title={src.lastError}>
                    <Icon icon="mdi:alert-circle" class="w-3 h-3 shrink-0 mt-0.5" />
                    <span class="truncate">{src.lastError}</span>
                  </div>
                {/if}
              </div>

              <div class="flex items-center gap-2 shrink-0">
                <Select.Root
                  value={currentInterval(src)}
                  onValueChange={(v) => { if (v) void handleIntervalChange(src, v) }}
                >
                  <Select.Trigger class="h-8 w-32 text-xs">
                    {formatIntervalLabel(Number(currentInterval(src)))}
                  </Select.Trigger>
                  <Select.Content>
                    {#each INTERVAL_VALUES as v (v)}
                      <Select.Item value={String(v)} label={formatIntervalLabel(v)} />
                    {/each}
                  </Select.Content>
                </Select.Root>

                <Button
                  variant="outline"
                  size="sm"
                  onclick={() => handleSyncNow(src)}
                  disabled={syncingSourceID !== null}
                  class="h-8"
                >
                  {#if syncingSourceID === src.id}
                    <Icon icon="mdi:loading" class="w-3.5 h-3.5 animate-spin" />
                  {:else}
                    <Icon icon="mdi:sync" class="w-3.5 h-3.5" />
                  {/if}
                  <span class="ml-1">{$_('calendar.settings.syncNow')}</span>
                </Button>

                <Button
                  variant="ghost"
                  size="sm"
                  class="h-8 text-destructive hover:text-destructive"
                  onclick={() => { deleteTarget = src }}
                >
                  <Icon icon="mdi:delete-outline" class="w-3.5 h-3.5" />
                </Button>
              </div>
            </div>

            <!-- Organizer email row. CalDAV-only: Google + Microsoft
                 sources auto-populate organizerIdentities from the bound
                 mail account at source-add time and don't need the
                 user-edit / re-probe path. CalDAV picks this up from
                 PROPFIND <C:calendar-user-address-set>; servers that
                 don't publish it land here empty and the user can type
                 one. "Re-probe" re-runs the PROPFIND. -->
            {#if src.type === 'caldav'}
            <div class="flex items-center gap-2 pl-2">
              <span class="text-xs text-muted-foreground shrink-0 w-28">
                {$_('calendar.settings.organizerEmailLabel')}
              </span>
              <Input
                type="email"
                placeholder={$_('calendar.settings.organizerEmailPlaceholder')}
                value={organizerDisplay(src)}
                oninput={(e) => {
                  organizerEdits[src.id] = (e.currentTarget as HTMLInputElement).value
                  organizerEdits = { ...organizerEdits }
                }}
                class="h-8 text-xs flex-1 min-w-0"
              />
              <Button
                variant="outline"
                size="sm"
                class="h-8 shrink-0"
                onclick={() => handleSaveOrganizer(src)}
                disabled={organizerEdits[src.id] === undefined || organizerEdits[src.id] === (src.organizerIdentities?.[0] ?? '')}
              >
                {$_('calendar.common.save')}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                class="h-8 shrink-0"
                onclick={() => handleReprobeOrganizer(src)}
                disabled={reprobingSourceID !== null}
                title={$_('calendar.settings.organizerEmailReprobeTitle')}
              >
                {#if reprobingSourceID === src.id}
                  <Icon icon="mdi:loading" class="w-3.5 h-3.5 animate-spin" />
                {/if}
                {#if reprobingSourceID !== src.id}
                  <Icon icon="mdi:cloud-search-outline" class="w-3.5 h-3.5" />
                {/if}
              </Button>
            </div>
            {/if}

            <!-- Per-calendar color rows. Color is the calendar's actual
                 rendered hue (stored hex if set, else the deterministic
                 HSL→hex fallback) — clicking opens the palette + hex input
                 popover. -->
            {#each (calendarSources.calendarsBySource[src.id] ?? []) as cal (cal.id)}
              <div class="flex items-center gap-3 pl-2">
                <ColorPicker
                  value={calendarSources.colorOfHex(cal.id)}
                  onchange={(hex) => { void calendarSources.setColor(cal.id, hex) }}
                />
                <span class="flex-1 min-w-0 truncate text-sm text-foreground">
                  {cal.displayName}
                </span>
              </div>
            {/each}
          </div>
        {/each}

        <div class="flex flex-wrap gap-2 mt-2">
          <Button
            variant="outline"
            size="sm"
            onclick={() => { showAddSource = true }}
          >
            <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
            {$_('calendar.sidebar.addSource')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onclick={() => { showAddGoogle = true }}
          >
            <Icon icon="mdi:google" class="w-4 h-4 mr-1" />
            {$_('calendar.settings.addGoogle')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onclick={() => { showAddMicrosoft = true }}
          >
            <Icon icon="mdi:microsoft-outlook" class="w-4 h-4 mr-1" />
            {$_('calendar.settings.addOutlook')}
          </Button>
        </div>
        </div>
        {/if}
      </section>

      <!-- Defaults for new events — both global + per-source. Stale entries
           (the source/calendar got deleted) return '' from the store getters
           so the trigger label gracefully shows "not set" until the user
           picks again. -->
      <section class="border border-border rounded-md">
        <button
          type="button"
          class="w-full flex items-center justify-between p-3 text-left hover:bg-muted/30 rounded-md"
          onclick={() => toggle('defaults')}
          aria-expanded={expanded.defaults}
        >
          <h3 class="text-sm font-semibold text-foreground">
            {$_('calendar.settings.defaultsSection')}
          </h3>
          <Icon icon={expanded.defaults ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="w-4 h-4 text-muted-foreground" />
        </button>
        {#if expanded.defaults}
        <div class="space-y-2 px-3 pb-3 pt-1">

        {#if writableSources.length === 0}
          <p class="text-xs text-muted-foreground">
            {$_('calendar.settings.noDefaultsHint')}
          </p>
        {/if}

        {#if writableSources.length > 0}
          <div class="flex items-center justify-between gap-3 p-3 border border-border rounded-md">
            <span class="text-sm text-foreground shrink-0">{$_('calendar.settings.globalDefaultLabel')}</span>
            <Select.Root
              value={calendarSettings.globalDefaultCalendarId}
              onValueChange={(v) => { if (v !== undefined) calendarSettings.setGlobalDefaultCalendarId(v) }}
            >
              <Select.Trigger class="h-8 max-w-xs text-xs">
                {globalDefaultLabel()}
              </Select.Trigger>
              <Select.Content>
                {#each globalDefaultOptions as opt (opt.value)}
                  <Select.Item value={opt.value} label={opt.label} />
                {/each}
              </Select.Content>
            </Select.Root>
          </div>

          <div class="text-xs text-muted-foreground mt-3">
            {$_('calendar.settings.providerDefaultsLabel')}
          </div>
          {#each writableSources as src (src.id)}
            {@const cals = writableCalsFor(src.id)}
            {#if cals.length > 0}
              <div class="flex items-center justify-between gap-3 p-3 border border-border rounded-md">
                <div class="min-w-0">
                  <div class="text-sm font-medium text-foreground truncate">{src.name}</div>
                  <div class="text-xs text-muted-foreground mt-0.5">{calendarCountLabel(src)}</div>
                </div>
                <Select.Root
                  value={calendarSettings.providerDefaultFor(src.id)}
                  onValueChange={(v) => { if (v !== undefined) calendarSettings.setProviderDefaultFor(src.id, v) }}
                >
                  <Select.Trigger class="h-8 max-w-xs text-xs">
                    {providerDefaultLabelFor(src)}
                  </Select.Trigger>
                  <Select.Content>
                    {#each cals as cal (cal.id)}
                      <Select.Item value={cal.id} label={cal.displayName} />
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
            {/if}
          {/each}
        {/if}
        </div>
        {/if}
      </section>

      <!-- Display timezone — picker for choosing how times render across
           views. The same TimezonePicker is also mounted in ViewSwitcher's
           top-right; both paths write to calendarSettings.displayTimezone. -->
      <section class="border border-border rounded-md">
        <button
          type="button"
          class="w-full flex items-center justify-between p-3 text-left hover:bg-muted/30 rounded-md"
          onclick={() => toggle('timezone')}
          aria-expanded={expanded.timezone}
        >
          <h3 class="text-sm font-semibold text-foreground">
            {$_('calendar.settings.timezoneSection')}
          </h3>
          <Icon icon={expanded.timezone ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="w-4 h-4 text-muted-foreground" />
        </button>
        {#if expanded.timezone}
        <div class="px-3 pb-3 pt-1 space-y-2">
          <p class="text-xs text-muted-foreground">
            {$_('calendar.settings.timezoneHelp')}
          </p>
          <TimezonePicker />
        </div>
        {/if}
      </section>

      <!-- OAuth Credentials (advanced) — picker matches Contacts'. Google
           shows "Aerion testing" as the default since the mail-app's verified
           client carries no Calendar scopes. Microsoft resolves to mail's
           client (consolidated in core_provider.go); Custom is always an
           escape hatch. -->
      <section class="border border-border rounded-md">
        <button
          type="button"
          class="w-full flex items-center justify-between p-3 text-left hover:bg-muted/30 rounded-md"
          onclick={() => toggle('oauth')}
          aria-expanded={expanded.oauth}
        >
          <h3 class="text-sm font-semibold text-foreground">
            {$_('calendar.settings.oauthSection')}
          </h3>
          <Icon icon={expanded.oauth ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="w-4 h-4 text-muted-foreground" />
        </button>
        {#if expanded.oauth}
        <div class="px-3 pb-3 pt-1 space-y-3">
          <p class="text-xs text-muted-foreground">
            {$_('calendar.settings.oauthHelp')}
          </p>
          <OAuthCredsSlotEditor
            configID="google-calendar"
            extensionID="calendar"
            label={$_('calendar.settings.oauthGoogleLabel')}
            secretRequired={true}
          />
          <OAuthCredsSlotEditor
            configID="microsoft-calendar"
            extensionID="calendar"
            label={$_('calendar.settings.oauthMicrosoftLabel')}
            secretRequired={false}
          />
        </div>
        {/if}
      </section>
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={close}>{$_('calendar.common.close')}</Button>
    </div>
  </Dialog.Content>
</Dialog.Root>

<AddCalDAVSourceDialog bind:open={showAddSource} onClose={() => { void calendarSources.load() }} />

<AddLocalCalendarDialog bind:open={showAddLocalCalendar} onCreated={() => { void calendarSources.load() }} />

<AddGoogleCalendarDialog bind:open={showAddGoogle} onClose={() => { void calendarSources.load() }} />

<AddMicrosoftCalendarDialog bind:open={showAddMicrosoft} onClose={() => { void calendarSources.load() }} />

<ConfirmDialog
  open={deleteTarget !== null}
  title={deleteTarget ? $_('calendar.settings.deleteConfirmTitle', { values: { name: deleteTarget.name } }) : ''}
  description={$_('calendar.settings.deleteConfirmDescription')}
  confirmLabel={$_('calendar.common.delete')}
  cancelLabel={$_('calendar.common.cancel')}
  variant="destructive"
  loading={deleting}
  onConfirm={handleConfirmDelete}
  onCancel={() => { deleteTarget = null }}
/>

<ConfirmDialog
  open={deleteCalendarTarget !== null}
  title={deleteCalendarTarget ? $_('calendar.settings.deleteCalendarConfirmTitle', { values: { name: deleteCalendarTarget.displayName } }) : ''}
  description={$_('calendar.settings.deleteCalendarConfirmDescription')}
  confirmLabel={$_('calendar.common.delete')}
  cancelLabel={$_('calendar.common.cancel')}
  variant="destructive"
  loading={deletingCalendar}
  onConfirm={handleConfirmDeleteCalendar}
  onCancel={() => { deleteCalendarTarget = null }}
/>
