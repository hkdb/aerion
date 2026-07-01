<script lang="ts">
  import Icon from '@iconify/svelte'
  // @ts-ignore - wailsjs path
  import { folder } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { MoveToFolder, Undo } from '../../../../wailsjs/go/app/App.js'
  import FolderContextMenu from './FolderContextMenu.svelte'
  import Self from './FolderTreeItem.svelte'
  import { toasts } from '$lib/stores/toast'
  import { _ } from '$lib/i18n'

  interface Props {
    tree: folder.FolderTree
    accountId: string
    selectedFolderId: string
    selectionSource: 'unified' | 'account' | null
    collapsedFolders: Record<string, boolean>
    onFolderSelect?: (f: folder.Folder) => void
    onToggleCollapse?: (folderId: string) => void
    onMessagesMoved?: () => void
  }

  let {
    tree,
    accountId,
    selectedFolderId,
    selectionSource,
    collapsedFolders,
    onFolderSelect,
    onToggleCollapse,
    onMessagesMoved,
  }: Props = $props()

  // Folder type to icon mapping
  const folderIcons: Record<string, string> = {
    inbox: 'mdi:inbox',
    sent: 'mdi:send',
    drafts: 'mdi:file-document-edit-outline',
    trash: 'mdi:delete-outline',
    archive: 'mdi:archive-outline',
    spam: 'mdi:alert-octagon-outline',
    all: 'mdi:email-multiple-outline',
    folder: 'mdi:folder-outline',
  }

  function getFolderIcon(type: string): string {
    return folderIcons[type] || folderIcons.folder
  }

  function isFolderSelected(folderId: string): boolean {
    return selectionSource === 'account' && selectedFolderId === folderId
  }

  let hasChildren = $derived(tree.children && tree.children.length > 0)
  let isCollapsed = $derived(
    hasChildren
      ? collapsedFolders[tree.folder!.id] !== false  // collapsed unless explicitly set to false
      : false
  )

  // Drag-and-drop state for receiving message drops on this folder
  let isDragOver = $state(false)

  function hasMessagesPayload(e: DragEvent): boolean {
    return !!e.dataTransfer?.types.includes('application/x-aerion-messages')
  }

  function handleDragEnter(e: DragEvent) {
    if (!hasMessagesPayload(e)) return
    e.preventDefault()
    isDragOver = true
  }

  function handleDragOver(e: DragEvent) {
    if (!hasMessagesPayload(e)) return
    e.preventDefault()  // required to allow drop
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  }

  function handleDragLeave() {
    isDragOver = false
  }

  async function handleDrop(e: DragEvent) {
    isDragOver = false
    const raw = e.dataTransfer?.getData('application/x-aerion-messages')
    if (!raw || !tree.folder) return
    e.preventDefault()

    let payload: { messageIds: string[]; sourceAccountId: string }
    try {
      payload = JSON.parse(raw)
    } catch {
      return
    }
    if (!payload.messageIds || payload.messageIds.length === 0) return

    // Same-folder drop: no-op
    if (tree.folder.id === selectedFolderId && selectionSource === 'account') {
      return
    }

    const folderName = tree.folder.name
    try {
      await MoveToFolder(payload.messageIds, tree.folder.id)
      onMessagesMoved?.()
      toasts.success($_('toast.movedTo', { values: { folder: folderName } }), [
        { label: $_('common.undo'), onClick: handleUndo },
      ])
    } catch (err) {
      console.error('Drag-drop move failed:', err)
      toasts.error($_('toast.failedToMove'))
    }
  }

  async function handleUndo() {
    try {
      await Undo()
    } catch (err) {
      console.error('Undo failed:', err)
      toasts.error($_('toast.undoFailed'))
    }
  }
</script>

{#if tree.folder}
  <FolderContextMenu folderId={tree.folder.id}>
    <button
      class="w-full flex items-center gap-2 px-3 py-1.5 text-sm rounded-md transition-colors {isFolderSelected(tree.folder.id)
        ? 'bg-primary/10 text-primary font-medium'
        : 'text-foreground hover:bg-muted/50'} {isDragOver ? 'ring-2 ring-primary ring-inset' : ''}"
      data-sidebar-item="folder"
      data-folder-id={tree.folder.id}
      data-has-children={hasChildren ? 'true' : undefined}
      onclick={() => onFolderSelect?.(tree.folder!)}
      ondragenter={handleDragEnter}
      ondragover={handleDragOver}
      ondragleave={handleDragLeave}
      ondrop={handleDrop}
    >
      <Icon
        icon={getFolderIcon(tree.folder.type)}
        class="w-4 h-4 flex-shrink-0"
      />
      <span class="truncate text-left">{tree.folder.name}</span>
      {#if hasChildren}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <span
          class="flex-shrink-0 p-0.5 rounded hover:bg-muted"
          role="button"
          tabindex="-1"
          onclick={(e: MouseEvent) => {
            e.stopPropagation()
            onToggleCollapse?.(tree.folder!.id)
          }}
        >
          <Icon
            icon={isCollapsed ? 'mdi:chevron-right' : 'mdi:chevron-down'}
            class="w-4 h-4 text-muted-foreground"
          />
        </span>
      {/if}
      <span class="flex-1"></span>
      {#if tree.folder.unreadCount > 0}
        <span
          class="px-1.5 py-0.5 text-xs font-medium rounded-full bg-muted text-muted-foreground"
        >
          {tree.folder.unreadCount}
        </span>
      {/if}
    </button>
  </FolderContextMenu>

  {#if hasChildren && !isCollapsed}
    <div class="ml-4">
      {#each tree.children as childTree (childTree.folder?.id ?? 'unknown')}
        <Self
          tree={childTree}
          {accountId}
          {selectedFolderId}
          {selectionSource}
          {collapsedFolders}
          {onFolderSelect}
          {onToggleCollapse}
          {onMessagesMoved}
        />
      {/each}
    </div>
  {/if}
{/if}
