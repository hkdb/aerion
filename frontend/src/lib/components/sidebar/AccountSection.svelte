<script lang="ts">
  import { _ } from "$lib/i18n";
  import type { SyncProgress } from "$lib/stores/accounts.svelte";
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import { account, folder } from "../../../../wailsjs/go/models";
  import FolderTreeItem from "./FolderTreeItem.svelte";

  interface Props {
    account: account.Account;
    folders: folder.FolderTree[];
    loading: boolean;
    syncing: boolean;
    error: string | null;
    selectedFolderId: string;
    selectionSource: "unified" | "account" | null;
    isHeaderFocused?: boolean;
    isExpanded?: boolean;
    syncProgress?: SyncProgress | null;
    syncError?: { folderId: string; error: string } | null;
    onFolderSelect?: (
      accountId: string,
      folderId: string,
      folderPath: string,
      folderName: string,
      folderType: string
    ) => void;
    onToggleExpanded?: () => void;
    onEdit?: () => void;
    onDelete?: () => void;
    onSync?: () => void;
    collapsedFolders?: Record<string, boolean>;
    onToggleFolderCollapse?: (folderId: string) => void;
  }

  let {
    account: acc,
    folders,
    loading,
    syncing,
    error,
    selectedFolderId,
    selectionSource,
    isHeaderFocused = false,
    isExpanded = true,
    syncProgress = null,
    syncError = null,
    onFolderSelect,
    onToggleExpanded,
    onEdit,
    onDelete,
    onSync,
    collapsedFolders = {},
    onToggleFolderCollapse
  }: Props = $props();

  let showMenu = $state(false);

  // Toggle expand/collapse via callback
  function toggleExpanded() {
    onToggleExpanded?.();
  }

  function selectFolder(f: folder.Folder) {
    onFolderSelect?.(acc.id, f.id, f.path, f.name, f.type);
  }

  function toggleMenu(e: MouseEvent) {
    e.stopPropagation();
    showMenu = !showMenu;
  }

  function handleEdit() {
    showMenu = false;
    onEdit?.();
  }

  function handleDelete() {
    showMenu = false;
    onDelete?.();
  }

  function handleSync() {
    showMenu = false;
    onSync?.();
  }

  // Close menu when clicking outside
  function handleClickOutside() {
    showMenu = false;
  }
</script>

<svelte:window onclick={handleClickOutside} />

<div class="mb-2">
  <!-- Account Header -->
  <div class="group relative">
    <button
      class="gap-2 px-3 py-2 text-sm font-medium text-foreground hover:bg-muted/50 flex w-full items-center transition-colors {isHeaderFocused
        ? 'bg-muted ring-primary/50 ring-1'
        : ''}"
      data-sidebar-item="account-header"
      data-account-id={acc.id}
      onclick={toggleExpanded}
    >
      <Icon
        icon={isExpanded ? "mdi:chevron-down" : "mdi:chevron-right"}
        class="w-4 h-4 text-muted-foreground"
      />
      <Icon icon="mdi:email-outline" class="w-4 h-4" />
      <span class="flex-1 truncate text-left">{acc.name}</span>

      {#if syncing}
        <Icon
          icon="mdi:sync"
          class="w-4 h-4 animate-spin text-muted-foreground"
        />
      {:else if error}
        <span title={error}>
          <Icon icon="mdi:alert-circle" class="w-4 h-4 text-destructive" />
        </span>
      {/if}
    </button>

    <!-- Account Menu Button -->
    <button
      class="right-2 p-1 rounded hover:bg-muted absolute top-1/2 -translate-y-1/2 opacity-0 transition-colors group-hover:opacity-100 focus:opacity-100"
      onclick={toggleMenu}
    >
      <Icon icon="mdi:dots-vertical" class="w-4 h-4 text-muted-foreground" />
    </button>

    <!-- Dropdown Menu -->
    {#if showMenu}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div
        class="right-2 mt-1 bg-popover border-border rounded-md shadow-md py-1 absolute top-full z-50 min-w-[160px] border"
        role="menu"
        tabindex="-1"
        onclick={(e) => e.stopPropagation()}
      >
        <button
          class="gap-2 px-3 py-2 text-sm hover:bg-muted flex w-full items-center transition-colors"
          onclick={handleSync}
        >
          <Icon icon="mdi:sync" class="w-4 h-4" />
          <span>{$_("sidebar.syncNow")}</span>
        </button>
        <button
          class="gap-2 px-3 py-2 text-sm hover:bg-muted flex w-full items-center transition-colors"
          onclick={handleEdit}
        >
          <Icon icon="mdi:pencil-outline" class="w-4 h-4" />
          <span>{$_("sidebar.editAccount")}</span>
        </button>
        <div class="my-1 border-border border-t"></div>
        <button
          class="gap-2 px-3 py-2 text-sm text-destructive hover:bg-destructive/10 flex w-full items-center transition-colors"
          onclick={handleDelete}
        >
          <Icon icon="mdi:delete-outline" class="w-4 h-4" />
          <span>{$_("sidebar.deleteAccount")}</span>
        </button>
      </div>
    {/if}
  </div>

  <!-- Sync Progress Bar / Error -->
  {#if syncError}
    <div class="px-3 py-1.5">
      <div class="gap-2 text-destructive flex items-center">
        <Icon icon="mdi:alert-circle" class="w-4 h-4 flex-shrink-0" />
        <p class="text-xs">{$_("sidebar.syncError")}</p>
      </div>
    </div>
  {:else if syncing && syncProgress}
    <div class="px-3 py-1.5">
      <div class="h-1 bg-muted overflow-hidden rounded-full">
        <div
          class="bg-primary ease-out h-full transition-all duration-300"
          style="width: {syncProgress.percentage}%"
        ></div>
      </div>
      <p class="text-xs text-muted-foreground mt-1">
        {#if syncProgress.phase === "folders"}
          {$_("sidebar.syncingFolders")}
        {:else if syncProgress.phase === "messages"}
          {$_("sidebar.fetchingMessageList")}
        {:else if syncProgress.phase === "headers"}
          {$_("sidebar.fetchingHeaders", {
            values: { percentage: syncProgress.percentage }
          })}
        {:else}
          {$_("sidebar.syncingContent", {
            values: { percentage: syncProgress.percentage }
          })}
        {/if}
      </p>
    </div>
  {/if}

  <!-- Folder List -->
  {#if isExpanded}
    <div class="ml-4">
      {#if loading}
        <div
          class="gap-2 px-3 py-2 text-sm text-muted-foreground flex items-center"
        >
          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
          <span>{$_("sidebar.loadingFolders")}</span>
        </div>
      {:else if folders.length === 0}
        <div class="px-3 py-2 text-sm text-muted-foreground">
          {$_("sidebar.noFoldersSynced")}
        </div>
      {:else}
        {#each folders as tree (tree.folder?.id ?? "unknown")}
          <FolderTreeItem
            {tree}
            {selectedFolderId}
            {selectionSource}
            {collapsedFolders}
            onFolderSelect={(f) => selectFolder(f)}
            onToggleCollapse={(folderId) => onToggleFolderCollapse?.(folderId)}
          />
        {/each}
      {/if}
    </div>
  {/if}
</div>
