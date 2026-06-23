<script lang="ts">
  // RecurrenceScopeDialog — shown before editing or deleting a recurring
  // event. User picks: just this instance / this and future / all in series.
  // Calls onPicked with the scope string. Caller then opens the composer
  // (for edit) or fires DeleteEvent (for delete).

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'

  type Scope = 'this' | 'this-and-future' | 'all'

  interface Props {
    open: boolean
    /** 'edit' or 'delete' — affects the question text only. */
    action?: 'edit' | 'delete'
    onPicked?: (scope: Scope) => void
    onClose?: () => void
  }

  let {
    open = $bindable(false),
    action = 'edit',
    onPicked,
    onClose,
  }: Props = $props()

  $effect(() => {
    if (!open) return
    dialogGuardOpen()
    return () => dialogGuardClose()
  })

  function pick(scope: Scope) {
    onPicked?.(scope)
    open = false
  }

  function close() {
    open = false
    onClose?.()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>
        {action === 'delete'
          ? $_('calendar.recurrenceScope.titleDelete')
          : $_('calendar.recurrenceScope.titleEdit')}
      </Dialog.Title>
      <Dialog.Description>
        {$_('calendar.recurrenceScope.description')}
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-2 mt-3">
      <Button variant="outline" class="justify-start" onclick={() => pick('this')}>
        {$_('calendar.recurrenceScope.this')}
      </Button>
      <Button variant="outline" class="justify-start" onclick={() => pick('this-and-future')}>
        {$_('calendar.recurrenceScope.thisAndFuture')}
      </Button>
      <Button variant="outline" class="justify-start" onclick={() => pick('all')}>
        {$_('calendar.recurrenceScope.all')}
      </Button>
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={close}>
        {$_('calendar.common.cancel')}
      </Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
