<script lang="ts">
  import { _ } from "$lib/i18n";
  import {
    isUnifiedInboxExpanded,
    setUnifiedInboxExpanded
  } from "$lib/stores/uiState.svelte";
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import type { account, folder } from "../../../../wailsjs/go/models";
  import FolderContextMenu from "./FolderContextMenu.svelte";

  interface AccountWithInbox {
    account: account.Account;
    inbox: folder.Folder | null;
  }

  interface Props {
    accounts: AccountWithInbox[];
    unifiedUnreadCount: number;
    selectedAccountId: string | null;
    selectedFolderId: string | null;
    selectionSource: "unified" | "account" | null;
    onSelectUnified: () => void;
    onSelectAccountInbox: (
      accountId: string,
      folderId: string,
      folderPath: string
    ) => void;
  }

  let {
    accounts,
    unifiedUnreadCount,
    selectedAccountId,
    selectedFolderId,
    selectionSource,
    onSelectUnified,
    onSelectAccountInbox
  }: Props = $props();

  // Initialize from persisted state (defaults to true)
  let expanded = $state(isUnifiedInboxExpanded());

  // Check if unified inbox is selected (All Inboxes)
  const isUnifiedSelected = $derived(
    selectedAccountId === "unified" && selectedFolderId === "inbox"
  );

  // Check if a specific account inbox is selected IN THE UNIFIED SECTION
  // Only highlight if selectionSource is 'unified'
  function isAccountInboxSelected(accountId: string, inboxId: string): boolean {
    return (
      selectionSource === "unified" &&
      selectedAccountId === accountId &&
      selectedFolderId === inboxId
    );
  }

  // Toggle expand/collapse and persist
  function toggleExpanded() {
    expanded = !expanded;
    setUnifiedInboxExpanded(expanded);
  }

  function handleUnifiedClick() {
    onSelectUnified();
  }

  function handleAccountInboxClick(acc: AccountWithInbox) {
    if (acc.inbox) {
      onSelectAccountInbox(acc.account.id, acc.inbox.id, acc.inbox.path);
    }
  }

  // Get account color with fallback
  function getAccountColor(acc: account.Account): string {
    // @ts-ignore - color field from backend
    return acc.color || "#6B7280"; // Default gray if no color set
  }
</script>

<div class="px-2 py-1">
  <!-- Unified Inbox Header -->
  <div
    class="gap-2 px-2 py-1.5 rounded-md flex w-full cursor-pointer items-center transition-colors {isUnifiedSelected
      ? 'bg-primary/10 text-primary'
      : 'hover:bg-muted/50'}"
    data-sidebar-item="unified"
  >
    <!-- Expand/Collapse Toggle -->
    <button
      class="p-0.5 -ml-0.5 hover:bg-muted rounded transition-colors"
      onclick={(e) => {
        e.stopPropagation();
        toggleExpanded();
      }}
    >
      <Icon
        icon={expanded ? "mdi:chevron-down" : "mdi:chevron-right"}
        class="w-4 h-4 text-muted-foreground"
      />
    </button>

    <!-- Clickable area for selecting unified inbox -->
    <button class="gap-2 flex flex-1 items-center" onclick={handleUnifiedClick}>
      <!-- Inbox Icon -->
      <Icon icon="mdi:inbox-multiple" class="w-4 h-4 flex-shrink-0" />

      <!-- Label -->
      <span class="text-sm font-medium flex-1 truncate text-left"
        >{$_("sidebar.allInboxes")}</span
      >

      <!-- Unread Badge -->
      {#if unifiedUnreadCount > 0}
        <span
          class="px-1.5 py-0.5 text-xs font-medium bg-primary text-primary-foreground rounded-full"
        >
          {unifiedUnreadCount}
        </span>
      {/if}
    </button>
  </div>

  <!-- Individual Account Inboxes -->
  {#if expanded}
    <div class="ml-4 mt-0.5 space-y-0.5">
      {#each accounts as acc (acc.account.id)}
        {#if acc.inbox}
          <FolderContextMenu folderId={acc.inbox.id}>
            <button
              class="gap-2 px-2 py-1.5 rounded-md text-sm flex w-full items-center transition-colors {isAccountInboxSelected(
                acc.account.id,
                acc.inbox.id
              )
                ? 'bg-primary/10 text-primary'
                : 'hover:bg-muted/50 text-muted-foreground hover:text-foreground'}"
              data-sidebar-item="unified-account"
              data-folder-id={acc.inbox.id}
              onclick={() => handleAccountInboxClick(acc)}
            >
              <!-- Account Color Dot -->
              <span
                class="w-2 h-2 flex-shrink-0 rounded-full"
                style="background-color: {getAccountColor(acc.account)}"
              ></span>

              <!-- Account Name -->
              <span class="flex-1 truncate text-left">{acc.account.name}</span>

              <!-- Unread Badge -->
              {#if acc.inbox.unreadCount > 0}
                <span
                  class="px-1.5 py-0.5 text-xs font-medium bg-muted text-muted-foreground rounded-full"
                >
                  {acc.inbox.unreadCount}
                </span>
              {/if}
            </button>
          </FolderContextMenu>
        {/if}
      {/each}
    </div>
  {/if}
</div>
