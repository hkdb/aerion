<script lang="ts">
  import * as AlertDialog from '$lib/components/ui/alert-dialog'
  import { _ } from '$lib/i18n'

  interface Props {
    /** Whether the dialog is open (bindable) */
    open?: boolean
    /** true = mark as read, false = mark as unread */
    seen: boolean
    /** Messages beyond the sync period the server-wide action would reach */
    count: number
    /** Yes: mark the whole folder on the server */
    onAll: () => void
    /** No: mark only the messages within the sync period */
    onSynced: () => void
  }

  let {
    open = $bindable(false),
    seen,
    count,
    onAll,
    onSynced,
  }: Props = $props()

  function choose(action: () => void) {
    open = false
    action()
  }
</script>

<!-- Three-way scope prompt for "Mark all as read/unread" (#458): Yes = whole
     folder on the server, No = sync-period rows only, Cancel = nothing. -->
<AlertDialog.Root bind:open>
  <AlertDialog.Content preventCloseAutoFocus>
    <AlertDialog.Header>
      <AlertDialog.Title>
        {seen ? $_('contextMenu.markAllScopeTitleRead') : $_('contextMenu.markAllScopeTitleUnread')}
      </AlertDialog.Title>
      <AlertDialog.Description>
        {$_('contextMenu.markAllScopeBody', { values: { count } })}
      </AlertDialog.Description>
    </AlertDialog.Header>
    <AlertDialog.Footer>
      <AlertDialog.Cancel onclick={() => (open = false)}>
        {$_('common.cancel')}
      </AlertDialog.Cancel>
      <AlertDialog.Action
        onclick={() => choose(onSynced)}
        class="bg-secondary text-secondary-foreground hover:bg-secondary/80"
      >
        {$_('contextMenu.markAllScopeSynced')}
      </AlertDialog.Action>
      <AlertDialog.Action onclick={() => choose(onAll)}>
        {$_('contextMenu.markAllScopeAll')}
      </AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
