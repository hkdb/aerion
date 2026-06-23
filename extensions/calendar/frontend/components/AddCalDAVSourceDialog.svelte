<script lang="ts">
  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import Icon from '@iconify/svelte'
  import { toasts } from '$lib/stores/toast'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import CalendarColorPickStage from './CalendarColorPickStage.svelte'
  import { applyDefaultsAfterAdd } from '$extensions/calendar/frontend/lib/defaultsApply'

  interface Props {
    open: boolean
    onClose?: () => void
  }

  let { open = $bindable(false), onClose }: Props = $props()

  let providerDefaultTempId = $state('')
  let globalDefaultRef = $state('')

  // Two-stage flow: 'form' (connection inputs) → 'colors' (per-calendar
  // pickers, post-persist). Backend writes already happened by the time we
  // enter 'colors'; per-row picker changes call Calendar_SetCalendarColor
  // immediately. Closing during 'colors' just keeps whatever was picked so
  // far (HSL fallback covers any calendars still at NULL color).
  let stage = $state<'form' | 'colors'>('form')
  let newSourceID = $state('')

  let nameInput = $state('')
  let urlInput = $state('')
  let usernameInput = $state('')
  let passwordInput = $state('')
  // organizerEmailInput stays hidden until the backend's first attempt
  // returns the ErrCalDAVOrganizerEmailRequired sentinel (server didn't
  // publish a calendar user address via PROPFIND). The field then
  // appears with a prompt; the user fills it and Save retries.
  let organizerEmailInput = $state('')
  let needsOrganizerEmail = $state(false)
  let submitting = $state(false)
  let lastError = $state('')

  $effect(() => {
    if (!open) return
    // Reset form + stage each time the dialog opens.
    stage = 'form'
    newSourceID = ''
    nameInput = ''
    urlInput = ''
    usernameInput = ''
    passwordInput = ''
    organizerEmailInput = ''
    needsOrganizerEmail = false
    lastError = ''
    submitting = false
    providerDefaultTempId = ''
    globalDefaultRef = ''
  })

  // Minimal email shape check so Save is disabled until the user types
  // something plausible. The backend re-validates server-side.
  const emailLooksValid = $derived(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(organizerEmailInput.trim()))

  // Match the backend sentinel error by stable substring. Lifted from
  // ErrCalDAVOrganizerEmailRequired's message: "calendar: organizer
  // email required". Robust to wrapping (errors.Errorf etc.).
  function isOrganizerEmailRequired(msg: string): boolean {
    return msg.toLowerCase().includes('organizer email required')
  }

  // Calendars discovered for the new source — populated after Stage 1 by
  // calendarSources.addCalDAVSource → load(). Reactive on the store.
  const discoveredCalendars = $derived.by(() => {
    if (stage !== 'colors' || newSourceID === '') return []
    return calendarSources.calendarsBySource[newSourceID] ?? []
  })

  // Register with the host's dialogGuard while open. Without this, mail's
  // global Enter/Space handler in App.svelte calls e.preventDefault() on
  // the dialog buttons. Same pattern AddContactDialog uses.
  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  function close() {
    if (submitting) return
    finalizeDefaults()
    open = false
    onClose?.()
  }

  // Apply the user's provider-default / global-default picks. The dialog
  // calls this on close() — the colors stage doesn't have its own commit
  // button beyond "Done" (which routes here too). The added calendars use
  // their real backend IDs as both id and tempId.
  function finalizeDefaults() {
    if (newSourceID === '') return
    const added = discoveredCalendars.map(c => ({
      id: c.id,
      tempId: c.id,
      writable: c.writable !== false,
    }))
    if (added.length === 0) return
    applyDefaultsAfterAdd({
      sourceId: newSourceID,
      added,
      providerDefaultTempId,
      globalDefaultRef,
    })
  }

  function validate(): boolean {
    lastError = ''
    if (nameInput.trim() === '') {
      lastError = $_('calendar.add.fieldRequired', { values: { field: $_('calendar.add.nameLabel') } })
      return false
    }
    if (urlInput.trim() === '') {
      lastError = $_('calendar.add.fieldRequired', { values: { field: $_('calendar.add.urlLabel') } })
      return false
    }
    if (usernameInput.trim() === '') {
      lastError = $_('calendar.add.fieldRequired', { values: { field: $_('calendar.add.usernameLabel') } })
      return false
    }
    if (passwordInput === '') {
      lastError = $_('calendar.add.fieldRequired', { values: { field: $_('calendar.add.passwordLabel') } })
      return false
    }
    return true
  }

  async function submit() {
    if (!validate()) return
    if (needsOrganizerEmail && !emailLooksValid) {
      lastError = $_('calendar.add.organizerEmailInvalid')
      return
    }
    submitting = true
    lastError = ''
    try {
      const sourceID = await calendarSources.addCalDAVSource(
        nameInput.trim(),
        urlInput.trim(),
        usernameInput.trim(),
        passwordInput,
        organizerEmailInput.trim(),
      )
      const count = calendarSources.calendarsBySource[sourceID]?.length ?? 0
      toasts.success(
        $_('calendar.add.successToast', { values: { count, name: nameInput.trim() } }),
      )
      // Stay open; transition to Stage 2 so the user can pick per-calendar
      // colors. They can dismiss at any point — HSL fallback covers calendars
      // they didn't customize.
      newSourceID = sourceID
      stage = 'colors'
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      console.error('Add CalDAV source failed:', err)
      // Backend signals "server didn't publish a calendar user address
      // — give me an organizer email". Reveal the field instead of
      // bouncing the user out with a generic error.
      if (isOrganizerEmailRequired(msg)) {
        needsOrganizerEmail = true
        lastError = $_('calendar.add.organizerEmailPrompt')
        return
      }
      lastError = msg
    } finally {
      submitting = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key !== 'Enter' || submitting || stage !== 'form') return
    e.preventDefault()
    submit()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-md">
    {#if stage === 'form'}
      <Dialog.Header>
        <Dialog.Title>{$_('calendar.add.title')}</Dialog.Title>
        <Dialog.Description>
          {$_('calendar.add.description')}
        </Dialog.Description>
      </Dialog.Header>

      <div class="space-y-3 mt-2">
        <div>
          <Label for="cal-add-name">{$_('calendar.add.nameLabel')}</Label>
          <Input
            id="cal-add-name"
            type="text"
            placeholder={$_('calendar.add.namePlaceholder')}
            bind:value={nameInput}
            disabled={submitting}
            onkeydown={onKeydown}
          />
        </div>
        <div>
          <Label for="cal-add-url">{$_('calendar.add.urlLabel')}</Label>
          <Input
            id="cal-add-url"
            type="text"
            placeholder={$_('calendar.add.urlPlaceholder')}
            bind:value={urlInput}
            disabled={submitting}
            onkeydown={onKeydown}
          />
          <p class="text-xs text-muted-foreground mt-1">
            {$_('calendar.add.urlHelp')}
          </p>
        </div>
        <div>
          <Label for="cal-add-username">{$_('calendar.add.usernameLabel')}</Label>
          <Input
            id="cal-add-username"
            type="text"
            bind:value={usernameInput}
            disabled={submitting}
            onkeydown={onKeydown}
          />
        </div>
        <div>
          <Label for="cal-add-password">{$_('calendar.add.passwordLabel')}</Label>
          <Input
            id="cal-add-password"
            type="password"
            bind:value={passwordInput}
            disabled={submitting}
            onkeydown={onKeydown}
          />
        </div>

        {#if needsOrganizerEmail}
          <div>
            <Label for="cal-add-organizer">{$_('calendar.add.organizerEmailLabel')}</Label>
            <Input
              id="cal-add-organizer"
              type="email"
              placeholder={$_('calendar.add.organizerEmailPlaceholder')}
              bind:value={organizerEmailInput}
              disabled={submitting}
              onkeydown={onKeydown}
            />
            <p class="text-xs text-muted-foreground mt-1">
              {$_('calendar.add.organizerEmailHelp')}
            </p>
          </div>
        {/if}

        {#if lastError !== ''}
          <div class="flex items-start gap-2 p-2 bg-destructive/10 rounded text-sm min-w-0">
            <Icon icon="mdi:alert-circle" class="w-4 h-4 text-destructive shrink-0 mt-0.5" />
            <div class="flex-1 min-w-0">
              <div class="text-destructive font-medium">{$_('calendar.add.errorTitle')}</div>
              <div class="text-xs text-muted-foreground break-all max-h-24 overflow-y-auto">{lastError}</div>
              <div class="text-xs text-muted-foreground mt-1">{$_('calendar.add.errorHelp')}</div>
            </div>
          </div>
        {/if}
      </div>

      <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
        <Button variant="ghost" onclick={close} disabled={submitting}>
          {$_('calendar.common.cancel')}
        </Button>
        <Button onclick={submit} disabled={submitting}>
          {#if submitting}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
            {$_('calendar.add.submitting')}
          {:else}
            {$_('calendar.add.submit')}
          {/if}
        </Button>
      </div>
    {:else}
      <CalendarColorPickStage
        sourceId={newSourceID}
        providerLabel={nameInput}
        discoveredCalendars={discoveredCalendars}
        bind:providerDefaultTempId
        bind:globalDefaultRef
        onDone={close}
      />
    {/if}
  </Dialog.Content>
</Dialog.Root>
