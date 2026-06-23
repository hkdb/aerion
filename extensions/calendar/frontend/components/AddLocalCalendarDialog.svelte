<script lang="ts">
  // AddLocalCalendarDialog — small form that creates a local calendar.
  //
  // First call lazily creates the synthetic "Local" source via
  // Calendar_AddLocalSource (idempotent), then attaches the new calendar.
  // Color is optional; empty falls back to the HSL hash via colorOfHex.

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import ColorPicker from '$lib/components/kit/ColorPicker.svelte'
  import Icon from '@iconify/svelte'
  import { toasts } from '$lib/stores/toast'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import AddCalendarDefaultsControl from './AddCalendarDefaultsControl.svelte'
  import { applyDefaultsAfterAdd } from '$extensions/calendar/frontend/lib/defaultsApply'
  // @ts-ignore - wailsjs bindings
  import { Calendar_AddLocalSource, Calendar_AddLocalCalendar } from '$wailsjs/go/app/App.js'

  interface Props {
    open: boolean
    onClose?: () => void
    onCreated?: () => void
  }

  let { open = $bindable(false), onClose, onCreated }: Props = $props()

  let displayName = $state('')
  let color = $state('')
  let submitting = $state(false)
  let errorMessage = $state('')
  let providerDefaultTempId = $state('')
  let globalDefaultRef = $state('')

  const NEW_TEMP_ID = 'new'

  $effect(() => {
    if (!open) return
    dialogGuardOpen()
    return () => dialogGuardClose()
  })

  $effect(() => {
    if (!open) return
    displayName = ''
    color = ''
    errorMessage = ''
    submitting = false
    providerDefaultTempId = ''
    globalDefaultRef = ''
  })

  async function ensureLocalSource(): Promise<string> {
    const existing = calendarSources.sources.find(s => s.type === 'local')
    if (existing) return existing.id
    return await Calendar_AddLocalSource('Local')
  }

  async function handleSave() {
    if (submitting) return
    if (!displayName.trim()) {
      errorMessage = $_('calendar.localCalendar.errorNameRequired')
      return
    }
    submitting = true
    errorMessage = ''
    try {
      const sourceID = await ensureLocalSource()
      const newCalendarID = await Calendar_AddLocalCalendar(sourceID, displayName.trim(), color)
      await calendarSources.load()
      applyDefaultsAfterAdd({
        sourceId: sourceID,
        added: [{ id: newCalendarID, tempId: NEW_TEMP_ID, writable: true }],
        providerDefaultTempId,
        globalDefaultRef,
      })
      toasts.success($_('calendar.localCalendar.toastCreated', { values: { name: displayName.trim() } }))
      // Clear the submitting flag BEFORE close() so the guard inside
      // close() (which blocks user-initiated closes during a request)
      // doesn't short-circuit the auto-close.
      submitting = false
      onCreated?.()
      close()
    } catch (err) {
      errorMessage = (err as Error)?.message ?? String(err)
    } finally {
      submitting = false
    }
  }

  function close() {
    if (submitting) return
    open = false
    onClose?.()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>{$_('calendar.localCalendar.title')}</Dialog.Title>
      <Dialog.Description>{$_('calendar.localCalendar.description')}</Dialog.Description>
    </Dialog.Header>

    <div class="space-y-3 mt-2">
      <div>
        <Label for="cal-local-name">{$_('calendar.localCalendar.nameLabel')}</Label>
        <Input
          id="cal-local-name"
          type="text"
          placeholder={$_('calendar.localCalendar.namePlaceholder')}
          bind:value={displayName}
          disabled={submitting}
        />
      </div>

      <div class="flex items-center gap-3">
        <Label>{$_('calendar.localCalendar.colorLabel')}</Label>
        <ColorPicker value={color} onchange={(hex) => { color = hex }} />
        <span class="text-xs text-muted-foreground">
          {color || $_('calendar.localCalendar.colorAuto')}
        </span>
      </div>

      <AddCalendarDefaultsControl
        mode="single"
        sourceId={calendarSources.sources.find(s => s.type === 'local')?.id ?? ''}
        providerLabel={$_('calendar.add.providerLabelLocal')}
        candidates={[{ tempId: NEW_TEMP_ID, displayName: displayName || $_('calendar.localCalendar.title'), writable: true }]}
        bind:providerDefaultTempId
        bind:globalDefaultRef
      />

      {#if errorMessage}
        <div class="flex items-start gap-2 p-2 bg-destructive/10 rounded text-sm">
          <Icon icon="mdi:alert-circle" class="w-4 h-4 text-destructive shrink-0 mt-0.5" />
          <div class="text-xs text-destructive break-words">{errorMessage}</div>
        </div>
      {/if}
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={close} disabled={submitting}>
        {$_('calendar.common.cancel')}
      </Button>
      <Button onclick={handleSave} disabled={submitting}>
        {#if submitting}<Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />{/if}
        {$_('calendar.common.save')}
      </Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
