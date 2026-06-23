<script lang="ts">
  // SendInvitationsDialog — confirmation modal shown on Edit-save when an
  // event has attendees. Matches Outlook's "Send update?" pattern: tiny
  // tweaks (typo in description, color change) shouldn't spam everyone
  // with a MEETING UPDATED email. Two choices only: Send or Don't send.
  //
  // NOT shown on Create — adding attendees on a new event implicitly
  // means you want them notified (matches the user's intuition that
  // "why would you add invitees and not send the invitation?").

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'

  type SourceKind = 'google' | 'microsoft' | 'caldav-server' | 'caldav-none' | 'local' | ''

  interface Props {
    open: boolean
    attendeeCount: number
    sourceKind?: SourceKind
    /** Confirm callback: 'all' or 'none'. Composer threads it into EventInput
     *  as the sendUpdates value before the actual save fires. */
    onConfirm?: (sendUpdates: string) => void
    onCancel?: () => void
  }

  let {
    open = $bindable(false),
    attendeeCount,
    sourceKind = '',
    onConfirm,
    onCancel,
  }: Props = $props()

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  // Provider note: surface real-world limitations so users don't expect
  // behavior that can't happen.
  const providerNote = $derived.by(() => {
    switch (sourceKind) {
      case 'microsoft':
        return $_('calendar.attendees.dialogNoteMicrosoft')
      case 'caldav-none':
        return $_('calendar.attendees.dialogNoteCalDAVNone')
      default:
        return ''
    }
  })

  function pickSend() {
    onConfirm?.('all')
    open = false
  }

  function pickDontSend() {
    onConfirm?.('none')
    open = false
  }

  function cancel() {
    onCancel?.()
    open = false
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) cancel() }}>
  <Dialog.Content class="max-w-md">
    <Dialog.Header>
      <Dialog.Title>{$_('calendar.attendees.dialogTitle')}</Dialog.Title>
      <Dialog.Description>
        {$_('calendar.attendees.dialogDescription', { values: { count: attendeeCount } })}
      </Dialog.Description>
    </Dialog.Header>

    {#if providerNote}
      <p class="mt-2 rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">{providerNote}</p>
    {/if}

    <div class="mt-4 flex flex-col-reverse gap-2 border-t border-border pt-4 sm:flex-row sm:items-center sm:justify-end">
      <Button variant="ghost" onclick={cancel}>{$_('calendar.attendees.dialogCancel')}</Button>
      <Button variant="outline" onclick={pickDontSend}>{$_('calendar.attendees.dialogDontSend')}</Button>
      <Button onclick={pickSend}>{$_('calendar.attendees.dialogSend')}</Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
