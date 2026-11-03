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
  } from '../../../../wailsjs/go/app/App'
  import { toasts } from '$lib/stores/toast'
  import type { Snippet } from 'svelte'
  import { _ } from '$lib/i18n'
  import MarkAllScopeDialog from './MarkAllScopeDialog.svelte'

  interface Props {
    folderId: string
    children?: Snippet
  }

  let {
    folderId,
    children,
  }: Props = $props()

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
  </ContextMenuContent>
</ContextMenuPrimitive.Root>
