import {
  GetAccounts,
  GetFolderTree,
  SyncAccountComplete,
  SyncAllComplete,
  CancelAllSyncs,
  AddAccount,
  UpdateAccount,
  RemoveAccount,
  TestConnection,
  CompleteOAuthAccountSetup,
  ReorderAccounts,
} from '../../../wailsjs/go/app/App'
import { account, app, folder } from '../../../wailsjs/go/models'
// @ts-ignore - wailsjs runtime
import { EventsOn } from '../../../wailsjs/runtime/runtime'

export interface AccountWithFolders {
  account: account.Account
  folders: folder.FolderTree[]
  loading: boolean
  syncing: boolean
  error: string | null
  lastSync: Date | null
}

export interface SyncProgress {
  folderId: string
  fetched: number
  total: number
  phase: 'folders' | 'messages' | 'headers' | 'bodies'
  percentage: number
}

export interface SelectedFolder {
  accountId: string
  folderId: string
  folderPath: string
  folderName: string
}

class AccountStore {
  // State
  accounts = $state<AccountWithFolders[]>([])
  loading = $state(false)
  error = $state<string | null>(null)
  selectedFolder = $state<SelectedFolder | null>(null)
  isOnline = $state(true) // Track online/offline status
  // Sync progress keyed by accountId, then folderId (supports multiple folders syncing per account)
  syncProgress = $state<Record<string, Record<string, SyncProgress>>>({})
  // Sync errors keyed by accountId (shows "Sync error. Try again..." message)
  syncErrors = $state<Record<string, { folderId: string; error: string }>>({})
  private eventsInitialized = false

  /**
   * Initialize event listeners (called once)
   */
  private initEvents(): void {
    if (this.eventsInitialized) return
    this.eventsInitialized = true

    // Listen for folder count changes (e.g., when messages are marked as read)
    EventsOn('folders:countsChanged', (folderCounts: Record<string, number>) => {
      // Update folder counts locally instead of reloading from DB
      for (const acc of this.accounts) {
        this.updateFolderCountsInTree(acc.folders, folderCounts)
      }
      // Trigger reactivity by reassigning the array reference
      this.accounts = [...this.accounts]
    })

    // Listen for sync progress updates
    EventsOn('sync:progress', (data: { accountId: string; folderId: string; fetched: number; total: number; phase: string }) => {
      // Cap percentage at 100% as a safety net
      const percentage = data.total > 0 ? Math.min(100, Math.round((data.fetched / data.total) * 100)) : 0

      // Initialize account's progress map if needed
      if (!this.syncProgress[data.accountId]) {
        this.syncProgress[data.accountId] = {}
      }

      // When we start syncing an actual folder, clear the "folders" phase entry
      // (folders phase uses empty folderId)
      if (data.folderId && this.syncProgress[data.accountId]['']) {
        delete this.syncProgress[data.accountId]['']
      }

      // Store progress keyed by folderId within the account
      this.syncProgress[data.accountId][data.folderId] = {
        folderId: data.folderId,
        fetched: data.fetched,
        total: data.total,
        phase: data.phase as 'folders' | 'messages' | 'headers' | 'bodies',
        percentage,
      }
      // Trigger reactivity
      this.syncProgress = { ...this.syncProgress }

      // Also set syncing flag on the account so progress bar shows
      const acc = this.accounts.find((a) => a.account.id === data.accountId)
      if (acc && !acc.syncing) {
        acc.syncing = true
        this.accounts = [...this.accounts]
      }
    })

    // Listen for folder sync complete (clear progress for that specific folder)
    EventsOn('folder:synced', (data: { accountId: string; folderId: string }) => {
      // Clear progress for this folder if it exists
      if (this.syncProgress[data.accountId]?.[data.folderId]) {
        delete this.syncProgress[data.accountId][data.folderId]
      }

      // Also clear the "folders" phase entry (uses empty folderId)
      if (this.syncProgress[data.accountId]?.['']) {
        delete this.syncProgress[data.accountId]['']
      }

      // Clear account entry if no more folders are syncing
      if (this.syncProgress[data.accountId] && Object.keys(this.syncProgress[data.accountId]).length === 0) {
        delete this.syncProgress[data.accountId]
      }

      this.syncProgress = { ...this.syncProgress }

      // Clear sync error if the failed folder just synced successfully
      if (this.syncErrors[data.accountId]?.folderId === data.folderId) {
        delete this.syncErrors[data.accountId]
        this.syncErrors = { ...this.syncErrors }
      }

      // Always check if we should clear the syncing flag
      // This handles cases where sync completes so fast no progress was recorded
      const hasRemainingProgress = this.syncProgress[data.accountId] &&
        Object.keys(this.syncProgress[data.accountId]).length > 0

      if (!hasRemainingProgress) {
        const acc = this.accounts.find((a) => a.account.id === data.accountId)
        if (acc) {
          if (acc.syncing) {
            acc.syncing = false
          }
          // Update lastSync time (handles wake-from-sleep syncs too)
          acc.lastSync = new Date()
          this.accounts = [...this.accounts]
        }
      }
    })

    // Listen for sync errors
    EventsOn('folder:syncError', (data: { accountId: string; folderId: string; error: string }) => {
      console.error('[AccountStore] Sync error:', data)

      // Clear any progress for this account/folder
      if (this.syncProgress[data.accountId]?.[data.folderId]) {
        delete this.syncProgress[data.accountId][data.folderId]
        if (Object.keys(this.syncProgress[data.accountId]).length === 0) {
          delete this.syncProgress[data.accountId]
        }
        this.syncProgress = { ...this.syncProgress }
      }

      // Set error state for this account
      this.syncErrors[data.accountId] = {
        folderId: data.folderId,
        error: data.error,
      }
      this.syncErrors = { ...this.syncErrors }

      // Clear syncing flag
      const acc = this.accounts.find((a) => a.account.id === data.accountId)
      if (acc && acc.syncing) {
        acc.syncing = false
        this.accounts = [...this.accounts]
      }
    })

    // Track online/offline status using browser API + backend connectivity events
    this.isOnline = navigator.onLine

    window.addEventListener('online', () => {
      this.isOnline = true
    })

    window.addEventListener('offline', () => {
      this.isOnline = false
    })

    // Backend connectivity detection (wake from sleep IMAP reachability check)
    EventsOn('network:online', () => {
      this.isOnline = true
    })

    EventsOn('network:offline', () => {
      this.isOnline = false
    })
  }

  /**
   * Check if any folder in the tree matches the given folder IDs
   */
  private findFolderInTree(trees: folder.FolderTree[], folderIds: string[]): boolean {
    for (const tree of trees) {
      if (tree.folder && folderIds.includes(tree.folder.id)) {
        return true
      }
      if (tree.children && this.findFolderInTree(tree.children, folderIds)) {
        return true
      }
    }
    return false
  }

  /**
   * Update folder unread counts in the tree based on the counts map
   */
  private updateFolderCountsInTree(trees: folder.FolderTree[], counts: Record<string, number>): void {
    for (const tree of trees) {
      if (tree.folder && counts[tree.folder.id] !== undefined) {
        tree.folder.unreadCount = counts[tree.folder.id]
      }
      if (tree.children) {
        this.updateFolderCountsInTree(tree.children, counts)
      }
    }
  }

  /**
   * Load all accounts from the backend
   */
  async load(): Promise<void> {
    // Initialize event listeners on first load
    this.initEvents()
    this.loading = true
    this.error = null

    try {
      const accountList = await GetAccounts()

      // Initialize accounts with empty folders
      this.accounts = (accountList || []).map((acc) => ({
        account: acc,
        folders: [],
        loading: false,
        syncing: false,
        error: null,
        lastSync: null,
      }))

      // Load folders for each account in parallel
      await Promise.all(
        this.accounts.map((acc) => this.loadFolders(acc.account.id))
      )
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err)
      console.error('Failed to load accounts:', err)
    } finally {
      this.loading = false
    }
  }

  /**
   * Load folders for a specific account
   */
  async loadFolders(accountId: string): Promise<void> {
    const acc = this.accounts.find((a) => a.account.id === accountId)
    if (!acc) return

    acc.loading = true
    acc.error = null

    try {
      const folderTree = await GetFolderTree(accountId)
      acc.folders = folderTree || []
    } catch (err) {
      acc.error = err instanceof Error ? err.message : String(err)
      console.error(`Failed to load folders for account ${accountId}:`, err)
    } finally {
      acc.loading = false
    }
  }

  /**
   * Sync folders for an account with IMAP server
   */
  async syncAccount(accountId: string): Promise<void> {
    const acc = this.accounts.find((a) => a.account.id === accountId)
    if (!acc) return

    acc.syncing = true
    acc.error = null

    try {
      // Use SyncAccountComplete to sync folders + core folder messages (Inbox, Drafts, Sent)
      await SyncAccountComplete(accountId)
      acc.lastSync = new Date()
      // Reload folders after sync
      await this.loadFolders(accountId)
    } catch (err) {
      acc.error = err instanceof Error ? err.message : String(err)
      console.error(`Failed to sync account ${accountId}:`, err)
      // Clear syncing on error (event handlers won't clear it)
      acc.syncing = false
      throw err
    }
    // NOTE: Don't set syncing=false here - body sync runs in background
    // and will emit folder:synced when complete, which clears syncing flag
  }

  /**
   * Comprehensive sync of all accounts (folders + messages) and contacts.
   * Syncs: folder list, Inbox/Drafts/Sent messages, CardDAV contacts.
   */
  async syncAllComplete(): Promise<void> {
    // Mark all accounts as syncing
    for (const acc of this.accounts) {
      acc.syncing = true
      acc.error = null
    }

    try {
      await SyncAllComplete()
      // Update last sync time for all accounts
      const now = new Date()
      for (const acc of this.accounts) {
        acc.lastSync = now
      }
      // Reload folders for all accounts
      for (const acc of this.accounts) {
        await this.loadFolders(acc.account.id)
      }
    } catch (err) {
      // Parse which account(s) actually failed and only set error on those
      const errorMsg = err instanceof Error ? err.message : String(err)
      // Backend returns format like: "sync errors: email@example.com: error; email2@example.com: error"
      for (const acc of this.accounts) {
        // Check if this account's email appears in the error message
        if (errorMsg.includes(acc.account.email + ':')) {
          acc.error = errorMsg
          // Clear syncing on error for failed accounts
          acc.syncing = false
        }
      }
      throw err
    }
    // NOTE: Don't set syncing=false here - body syncs run in background
    // and will emit folder:synced when complete, which clears syncing flag
  }

  /**
   * Cancel all running syncs
   */
  async cancelAllSyncs(): Promise<void> {
    await CancelAllSyncs()
    // Mark all accounts as not syncing
    for (const acc of this.accounts) {
      acc.syncing = false
    }
  }

  /**
   * Add a new account (password authentication)
   */
  async addAccount(config: account.AccountConfig): Promise<account.Account> {
    const newAccount = await AddAccount(config)

    // Add to local state
    this.accounts.push({
      account: newAccount,
      folders: [],
      loading: false,
      syncing: false,
      error: null,
      lastSync: null,
    })

    // Start sync in background (don't await - let dialog close immediately)
    this.syncAccount(newAccount.id).catch(err => {
      console.error('Initial sync failed:', err)
    })

    return newAccount
  }

  /**
   * Add a new OAuth account
   * This uses CompleteOAuthAccountSetup which creates the account AND saves the OAuth tokens
   * that were stored temporarily during the OAuth flow.
   */
  async addOAuthAccount(provider: string, email: string, accountName: string, displayName: string, color: string): Promise<account.Account> {
    // CompleteOAuthAccountSetup creates the account with correct IMAP/SMTP settings
    // and saves the OAuth tokens from pendingOAuthTokens
    const newAccount = await CompleteOAuthAccountSetup(provider, email, accountName, displayName, color)

    // Add to local state
    this.accounts.push({
      account: newAccount,
      folders: [],
      loading: false,
      syncing: false,
      error: null,
      lastSync: null,
    })

    // Start sync in background (don't await - let dialog close immediately)
    this.syncAccount(newAccount.id).catch(err => {
      console.error('Initial sync failed:', err)
    })

    return newAccount
  }

  /**
   * Update an existing account
   */
  async updateAccount(
    id: string,
    config: account.AccountConfig
  ): Promise<account.Account> {
    const updatedAccount = await UpdateAccount(id, config)

    // Update local state
    const index = this.accounts.findIndex((a) => a.account.id === id)
    if (index !== -1) {
      this.accounts[index].account = updatedAccount
    }

    return updatedAccount
  }

  /**
   * Remove an account
   */
  async removeAccount(id: string): Promise<void> {
    await RemoveAccount(id)

    // Remove from local state
    const index = this.accounts.findIndex((a) => a.account.id === id)
    if (index !== -1) {
      this.accounts.splice(index, 1)
    }

    // Clear selection if this account was selected
    if (this.selectedFolder?.accountId === id) {
      this.selectedFolder = null
    }
  }

  /**
   * Test connection with provided config
   */
  async testConnection(config: account.AccountConfig): Promise<app.ConnectionTestResult> {
    return await TestConnection(config)
  }

  /**
   * Select a folder
   */
  selectFolder(
    accountId: string,
    folderId: string,
    folderPath: string,
    folderName: string
  ): void {
    this.selectedFolder = {
      accountId,
      folderId,
      folderPath,
      folderName,
    }
  }

  /**
   * Get account by ID
   */
  getAccount(id: string): AccountWithFolders | undefined {
    return this.accounts.find((a) => a.account.id === id)
  }

  /**
   * Check if any account is syncing
   */
  get isAnySyncing(): boolean {
    return this.accounts.some((a) => a.syncing)
  }

  /**
   * Get the most recent sync time across all accounts
   */
  get lastSyncTime(): Date | null {
    const syncs = this.accounts
      .map((a) => a.lastSync)
      .filter((d): d is Date => d !== null)

    if (syncs.length === 0) return null
    return new Date(Math.max(...syncs.map((d) => d.getTime())))
  }

  /**
   * Get sync progress for an account.
   * Returns the folder with the LOWEST percentage (most behind) among all syncing folders.
   */
  getSyncProgress(accountId: string): SyncProgress | null {
    const accountProgress = this.syncProgress[accountId]
    if (!accountProgress) return null

    const folders = Object.values(accountProgress)
    if (folders.length === 0) return null

    // Return the folder with the LOWEST percentage (most behind)
    return folders.reduce((lowest, current) =>
      current.percentage < lowest.percentage ? current : lowest
    )
  }

  /**
   * Get sync error for an account.
   * Returns error info or null if no error.
   */
  getSyncError(accountId: string): { folderId: string; error: string } | null {
    return this.syncErrors[accountId] ?? null
  }

  /**
   * Clear sync error for an account.
   * Called when user triggers a new sync (error should clear on retry).
   */
  clearSyncError(accountId: string): void {
    if (this.syncErrors[accountId]) {
      delete this.syncErrors[accountId]
      this.syncErrors = { ...this.syncErrors }
    }
  }

  /**
   * Reorder accounts by providing the new order of account IDs.
   * Updates backend and reloads accounts to reflect new order.
   */
  async reorderAccounts(ids: string[]): Promise<void> {
    await ReorderAccounts(ids)
    await this.load()
  }
}

// Export singleton instance
export const accountStore = new AccountStore()
