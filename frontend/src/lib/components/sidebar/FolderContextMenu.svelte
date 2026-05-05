<script lang="ts">
  import {
    ContextMenuContent,
    ContextMenuItem
  } from "$lib/components/ui/context-menu";
  import { _ } from "$lib/i18n";
  import { toasts } from "$lib/stores/toast";
  import Icon from "@iconify/svelte";
  import { ContextMenu as ContextMenuPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  import {
    MarkAllFolderMessagesAsRead,
    MarkAllFolderMessagesAsUnread,
    Undo
  } from "../../../../wailsjs/go/app/App";

  interface Props {
    folderId: string;
    children?: Snippet;
  }

  let { folderId, children }: Props = $props();

  async function handleUndo() {
    try {
      const description = await Undo();
      toasts.success($_("toast.undone", { values: { description } }));
    } catch (err) {
      console.error("Undo failed:", err);
      toasts.error($_("toast.undoFailed"));
    }
  }

  async function handleMarkAllRead() {
    try {
      await MarkAllFolderMessagesAsRead(folderId);
      toasts.success($_("toast.markedAllAsRead"), [
        { label: $_("common.undo"), onClick: handleUndo }
      ]);
    } catch (err) {
      console.error("Mark all as read failed:", err);
      toasts.error($_("toast.failedToMarkAllAsRead"));
    }
  }

  async function handleMarkAllUnread() {
    try {
      await MarkAllFolderMessagesAsUnread(folderId);
      toasts.success($_("toast.markedAllAsUnread"), [
        { label: $_("common.undo"), onClick: handleUndo }
      ]);
    } catch (err) {
      console.error("Mark all as unread failed:", err);
      toasts.error($_("toast.failedToMarkAllAsUnread"));
    }
  }
</script>

<ContextMenuPrimitive.Root>
  <ContextMenuPrimitive.Trigger>
    {#if children}
      {@render children()}
    {/if}
  </ContextMenuPrimitive.Trigger>

  <ContextMenuContent>
    <ContextMenuItem onSelect={handleMarkAllRead}>
      <Icon icon="mdi:email-check-outline" class="mr-2 h-4 w-4" />
      {$_("contextMenu.markAllAsRead")}
    </ContextMenuItem>
    <ContextMenuItem onSelect={handleMarkAllUnread}>
      <Icon icon="mdi:email-outline" class="mr-2 h-4 w-4" />
      {$_("contextMenu.markAllAsUnread")}
    </ContextMenuItem>
  </ContextMenuContent>
</ContextMenuPrimitive.Root>
