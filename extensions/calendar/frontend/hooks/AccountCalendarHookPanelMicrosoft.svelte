<script lang="ts">
  // AccountCalendarHookPanelMicrosoft — host-rendered hook panel that
  // fires after the user adds a Microsoft / Outlook mail account through
  // AccountDialog. Sibling of AccountCalendarHookPanelGoogle with the
  // bridge methods + field-name shape swapped for Graph:
  //   - Calendar_ListMicrosoftCalendarsForAccount(accountId)
  //   - Calendar_AddMicrosoftSource(accountId, name, selections)
  //   - MicrosoftCalendarChoice uses `name` instead of `summary` and
  //     `isDefaultCalendar` instead of `primary`.

  import { onMount } from 'svelte'
  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { addToast } from '$lib/stores/toast'
  import { refreshExtensionRegistry } from '$lib/stores/extensionRegistry.svelte'
  // @ts-ignore - wailsjs bindings
  import { SetExtensionEnabled, Calendar_ListMicrosoftCalendarsForAccount, Calendar_AddMicrosoftSource, Calendar_GrantCalendarAccess } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { v1, backend } from '$wailsjs/go/models'

  interface Props {
    hook: v1.AccountSetupHookRequest
    accountId: string
    accountName: string
    accountEmail: string
    onResolved: () => void
  }

  const { hook, accountId, accountName, accountEmail, onResolved }: Props = $props()

  let calendars = $state<backend.MicrosoftCalendarChoice[]>([])
  let selectedIds = $state<Set<string>>(new Set())
  // svelte-ignore state_referenced_locally
  // sourceName captures accountName's initial value intentionally — the
  // user edits the field after mount; the prop is stable for the panel's
  // lifetime, so no reactive binding is needed.
  let sourceName = $state(accountName ? `Outlook: ${accountName}` : 'Outlook Calendar')
  let loading = $state(true)
  let busy = $state(false)
  let done = $state(false)
  let needsConsent = $state(false)
  let granting = $state(false)
  let error = $state<string | null>(null)

  onMount(() => void loadCalendars())

  async function loadCalendars() {
    loading = true
    error = null
    needsConsent = false
    try {
      const list = await Calendar_ListMicrosoftCalendarsForAccount(accountId)
      calendars = list || []
      const next = new Set<string>()
      for (const cal of calendars) {
        if (cal.writable) next.add(cal.id)
      }
      selectedIds = next
    } catch (err) {
      const msg = String(err)
      needsConsent = msg.includes('additional consent required')
      if (!needsConsent) {
        error = msg
      }
    } finally {
      loading = false
    }
  }

  function toggleCalendar(id: string) {
    const next = new Set(selectedIds)
    if (next.has(id)) {
      next.delete(id)
      selectedIds = next
      return
    }
    next.add(id)
    selectedIds = next
  }

  function toggleSelectAll() {
    if (selectedIds.size === calendars.length) {
      selectedIds = new Set()
      return
    }
    selectedIds = new Set(calendars.map((c) => c.id))
  }

  const allSelected = $derived(calendars.length > 0 && selectedIds.size === calendars.length)
  const someSelected = $derived(selectedIds.size > 0 && selectedIds.size < calendars.length)
  let selectAllEl = $state<HTMLInputElement | null>(null)
  $effect(() => {
    if (selectAllEl) selectAllEl.indeterminate = someSelected
  })

  async function grantAccess() {
    if (granting) return
    granting = true
    error = null
    try {
      await Calendar_GrantCalendarAccess('microsoft', accountId, accountName)
      await loadCalendars()
    } catch (err) {
      error = (err as Error)?.message ?? String(err)
    } finally {
      granting = false
    }
  }

  async function setUp() {
    if (selectedIds.size === 0) {
      error = $_('calendar.hooks.errorPick')
      return
    }
    if (!sourceName.trim()) {
      error = $_('calendar.hooks.errorName')
      return
    }
    busy = true
    error = null
    try {
      const selections = calendars
        .filter((c) => selectedIds.has(c.id))
        .map((c) => ({ id: c.id, displayName: c.name, color: '', writable: c.writable }))
      await Calendar_AddMicrosoftSource(accountId, sourceName.trim(), accountEmail, selections)
      await SetExtensionEnabled('calendar', true)
      await refreshExtensionRegistry()
      done = true
      addToast({
        type: 'success',
        message: $_('calendar.hooks.successToast', { values: { name: accountName } }),
      })
    } catch (err) {
      console.error('Failed to set up Outlook calendar:', err)
      error = (err as Error)?.message || String(err)
    } finally {
      busy = false
    }
  }

  function skip() {
    onResolved()
  }

  function close() {
    onResolved()
  }
</script>

<section class="p-4 border border-border rounded-md mb-3 bg-card text-card-foreground">
  <header class="flex items-start gap-3 mb-3">
    <Icon icon="mdi:calendar-month" width="24" height="24" />
    <div>
      <h3 class="m-0 mb-1 text-[15px] font-semibold text-foreground">{hook.buttonLabel}</h3>
      {#if hook.description}
        <p class="m-0 text-sm text-muted-foreground">{hook.description}</p>
      {/if}
    </div>
  </header>

  {#if !done}
    {#if loading}
      <p class="text-sm text-muted-foreground">{$_('calendar.hooks.loading')}</p>
    {/if}

    {#if !loading && needsConsent}
      <div class="rounded-md border border-yellow-400/40 bg-yellow-400/10 p-3 text-xs text-yellow-700 dark:text-yellow-300 mb-3 space-y-2">
        <div>{$_('calendar.hooks.consentNeeded')}</div>
        <Button size="sm" variant="outline" onclick={grantAccess} disabled={granting}>
          {#if granting}{$_('calendar.hooks.granting')}{:else}{$_('calendar.hooks.grantButton')}{/if}
        </Button>
      </div>
    {/if}

    {#if !loading && !needsConsent && calendars.length > 0}
      <div class="space-y-1 mb-3">
        <Label>{$_('calendar.hooks.calendarsLabel')}</Label>
        <div class="max-h-48 overflow-y-auto rounded-md border border-border">
          <label
            class="flex items-center gap-2 px-3 py-2 text-sm border-b border-border bg-muted/30 cursor-pointer hover:bg-muted/50"
          >
            <input
              type="checkbox"
              bind:this={selectAllEl}
              checked={allSelected}
              onchange={toggleSelectAll}
            />
            <span class="font-medium">{$_('calendar.hooks.selectAll')}</span>
          </label>
          {#each calendars as cal (cal.id)}
            <label
              class="flex items-center gap-2 px-3 py-2 text-sm border-b border-border last:border-b-0
                     hover:bg-muted/40 cursor-pointer"
              title={!cal.writable ? $_('calendar.hooks.readOnlyHint') : ''}
            >
              <input
                type="checkbox"
                checked={selectedIds.has(cal.id)}
                onchange={() => toggleCalendar(cal.id)}
              />
              <span class="truncate flex-1">{cal.name}</span>
              {#if cal.isDefaultCalendar}
                <span class="text-xs text-muted-foreground">{$_('calendar.hooks.defaultBadge')}</span>
              {/if}
              {#if !cal.writable}
                <span class="text-xs text-muted-foreground">{$_('calendar.hooks.readOnlyBadge')}</span>
              {/if}
            </label>
          {/each}
        </div>
      </div>

      <div class="space-y-1 mb-3">
        <Label for="hook-ms-source-name">{$_('calendar.hooks.sourceNameLabel')}</Label>
        <Input id="hook-ms-source-name" bind:value={sourceName} />
      </div>
    {/if}

    {#if error}
      <p class="text-sm text-destructive m-0 mb-2" role="alert">{error}</p>
    {/if}

    <div class="flex justify-end gap-2">
      <Button variant="ghost" onclick={skip} disabled={busy}>{$_('calendar.hooks.skip')}</Button>
      <Button onclick={setUp} disabled={busy || loading || needsConsent || calendars.length === 0}>
        {#if busy}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
        {/if}
        {$_('calendar.hooks.setup')}
      </Button>
    </div>
  {/if}

  {#if done}
    <div class="flex items-center gap-2 text-primary">
      <Icon icon="mdi:check-circle" width="20" height="20" />
      <span class="flex-1">{$_('calendar.hooks.completeMessage')}</span>
      <Button variant="ghost" onclick={close}>{$_('calendar.hooks.done')}</Button>
    </div>
  {/if}
</section>
