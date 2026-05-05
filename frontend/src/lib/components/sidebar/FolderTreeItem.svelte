<script lang="ts">
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import { folder } from "../../../../wailsjs/go/models";
  import FolderContextMenu from "./FolderContextMenu.svelte";
  import Self from "./FolderTreeItem.svelte";

  interface Props {
    tree: folder.FolderTree;
    selectedFolderId: string;
    selectionSource: "unified" | "account" | null;
    collapsedFolders: Record<string, boolean>;
    onFolderSelect?: (f: folder.Folder) => void;
    onToggleCollapse?: (folderId: string) => void;
  }

  let {
    tree,
    selectedFolderId,
    selectionSource,
    collapsedFolders,
    onFolderSelect,
    onToggleCollapse
  }: Props = $props();

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

  function isFolderSelected(folderId: string): boolean {
    return selectionSource === "account" && selectedFolderId === folderId;
  }

  let hasChildren = $derived(tree.children && tree.children.length > 0);
  let isCollapsed = $derived(
    hasChildren
      ? collapsedFolders[tree.folder!.id] !== false // collapsed unless explicitly set to false
      : false
  );
</script>

{#if tree.folder}
  <FolderContextMenu folderId={tree.folder.id}>
    <button
      class="gap-2 px-3 py-1.5 text-sm rounded-md flex w-full items-center transition-colors {isFolderSelected(
        tree.folder.id
      )
        ? 'bg-primary/10 text-primary font-medium'
        : 'text-foreground hover:bg-muted/50'}"
      data-sidebar-item="folder"
      data-folder-id={tree.folder.id}
      data-has-children={hasChildren ? "true" : undefined}
      onclick={() => onFolderSelect?.(tree.folder!)}
    >
      <Icon
        icon={getFolderIcon(tree.folder.type)}
        class="w-4 h-4 flex-shrink-0"
      />
      <span class="truncate text-left">{tree.folder.name}</span>
      {#if hasChildren}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <span
          class="p-0.5 rounded hover:bg-muted flex-shrink-0"
          role="button"
          tabindex="-1"
          onclick={(e: MouseEvent) => {
            e.stopPropagation();
            onToggleCollapse?.(tree.folder!.id);
          }}
        >
          <Icon
            icon={isCollapsed ? "mdi:chevron-right" : "mdi:chevron-down"}
            class="w-4 h-4 text-muted-foreground"
          />
        </span>
      {/if}
      <span class="flex-1"></span>
      {#if tree.folder.unreadCount > 0}
        <span
          class="px-1.5 py-0.5 text-xs font-medium bg-primary text-primary-foreground rounded-full"
        >
          {tree.folder.unreadCount}
        </span>
      {/if}
    </button>
  </FolderContextMenu>

  {#if hasChildren && !isCollapsed}
    <div class="ml-4">
      {#each tree.children as childTree (childTree.folder?.id ?? "unknown")}
        <Self
          tree={childTree}
          {selectedFolderId}
          {selectionSource}
          {collapsedFolders}
          {onFolderSelect}
          {onToggleCollapse}
        />
      {/each}
    </div>
  {/if}
{/if}
