<script lang="ts">
  import Icon from '@iconify/svelte'
  import { ContextMenu as ContextMenuPrimitive } from 'bits-ui'
  import {
    ContextMenuContent,
    ContextMenuItem,
  } from '$lib/components/ui/context-menu'
  import {
    MarkAllFolderMessagesAsRead,
    MarkAllFolderMessagesAsUnread,
    MarkWholeFolder,
    GetMarkAllCoverage,
    EmptyTrash,
    EmptySpam,
  } from '../../../../wailsjs/go/app/App'
  import { toasts } from '$lib/stores/toast'
  import type { Snippet } from 'svelte'
  import { _ } from '$lib/i18n'
  import MarkAllScopeDialog from './MarkAllScopeDialog.svelte'
  import { ConfirmDialog } from '$lib/components/ui/confirm-dialog'

  interface Props {
    folderId: string
    /** Account owning the folder (needed for "Empty trash/spam") */
    accountId?: string
    /** Folder type; "Empty trash"/"Empty spam" are offered only for 'trash'/'spam' */
    folderType?: string
    /** Called after the trash/spam folder was emptied (e.g. to reload the message list) */
    onEmptied?: () => void
    children?: Snippet
  }

  let {
    folderId,
    accountId,
    folderType,
    onEmptied,
    children,
  }: Props = $props()

  // Empty-trash confirmation, same flow as the button in the message list
  let showEmptyTrashConfirm = $state(false)

  async function handleEmptyTrash() {
    if (!accountId) return
    try {
      await EmptyTrash(accountId, folderId)
      toasts.success($_('toast.trashEmptied'))
      onEmptied?.()
    } catch (err) {
      console.error('Empty trash failed:', err)
      toasts.error($_('toast.failedToEmptyTrash'))
    }
    showEmptyTrashConfirm = false
  }

  // Empty-spam confirmation, same flow as the button in the message list
  let showEmptySpamConfirm = $state(false)

  async function handleEmptySpam() {
    if (!accountId) return
    try {
      await EmptySpam(accountId, folderId)
      toasts.success($_('toast.spamEmptied'))
      onEmptied?.()
    } catch (err) {
      console.error('Empty spam failed:', err)
      toasts.error($_('toast.failedToEmptySpam'))
    }
    showEmptySpamConfirm = false
  }

  // Whole-folder scope prompt (#458): shown only when the server holds mail
  // beyond the sync period that the local-only action would not reach.
  let scopeOpen = $state(false)
  let scopeSeen = $state(true)
  let scopeCount = $state(0)

  // Read-flag changes are not on the undo stack (#243), so no Undo action on
  // these toasts — it used to undo whatever unrelated action was on top.
  async function runLocal(seen: boolean) {
    try {
      if (seen) {
        await MarkAllFolderMessagesAsRead(folderId)
      }
      if (!seen) {
        await MarkAllFolderMessagesAsUnread(folderId)
      }
      toasts.success(seen ? $_('toast.markedAllAsRead') : $_('toast.markedAllAsUnread'))
    } catch (err) {
      console.error('Mark all failed:', err)
      toasts.error(seen ? $_('toast.failedToMarkAllAsRead') : $_('toast.failedToMarkAllAsUnread'))
    }
  }

  async function runWhole(seen: boolean) {
    try {
      await MarkWholeFolder(folderId, seen)
      toasts.success(seen ? $_('toast.markedAllAsRead') : $_('toast.markedAllAsUnread'))
    } catch (err) {
      console.error('Mark whole folder failed:', err)
      toasts.error(seen ? $_('toast.failedToMarkAllAsRead') : $_('toast.failedToMarkAllAsUnread'))
    }
  }

  async function startMarkAll(seen: boolean) {
    let extra = 0
    try {
      extra = await GetMarkAllCoverage(folderId, seen)
    } catch (err) {
      console.error('Mark all coverage check failed:', err)
    }
    if (extra <= 0) {
      await runLocal(seen)
      return
    }
    scopeSeen = seen
    scopeCount = extra
    scopeOpen = true
  }

  function handleMarkAllRead() {
    void startMarkAll(true)
  }

  function handleMarkAllUnread() {
    void startMarkAll(false)
  }
</script>

<ConfirmDialog
  bind:open={showEmptyTrashConfirm}
  title={$_('dialog.emptyTrash')}
  description={$_('dialog.emptyTrashDescription')}
  confirmLabel={$_('dialog.confirmEmptyTrash')}
  variant="destructive"
  onConfirm={handleEmptyTrash}
  onCancel={() => { showEmptyTrashConfirm = false }}
/>

<ConfirmDialog
  bind:open={showEmptySpamConfirm}
  title={$_('dialog.emptySpam')}
  description={$_('dialog.emptySpamDescription')}
  confirmLabel={$_('dialog.confirmEmptySpam')}
  variant="destructive"
  onConfirm={handleEmptySpam}
  onCancel={() => { showEmptySpamConfirm = false }}
/>

<MarkAllScopeDialog
  bind:open={scopeOpen}
  seen={scopeSeen}
  count={scopeCount}
  onAll={() => runWhole(scopeSeen)}
  onSynced={() => runLocal(scopeSeen)}
/>

<ContextMenuPrimitive.Root>
  <ContextMenuPrimitive.Trigger>
    {#if children}
      {@render children()}
    {/if}
  </ContextMenuPrimitive.Trigger>

  <ContextMenuContent>
    <ContextMenuItem onSelect={handleMarkAllRead}>
      <Icon icon="mdi:email-check-outline" class="mr-2 h-4 w-4" />
      {$_('contextMenu.markAllAsRead')}
    </ContextMenuItem>
    <ContextMenuItem onSelect={handleMarkAllUnread}>
      <Icon icon="mdi:email-outline" class="mr-2 h-4 w-4" />
      {$_('contextMenu.markAllAsUnread')}
    </ContextMenuItem>
    {#if folderType === 'trash' && accountId}
      <ContextMenuItem onSelect={() => { showEmptyTrashConfirm = true }}>
        <Icon icon="mdi:delete-sweep-outline" class="mr-2 h-4 w-4" />
        {$_('messageList.emptyTrash')}
      </ContextMenuItem>
    {/if}
    {#if folderType === 'spam' && accountId}
      <ContextMenuItem onSelect={() => { showEmptySpamConfirm = true }}>
        <Icon icon="mdi:delete-sweep-outline" class="mr-2 h-4 w-4" />
        {$_('messageList.emptySpam')}
      </ContextMenuItem>
    {/if}
  </ContextMenuContent>
</ContextMenuPrimitive.Root>