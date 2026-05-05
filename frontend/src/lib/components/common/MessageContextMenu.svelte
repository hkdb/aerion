<script lang="ts">
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import {
    ContextMenuContent,
    ContextMenuItem,
    ContextMenuSeparator
  } from "$lib/components/ui/context-menu";
  import { _ } from "$lib/i18n";
  import { dialogGuardClose, dialogGuardOpen } from "$lib/stores/dialogGuard";
  import { toasts } from "$lib/stores/toast";
  import Icon from "@iconify/svelte";
  import { ContextMenu as ContextMenuPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  import {
    Archive,
    CopyToFolder,
    DeletePermanently,
    GetFolders,
    MarkAsNotSpam,
    MarkAsRead,
    MarkAsSpam,
    MarkAsUnread,
    MoveToFolder,
    Star,
    Trash,
    Undo,
    Unstar
  } from "../../../../wailsjs/go/app/App";
  // @ts-ignore - wailsjs path
  import { folder } from "../../../../wailsjs/go/models";
  import FolderPickerDialog from "./FolderPickerDialog.svelte";

  interface Props {
    messageIds: string[];
    accountId: string;
    currentFolderId: string;
    folderType: string;
    isStarred: boolean;
    isRead: boolean;
    onActionComplete?: (autoSelectNext?: boolean) => void;
    onReply?: (
      mode: "reply" | "reply-all" | "forward",
      messageId: string
    ) => void;
    onOpenChange?: (open: boolean) => void;
    children?: Snippet;
  }

  let {
    messageIds,
    accountId,
    currentFolderId,
    folderType,
    isStarred,
    isRead,
    onActionComplete,
    onReply,
    onOpenChange,
    children
  }: Props = $props();

  // Folders state for move/copy submenus
  let folders = $state<folder.Folder[]>([]);
  let foldersLoading = $state(false);
  let foldersLoaded = $state(false);

  // Permanent delete confirmation
  let showDeleteConfirm = $state(false);

  // Folder picker dialog state
  let showFolderPicker = $state(false);
  let folderPickerMode = $state<"move" | "copy">("move");

  // Track dialog open/close to prevent background reloads from dismissing dialogs
  $effect(() => {
    if (showFolderPicker) {
      dialogGuardOpen();
      return () => dialogGuardClose();
    }
  });

  $effect(() => {
    if (showDeleteConfirm) {
      dialogGuardOpen();
      return () => dialogGuardClose();
    }
  });

  // Folder type to icon mapping
  const folderIcons: Record<string, string> = {
    inbox: "mdi:inbox",
    sent: "mdi:send",
    drafts: "mdi:file-document-edit-outline",
    trash: "mdi:delete-outline",
    archive: "mdi:archive-outline",
    spam: "mdi:alert-octagon-outline",
    all: "mdi:email-multiple-outline",
    folder: "mdi:folder-outline"
  };

  function getFolderIcon(type: string): string {
    return folderIcons[type] || folderIcons.folder;
  }

  // Computed values
  const isTrashFolder = $derived(folderType === "trash");
  const isSpamFolder = $derived(folderType === "spam");
  const isSingleMessage = $derived(messageIds.length === 1);

  // Load folders when context menu opens
  async function loadFolders() {
    if (foldersLoaded || foldersLoading) return;

    foldersLoading = true;
    try {
      const result = await GetFolders(accountId);
      folders = result || [];
      foldersLoaded = true;
    } catch (err) {
      console.error("Failed to load folders:", err);
    } finally {
      foldersLoading = false;
    }
  }

  // Handle menu open
  function handleOpenChange(open: boolean) {
    if (open) {
      loadFolders();
    }
    onOpenChange?.(open);
  }

  // Get folders excluding current folder (for move/copy)
  const availableFolders = $derived(
    folders.filter((f) => f.id !== currentFolderId)
  );

  // Group folders: special folders first, then custom folders
  const specialFolderTypes = [
    "inbox",
    "sent",
    "drafts",
    "archive",
    "trash",
    "spam",
    "all"
  ];
  const specialFolders = $derived(
    availableFolders.filter((f) => specialFolderTypes.includes(f.type))
  );
  const customFolders = $derived(
    availableFolders.filter((f) => !specialFolderTypes.includes(f.type))
  );

  // Undo handler
  async function handleUndo() {
    try {
      const description = await Undo();
      toasts.success($_("toast.undone", { values: { description } }));
    } catch (err) {
      console.error("Undo failed:", err);
      toasts.error($_("toast.undoFailed"));
    }
  }

  // Action handlers
  async function handleReply() {
    if (isSingleMessage && onReply) {
      onReply("reply", messageIds[0]);
    }
  }

  async function handleReplyAll() {
    if (isSingleMessage && onReply) {
      onReply("reply-all", messageIds[0]);
    }
  }

  async function handleForward() {
    if (isSingleMessage && onReply) {
      onReply("forward", messageIds[0]);
    }
  }

  async function handleArchive() {
    try {
      await Archive(messageIds);
      toasts.success($_("toast.archived"), [
        { label: $_("common.undo"), onClick: handleUndo }
      ]);
      onActionComplete?.(true);
    } catch (err) {
      console.error("Archive failed:", err);
      toasts.error($_("toast.failedToArchive"));
    }
  }

  async function handleDelete() {
    if (isTrashFolder) {
      showDeleteConfirm = true;
    } else {
      try {
        const movedToTrash = await Trash(messageIds);
        const toastMsg = movedToTrash
          ? $_("toast.movedToTrash")
          : $_("toast.deletedFromFolder");
        const actions = movedToTrash
          ? [{ label: $_("common.undo"), onClick: handleUndo }]
          : [];
        toasts.success(toastMsg, actions);
        onActionComplete?.(true);
      } catch (err) {
        console.error("Delete failed:", err);
        toasts.error($_("toast.failedToDelete"));
      }
    }
  }

  async function handleConfirmPermanentDelete() {
    try {
      await DeletePermanently(messageIds);
      toasts.success($_("toast.permanentlyDeleted"));
      showDeleteConfirm = false;
      onActionComplete?.(true);
    } catch (err) {
      console.error("Permanent delete failed:", err);
      toasts.error($_("toast.failedToDelete"));
      showDeleteConfirm = false;
    }
  }

  async function handleSpam() {
    try {
      if (isSpamFolder) {
        // If we're in spam folder, mark as NOT spam
        await MarkAsNotSpam(messageIds);
        toasts.success($_("toast.markedAsNotSpam"), [
          { label: $_("common.undo"), onClick: handleUndo }
        ]);
        onActionComplete?.(true);
        return;
      }
      // Otherwise, mark as spam
      const movedToSpam = await MarkAsSpam(messageIds);
      const toastMsg = movedToSpam
        ? $_("toast.markedAsSpam")
        : $_("toast.deletedFromFolder");
      const actions = movedToSpam
        ? [{ label: $_("common.undo"), onClick: handleUndo }]
        : [];
      toasts.success(toastMsg, actions);
      onActionComplete?.(true);
    } catch (err) {
      console.error("Spam toggle failed:", err);
      toasts.error(
        $_(
          isSpamFolder
            ? "toast.failedToMarkAsNotSpam"
            : "toast.failedToMarkAsSpam"
        )
      );
    }
  }

  async function handleToggleStar() {
    try {
      if (isStarred) {
        await Unstar(messageIds);
        toasts.success($_("toast.starRemoved"));
      } else {
        await Star(messageIds);
        toasts.success($_("toast.starred"));
      }
      onActionComplete?.();
    } catch (err) {
      console.error("Star toggle failed:", err);
      toasts.error($_("toast.failedToUpdateStar"));
    }
  }

  async function handleToggleRead() {
    try {
      if (isRead) {
        await MarkAsUnread(messageIds);
        toasts.success($_("toast.markedAsUnread"));
      } else {
        await MarkAsRead(messageIds);
        toasts.success($_("toast.markedAsRead"));
      }
      onActionComplete?.();
    } catch (err) {
      console.error("Read status toggle failed:", err);
      toasts.error($_("toast.failedToUpdateReadStatus"));
    }
  }

  function openMoveTo() {
    folderPickerMode = "move";
    showFolderPicker = true;
  }

  function openCopyTo() {
    folderPickerMode = "copy";
    showFolderPicker = true;
  }

  function handleFolderSelected(folderId: string, folderName: string) {
    showFolderPicker = false;
    switch (folderPickerMode) {
      case "move":
        handleMoveTo(folderId, folderName);
        break;
      case "copy":
        handleCopyTo(folderId, folderName);
        break;
    }
  }

  async function handleMoveTo(destFolderId: string, folderName: string) {
    try {
      await MoveToFolder(messageIds, destFolderId);
      toasts.success($_("toast.movedTo", { values: { folder: folderName } }), [
        { label: $_("common.undo"), onClick: handleUndo }
      ]);
      onActionComplete?.(true);
    } catch (err) {
      console.error("Move failed:", err);
      toasts.error($_("toast.failedToMove"));
    }
  }

  async function handleCopyTo(destFolderId: string, folderName: string) {
    try {
      await CopyToFolder(messageIds, destFolderId);
      toasts.success($_("toast.copyingTo", { values: { folder: folderName } }));
      // Note: CopyToFolder syncs in background and emits messages:copied event
    } catch (err) {
      console.error("Copy failed:", err);
      toasts.error($_("toast.failedToCopy"));
    }
  }
</script>

<ContextMenuPrimitive.Root onOpenChange={handleOpenChange}>
  <ContextMenuPrimitive.Trigger>
    {#if children}
      {@render children()}
    {/if}
  </ContextMenuPrimitive.Trigger>

  <ContextMenuContent>
    <!-- Reply actions (single message only) -->
    {#if isSingleMessage}
      <ContextMenuItem onSelect={handleReply}>
        <Icon icon="mdi:reply" class="mr-2 h-4 w-4" />
        {$_("contextMenu.reply")}
      </ContextMenuItem>
      <ContextMenuItem onSelect={handleReplyAll}>
        <Icon icon="mdi:reply-all" class="mr-2 h-4 w-4" />
        {$_("contextMenu.replyAll")}
      </ContextMenuItem>
      <ContextMenuItem onSelect={handleForward}>
        <Icon icon="mdi:share" class="mr-2 h-4 w-4" />
        {$_("contextMenu.forward")}
      </ContextMenuItem>
      <ContextMenuSeparator />
    {/if}

    <!-- Move/Delete actions -->
    <ContextMenuItem onSelect={handleArchive}>
      <Icon icon="mdi:archive-outline" class="mr-2 h-4 w-4" />
      {$_("contextMenu.archive")}
    </ContextMenuItem>
    <ContextMenuItem onSelect={handleDelete}>
      <Icon
        icon={isTrashFolder ? "mdi:delete-forever" : "mdi:delete-outline"}
        class="mr-2 h-4 w-4"
      />
      {$_(
        isTrashFolder ? "contextMenu.deletePermanently" : "contextMenu.delete"
      )}
    </ContextMenuItem>
    <ContextMenuItem onSelect={handleSpam}>
      <Icon
        icon={isSpamFolder
          ? "mdi:email-check-outline"
          : "mdi:alert-octagon-outline"}
        class="mr-2 h-4 w-4"
      />
      {$_(
        isSpamFolder ? "contextMenu.markAsNotSpam" : "contextMenu.markAsSpam"
      )}
    </ContextMenuItem>

    <ContextMenuSeparator />

    <!-- Move to folder picker -->
    <ContextMenuItem onSelect={openMoveTo}>
      <Icon icon="mdi:folder-move-outline" class="mr-2 h-4 w-4" />
      {$_("contextMenu.moveTo")}
    </ContextMenuItem>

    <!-- Copy to folder picker -->
    <ContextMenuItem onSelect={openCopyTo}>
      <Icon icon="mdi:content-copy" class="mr-2 h-4 w-4" />
      {$_("contextMenu.copyTo")}
    </ContextMenuItem>

    <ContextMenuSeparator />

    <!-- Flag actions -->
    <ContextMenuItem onSelect={handleToggleStar}>
      <Icon
        icon={isStarred ? "mdi:star" : "mdi:star-outline"}
        class="mr-2 h-4 w-4 {isStarred ? 'text-yellow-500' : ''}"
      />
      {$_(isStarred ? "contextMenu.removeStar" : "contextMenu.star")}
    </ContextMenuItem>
    <ContextMenuItem onSelect={handleToggleRead}>
      <Icon
        icon={isRead ? "mdi:email-outline" : "mdi:email-open-outline"}
        class="mr-2 h-4 w-4"
      />
      {$_(isRead ? "contextMenu.markAsUnread" : "contextMenu.markAsRead")}
    </ContextMenuItem>
  </ContextMenuContent>
</ContextMenuPrimitive.Root>

<!-- Permanent Delete Confirmation Dialog -->
<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_("dialog.deletePermanently")}
  description={$_("dialog.deleteDescription")}
  confirmLabel={$_("dialog.confirmDeletePermanently")}
  variant="destructive"
  onConfirm={handleConfirmPermanentDelete}
  onCancel={() => (showDeleteConfirm = false)}
/>

<!-- Folder Picker Dialog -->
<FolderPickerDialog
  bind:open={showFolderPicker}
  title={$_(
    folderPickerMode === "move" ? "contextMenu.moveTo" : "contextMenu.copyTo"
  )}
  {foldersLoading}
  {specialFolders}
  {customFolders}
  onSelect={handleFolderSelected}
/>
