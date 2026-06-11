<script lang="ts">
  import Icon from '@iconify/svelte'
  import { onMount } from 'svelte'
  import AccountSection from './AccountSection.svelte'
  import UnifiedInboxSection from './UnifiedInboxSection.svelte'
  import AccountDialog from '$lib/components/settings/AccountDialog.svelte'
  import DeleteAccountDialog from '$lib/components/settings/DeleteAccountDialog.svelte'
  import SettingsDialog from '$lib/components/settings/SettingsDialog.svelte'
  import SidebarFooter from '$lib/components/kit/SidebarFooter.svelte'
  import { Button } from '$lib/components/ui/button'
  import { accountStore } from '$lib/stores/accounts.svelte'
  import { contactSourcesStore } from '$lib/stores/contactSources.svelte'
  import { isAccountExpanded, setAccountExpanded, isUnifiedInboxExpanded, setFolderCollapsed, getUIState, getUIStateVersion, saveUIState } from '$lib/stores/uiState.svelte'
  import { setFocusedPane } from '$lib/stores/keyboard.svelte'
  import { _ } from '$lib/i18n'
  // @ts-ignore - wailsjs path
  import { account, folder } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { GetUnifiedInboxUnreadCount } from '../../../../wailsjs/go/app/App'
  import { formatDistanceToNow } from 'date-fns'
  import { getCurrentDateFnsLocale } from '$lib/stores/settings.svelte'
  import { EventsOn } from '../../../../wailsjs/runtime/runtime'

  // Folder item type for flat navigation list
  interface FolderNavItem {
    type: 'unified' | 'unified-account' | 'account-header' | 'folder'
    accountId?: string
    folderId?: string
    folderPath?: string
    folderName: string
    folderType?: string
  }

  // Track focused account header for keyboard navigation
  let focusedAccountId = $state<string | null>(null)

  // Ref to scrollable container for auto-scroll
  let scrollContainer: HTMLDivElement | null = null

  // Track expanded state for each account (reactive, synced with persisted state)
  let expandedAccounts = $state<Record<string, boolean>>({})

  // Initialize expanded state from persisted storage
  // Depends on both accounts list AND UI state version (so it re-runs when persisted state loads)
  $effect(() => {
    // Read version to create dependency - effect re-runs when UI state finishes loading
    const _version = getUIStateVersion()

    const newExpanded: Record<string, boolean> = {}
    for (const acc of accountStore.accounts) {
      newExpanded[acc.account.id] = isAccountExpanded(acc.account.id)
    }
    expandedAccounts = newExpanded
  })

  // Toggle account expansion
  function toggleAccountExpanded(accountId: string) {
    const newValue = !expandedAccounts[accountId]
    expandedAccounts[accountId] = newValue
    setAccountExpanded(accountId, newValue)
  }

  // Track collapsed state for folders with children (reactive, synced with persisted state)
  let collapsedFolders = $state<Record<string, boolean>>({})

  // Initialize collapsed state from persisted storage and prune stale entries
  $effect(() => {
    const _version = getUIStateVersion()

    // Collect all folder IDs that exist in current accounts
    const allFolderIds = new Set<string>()
    const collectIds = (trees: folder.FolderTree[]) => {
      for (const tree of trees) {
        if (tree.folder) allFolderIds.add(tree.folder.id)
        if (tree.children) collectIds(tree.children)
      }
    }
    for (const acc of accountStore.accounts) {
      collectIds(acc.folders || [])
    }

    // Read persisted collapsed state, keep only entries for existing folders
    const persisted = getUIState().collapsedFolders
    const newCollapsed: Record<string, boolean> = {}
    let hasStale = false
    for (const folderId of Object.keys(persisted)) {
      if (allFolderIds.has(folderId)) {
        newCollapsed[folderId] = persisted[folderId]
      } else {
        hasStale = true
      }
    }
    collapsedFolders = newCollapsed

    // Persist cleaned state if stale entries were pruned
    if (hasStale) {
      saveUIState({ collapsedFolders: newCollapsed })
    }
  })

  // Toggle folder collapse
  function toggleFolderCollapsed(folderId: string) {
    const isCurrentlyCollapsed = collapsedFolders[folderId] !== false
    const newValue = !isCurrentlyCollapsed
    collapsedFolders = { ...collapsedFolders, [folderId]: newValue }
    setFolderCollapsed(folderId, newValue)
  }

  interface Props {
    onFolderSelect?: (accountId: string, folderId: string, folderPath: string, folderName: string, folderType: string) => void
    onUnifiedFolderSelect?: (accountId: string, folderId: string, folderPath: string, folderName: string, folderType: string) => void
    onUnifiedInboxSelect?: () => void
    onCompose?: () => void
    onMessagesMoved?: () => void
    selectedAccountId?: string | null
    selectedFolderId?: string | null
    selectionSource?: 'unified' | 'account' | null
    isFocused?: boolean
    isFlashing?: boolean
    showBackButton?: boolean
    onBack?: () => void
  }

  let {
    onFolderSelect,
    onUnifiedFolderSelect,
    onUnifiedInboxSelect,
    onCompose,
    onMessagesMoved,
    selectedAccountId = null,
    selectedFolderId = null,
    selectionSource = null,
    isFocused: _isFocused = false,
    isFlashing = false,
    showBackButton = false,
    onBack,
  }: Props = $props()

  // Unified inbox state
  let unifiedUnreadCount = $state(0)

  // Dialog state
  let showAccountDialog = $state(false)
  let showDeleteDialog = $state(false)
  let showSettingsDialog = $state(false)
  let editingAccount = $state<account.Account | null>(null)
  let deletingAccount = $state<account.Account | null>(null)

  // Load accounts and contact sources on mount
  onMount(() => {
    // Load accounts, then trigger comprehensive sync on launch
    accountStore.load().then(async () => {
      try {
        await accountStore.syncAllComplete()
      } catch (err) {
        console.error('Failed to sync on launch:', err)
      }
    })

    contactSourcesStore.load()
    loadUnifiedInboxCount()

    // Listen for folder count changes to update unified inbox count
    const unsubscribe = EventsOn('folders:countsChanged', (_data: Record<string, number>) => {
      loadUnifiedInboxCount()
    })

    return () => {
      unsubscribe()
    }
  })

  // Load unified inbox unread count
  async function loadUnifiedInboxCount() {
    try {
      const count = await GetUnifiedInboxUnreadCount()
      unifiedUnreadCount = count
    } catch (err) {
      console.error('Failed to load unified inbox count:', err)
    }
  }

  // Get accounts with their inbox folders for unified inbox section
  function getAccountsWithInbox() {
    return accountStore.accounts.map(acc => {
      // Find the inbox folder in the folder tree
      const findInbox = (folders: folder.FolderTree[]): folder.Folder | null => {
        for (const f of folders) {
          if (f.folder?.type === 'inbox') {
            return f.folder
          }
          if (f.children) {
            const found = findInbox(f.children)
            if (found) return found
          }
        }
        return null
      }
      return {
        account: acc.account,
        inbox: findInbox(acc.folders || [])
      }
    })
  }

  // Handle unified inbox selection (All Inboxes)
  function handleUnifiedInboxSelect() {
    onUnifiedInboxSelect?.()
  }

  // Handle individual account inbox selection from unified section
  function handleAccountInboxSelect(accountId: string, folderId: string, folderPath: string) {
    onUnifiedFolderSelect?.(accountId, folderId, folderPath, 'Inbox', 'inbox')
  }

  // Sync status: { accountName, label, percentage } — accountName + percentage are populated only when a sync is active
  let syncStatus = $derived.by<{ accountName: string | null; label: string; percentage: number | null }>(() => {
    if (accountStore.isAnySyncing) {
      const syncingAcc = accountStore.accounts.find((a) => a.syncing)
      if (syncingAcc) {
        const accountName = syncingAcc.account.name
        const progress = accountStore.getSyncProgress(syncingAcc.account.id)
        if (progress) {
          if (progress.phase === 'folders') return { accountName, label: $_('sidebar.syncingFolders'), percentage: null }
          if (progress.phase === 'messages') return { accountName, label: $_('sidebar.fetchingMessageList'), percentage: null }
          if (progress.phase === 'headers') return { accountName, label: $_('sidebar.fetchingHeaders', { values: { percentage: progress.percentage } }), percentage: progress.percentage }
          return { accountName, label: $_('sidebar.syncingContent', { values: { percentage: progress.percentage } }), percentage: progress.percentage }
        }
        return { accountName, label: $_('sidebar.syncing'), percentage: null }
      }
      return { accountName: null, label: $_('sidebar.syncing'), percentage: null }
    }
    if (!accountStore.isOnline) return { accountName: null, label: $_('sidebar.offline'), percentage: null }
    if (!accountStore.lastSyncTime) return { accountName: null, label: $_('sidebar.notSynced'), percentage: null }
    return { accountName: null, label: $_('sidebar.synced', { values: { time: formatDistanceToNow(accountStore.lastSyncTime, { addSuffix: true, locale: getCurrentDateFnsLocale() }) } }), percentage: null }
  })

  // Handle folder selection
  function handleFolderSelect(accountId: string, folderId: string, folderPath: string, folderName: string, folderType: string) {
    accountStore.selectFolder(accountId, folderId, folderPath, folderName)
    onFolderSelect?.(accountId, folderId, folderPath, folderName, folderType)
  }

  // Open add account dialog
  function openAddAccount() {
    editingAccount = null
    showAccountDialog = true
  }

  // Open edit account dialog
  function openEditAccount(acc: account.Account) {
    editingAccount = acc
    showAccountDialog = true
  }

  // Open delete confirmation
  function openDeleteAccount(acc: account.Account) {
    deletingAccount = acc
    showDeleteDialog = true
  }

  // Sync all accounts (comprehensive sync)
  export async function syncAllAccounts() {
    try {
      await accountStore.syncAllComplete()
    } catch (err) {
      console.error('Sync failed:', err)
      // Error is already stored in account store
    }
  }

  // Cancel all running syncs
  export async function cancelSync() {
    try {
      await accountStore.cancelAllSyncs()
    } catch (err) {
      console.error('Failed to cancel sync:', err)
    }
  }

  // Toggle sync (start if not running, cancel if running) - for keyboard shortcut
  export async function toggleSync() {
    if (accountStore.isAnySyncing) {
      await cancelSync()
    } else {
      await syncAllAccounts()
    }
  }

  // Build flat list of all navigable folders including Unified Inbox
  // The list matches the exact visual order in the sidebar, respecting expanded/collapsed state
  function buildFolderNavList(): FolderNavItem[] {
    const items: FolderNavItem[] = []

    // Add Unified Inbox section items if more than 1 account
    if (accountStore.accounts.length > 1) {
      // 1. Add "All Inboxes"
      items.push({
        type: 'unified',
        folderName: 'Unified Inbox',
        folderType: 'unified',
      })

      // 2. Add each account's inbox (under unified section) - only if unified section is expanded
      if (isUnifiedInboxExpanded()) {
        for (const accWithFolders of accountStore.accounts) {
          // Skip if account is not fully loaded yet (can happen during reauth)
          if (!accWithFolders.account) continue

          const findInbox = (trees: folder.FolderTree[]): folder.Folder | null => {
            for (const tree of trees) {
              if (tree.folder?.type === 'inbox') return tree.folder
              if (tree.children) {
                const found = findInbox(tree.children)
                if (found) return found
              }
            }
            return null
          }
          const inbox = findInbox(accWithFolders.folders || [])
          if (inbox) {
            items.push({
              type: 'unified-account',
              accountId: accWithFolders.account.id,
              folderId: inbox.id,
              folderPath: inbox.path,
              folderName: inbox.name,
              folderType: 'inbox',
            })
          }
        }
      }
    }

    // 3. Add account headers and their folders
    for (const accWithFolders of accountStore.accounts) {
      // Skip if account is not fully loaded yet (can happen during reauth)
      if (!accWithFolders.account) continue

      // Always add the account header (so user can navigate to it and expand)
      items.push({
        type: 'account-header',
        accountId: accWithFolders.account.id,
        folderName: accWithFolders.account.name,
      })

      // Only add folders if the account is expanded
      if (expandedAccounts[accWithFolders.account.id]) {
        const flattenFolders = (trees: folder.FolderTree[]) => {
          for (const tree of trees) {
            if (tree.folder) {
              items.push({
                type: 'folder',
                accountId: accWithFolders.account.id,
                folderId: tree.folder.id,
                folderPath: tree.folder.path,
                folderName: tree.folder.name,
                folderType: tree.folder.type,
              })
            }
            // Skip children of collapsed folders
            if (tree.children && tree.children.length > 0 && tree.folder && collapsedFolders[tree.folder.id] === false) {
              flattenFolders(tree.children)
            }
          }
        }
        flattenFolders(accWithFolders.folders || [])
      }
    }

    return items
  }

  // Get current folder index in navigation list
  function getCurrentFolderIndex(): number {
    const navList = buildFolderNavList()

    // Check if an account header is focused
    if (focusedAccountId) {
      return navList.findIndex(item =>
        item.type === 'account-header' && item.accountId === focusedAccountId
      )
    }

    // Check if Unified Inbox is selected (All Inboxes)
    if (selectedAccountId === 'unified') {
      return navList.findIndex(item => item.type === 'unified')
    }

    // Check selectionSource to find the correct item
    if (selectionSource === 'unified') {
      // Looking for unified-account item
      return navList.findIndex(item =>
        item.type === 'unified-account' && item.folderId === selectedFolderId
      )
    } else {
      // Looking for regular folder item
      return navList.findIndex(item =>
        item.type === 'folder' && item.folderId === selectedFolderId
      )
    }
  }

  // Navigate to previous folder (exposed for keyboard navigation)
  export function selectPreviousFolder() {
    const navList = buildFolderNavList()
    if (navList.length === 0) return

    const currentIndex = getCurrentFolderIndex()
    const newIndex = currentIndex <= 0 ? 0 : currentIndex - 1

    selectFolderByIndex(navList, newIndex)
  }

  // Navigate to next folder (exposed for keyboard navigation)
  export function selectNextFolder() {
    const navList = buildFolderNavList()
    if (navList.length === 0) return

    const currentIndex = getCurrentFolderIndex()
    const newIndex = currentIndex >= navList.length - 1 ? navList.length - 1 : currentIndex + 1

    selectFolderByIndex(navList, newIndex)
  }

  // Scroll an item into view
  function scrollItemIntoView(item: FolderNavItem) {
    if (!scrollContainer) return

    // Build selector based on item type
    let selector: string | null = null
    if (item.type === 'unified') {
      selector = '[data-sidebar-item="unified"]'
    } else if (item.type === 'unified-account' && item.folderId) {
      selector = `[data-sidebar-item="unified-account"][data-folder-id="${item.folderId}"]`
    } else if (item.type === 'account-header' && item.accountId) {
      selector = `[data-sidebar-item="account-header"][data-account-id="${item.accountId}"]`
    } else if (item.type === 'folder' && item.folderId) {
      selector = `[data-sidebar-item="folder"][data-folder-id="${item.folderId}"]`
    }

    if (selector) {
      const element = scrollContainer.querySelector(selector)
      element?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }

  // Select folder by index in nav list
  function selectFolderByIndex(navList: FolderNavItem[], index: number) {
    const item = navList[index]
    if (!item) return

    // Clear account header focus when selecting a folder
    if (item.type !== 'account-header') {
      focusedAccountId = null
    }

    if (item.type === 'unified') {
      onUnifiedInboxSelect?.()
    } else if (item.type === 'unified-account' && item.accountId && item.folderId && item.folderPath) {
      // Select from unified section - uses onUnifiedFolderSelect
      onUnifiedFolderSelect?.(item.accountId, item.folderId, item.folderPath, item.folderName, item.folderType || 'inbox')
    } else if (item.type === 'account-header' && item.accountId) {
      // Focus on account header (Enter/Space will toggle expand)
      focusedAccountId = item.accountId
    } else if (item.type === 'folder' && item.accountId && item.folderId && item.folderPath) {
      // Select from account tree - uses handleFolderSelect
      handleFolderSelect(item.accountId, item.folderId, item.folderPath, item.folderName, item.folderType || 'folder')
    }

    // Scroll the selected item into view
    scrollItemIntoView(item)
  }

  // Toggle expand/collapse for the focused account (called on Enter/Space/Alt+Enter)
  export function toggleFocusedAccount() {
    if (focusedAccountId) {
      toggleAccountExpanded(focusedAccountId)
    }
  }

  // Check if an account header is focused
  export function hasFocusedAccount(): boolean {
    return focusedAccountId !== null
  }

  // Check if the currently selected folder has children
  export function hasSelectedFolderWithChildren(): boolean {
    if (!selectedFolderId || selectionSource !== 'account') return false
    return folderHasChildren(selectedFolderId)
  }

  // Toggle collapse for the currently selected folder
  export function toggleSelectedFolderCollapse(): void {
    if (!selectedFolderId || selectionSource !== 'account') return
    if (!folderHasChildren(selectedFolderId)) return
    toggleFolderCollapsed(selectedFolderId)
  }

  // Check if a folder has children by searching the account folder trees
  function folderHasChildren(folderId: string): boolean {
    for (const acc of accountStore.accounts) {
      const found = findTreeNode(acc.folders || [], folderId)
      if (found) return (found.children && found.children.length > 0) || false
    }
    return false
  }

  // Find a FolderTree node by folder ID
  function findTreeNode(trees: folder.FolderTree[], folderId: string): folder.FolderTree | null {
    for (const tree of trees) {
      if (tree.folder?.id === folderId) return tree
      if (tree.children) {
        const found = findTreeNode(tree.children, folderId)
        if (found) return found
      }
    }
    return null
  }
</script>

<div class="flex flex-col h-full {isFlashing ? 'pane-focus-flash' : ''}">
  <!-- Header with Compose Button -->
  <div class="px-4 py-3 border-b border-border">
    <div class="flex items-center gap-2">
      <button
        class="flex-1 flex items-center justify-center gap-2 px-3 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors"
        onclick={onCompose}
      >
        <Icon icon="mdi:pencil" class="w-4 h-4" />
        <span>{$_('sidebar.compose')}</span>
      </button>
      {#if showBackButton}
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors flex-shrink-0"
          title={$_('responsive.back')}
          aria-label={$_('aria.closeSidebar')}
          onclick={onBack}
        >
          <Icon icon="mdi:close" class="w-5 h-5 text-muted-foreground" />
        </button>
      {/if}
    </div>
  </div>

  <!-- Account List -->
  <div class="flex-1 overflow-y-auto scrollbar-thin py-2" bind:this={scrollContainer}>
    {#if accountStore.loading}
      <div class="flex items-center justify-center py-8">
        <Icon icon="mdi:loading" class="w-6 h-6 animate-spin text-muted-foreground" />
      </div>
    {:else if accountStore.accounts.length === 0}
      <!-- Empty State -->
      <div class="flex flex-col items-center justify-center py-8 px-4 text-center">
        <Icon icon="mdi:email-plus-outline" class="w-12 h-12 text-muted-foreground mb-3" />
        <h3 class="text-sm font-medium mb-1">{$_('sidebar.noAccountsYet')}</h3>
        <p class="text-xs text-muted-foreground mb-4">
          {$_('sidebar.addFirstAccount')}
        </p>
        <Button size="sm" onclick={openAddAccount}>
          <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
          {$_('sidebar.addAccount')}
        </Button>
      </div>
    {:else}
      <!-- Unified Inbox Section (only show if more than 1 account) -->
      {#if accountStore.accounts.length > 1}
        <UnifiedInboxSection
          accounts={getAccountsWithInbox()}
          {unifiedUnreadCount}
          {selectedAccountId}
          {selectedFolderId}
          {selectionSource}
          onSelectUnified={handleUnifiedInboxSelect}
          onSelectAccountInbox={handleAccountInboxSelect}
        />
        <div class="border-b border-border mx-3 my-1"></div>
      {/if}

      {#each accountStore.accounts as accWithFolders (accWithFolders.account.id)}
        <AccountSection
          account={accWithFolders.account}
          folders={accWithFolders.folders}
          loading={accWithFolders.loading}
          syncing={accWithFolders.syncing}
          error={accWithFolders.error}
          selectedFolderId={accountStore.selectedFolder?.folderId ?? ''}
          {selectionSource}
          isHeaderFocused={focusedAccountId === accWithFolders.account.id}
          isExpanded={expandedAccounts[accWithFolders.account.id] ?? true}
          syncError={accountStore.getSyncError(accWithFolders.account.id)}
          {collapsedFolders}
          {onMessagesMoved}
          onFolderSelect={handleFolderSelect}
          onToggleExpanded={() => toggleAccountExpanded(accWithFolders.account.id)}
          onToggleFolderCollapse={toggleFolderCollapsed}
          onEdit={() => openEditAccount(accWithFolders.account)}
          onDelete={() => openDeleteAccount(accWithFolders.account)}
          onSync={() => {
            // Clear any sync error before retrying
            accountStore.clearSyncError(accWithFolders.account.id)
            accountStore.syncAccount(accWithFolders.account.id)
          }}
        />
      {/each}

      <!-- Add Account Button -->
      <div class="px-3 py-2">
        <button
          class="w-full flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground hover:text-foreground hover:bg-muted/50 rounded-md transition-colors"
          onclick={openAddAccount}
        >
          <Icon icon="mdi:plus" class="w-4 h-4" />
          <span>{$_('sidebar.addAccount')}</span>
        </button>
      </div>
    {/if}
  </div>

  <!-- Footer: Sync Status + Settings. Chrome (padding/border/min-height)
       lives in kit `SidebarFooter` so mail, calendar, and contacts all
       render the same strip height. Mail's progress bar overlay + 2-line
       leading content (account name + status label) pass through
       SidebarFooter's overlay/leading snippets unchanged. -->
  <SidebarFooter>
    {#snippet overlay()}
      {#if syncStatus.percentage !== null}
        <div class="absolute top-0 left-0 right-0 h-1 bg-muted overflow-hidden">
          <div
            class="h-full bg-primary transition-all duration-300 ease-out"
            style="width: {syncStatus.percentage}%"
          ></div>
        </div>
      {/if}
    {/snippet}
    {#snippet leading()}
      <button
        class="flex-1 min-w-0 flex items-end gap-2 hover:text-foreground transition-colors text-left"
        onclick={accountStore.isAnySyncing ? cancelSync : syncAllAccounts}
        title={$_(accountStore.isAnySyncing ? 'sidebar.clickToCancel' : 'sidebar.syncAllAccounts')}
      >
        <Icon
          icon="mdi:sync"
          class="w-4 h-4 flex-shrink-0 {accountStore.isAnySyncing ? 'animate-spin' : ''}"
        />
        <div class="flex-1 min-w-0">
          {#if syncStatus.accountName}
            <div class="truncate text-foreground font-medium leading-tight mb-0.5">{syncStatus.accountName}</div>
          {/if}
          <span class="block leading-tight">{syncStatus.label}</span>
        </div>
      </button>
    {/snippet}
    {#snippet trailing()}
      <button
        class="p-1 hover:text-foreground hover:bg-muted rounded transition-colors relative"
        onclick={() => showSettingsDialog = true}
        title={$_('sidebar.settings')}
      >
        <Icon icon="mdi:cog" class="w-4 h-4" />
        {#if contactSourcesStore.hasErrors}
          <span class="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 bg-destructive rounded-full border border-background"></span>
        {/if}
      </button>
    {/snippet}
  </SidebarFooter>
</div>

<!-- Account Dialog -->
<AccountDialog
  bind:open={showAccountDialog}
  editAccount={editingAccount}
  onClose={() => {
    showAccountDialog = false
    editingAccount = null
    setFocusedPane('messageList')
  }}
/>

<!-- Delete Confirmation Dialog -->
<DeleteAccountDialog
  bind:open={showDeleteDialog}
  account={deletingAccount}
  onClose={() => {
    showDeleteDialog = false
    deletingAccount = null
    setFocusedPane('messageList')
  }}
/>

<!-- Settings Dialog -->
<SettingsDialog
  bind:open={showSettingsDialog}
  onClose={() => {
    showSettingsDialog = false
    setFocusedPane('messageList')
  }}
/>
