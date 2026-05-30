<script lang="ts">
  import Icon from '@iconify/svelte'
  import * as Dialog from '$lib/components/ui/dialog'
  import * as Select from '$lib/components/ui/select'
  import { Button } from '$lib/components/ui/button'
  // @ts-ignore - wailsjs path
  import { folder, account } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { GetFolders } from '../../../../wailsjs/go/app/App'
  import { _ } from '$lib/i18n'
  import { getMoveSuggestions } from '$lib/stores/moveSuggestions'

  interface Props {
    open: boolean
    title: string
    initialAccountId: string
    accounts: account.Account[]
    excludeFolderId?: string
    senderEmail?: string
    onSelect: (folderId: string, folderName: string, accountId: string, folderPath?: string) => void
  }

  let {
    open = $bindable(false),
    title,
    initialAccountId,
    accounts,
    excludeFolderId = '',
    senderEmail = '',
    onSelect,
  }: Props = $props()

  let focusedIndex = $state(-1)
  let active = $state(false)
  let listEl: HTMLDivElement | undefined = $state()
  let searchQuery = $state('')
  let searchInput: HTMLInputElement | null = $state(null)

  // Account state — dropdown selection drives folder loading. Initialized on
  // dialog open (rather than at $state declaration) so prop changes between
  // opens are picked up.
  let selectedAccountId = $state('')
  let folders = $state<folder.Folder[]>([])
  let foldersLoading = $state(false)
  // Tracks the load that produced the current folders so out-of-order responses don't overwrite a newer load
  let loadVersion = 0

  // Reset selected account when the dialog (re)opens for a new context
  $effect(() => {
    if (open) selectedAccountId = initialAccountId
  })

  // Load folders whenever the selected account changes
  $effect(() => {
    const accountId = selectedAccountId
    if (!accountId) return
    const myVersion = ++loadVersion
    foldersLoading = true
    GetFolders(accountId)
      .then((result: folder.Folder[]) => {
        if (myVersion !== loadVersion) return
        folders = result || []
      })
      .catch((err: unknown) => {
        if (myVersion !== loadVersion) return
        console.error('Failed to load folders:', err)
        folders = []
      })
      .finally(() => {
        if (myVersion !== loadVersion) return
        foldersLoading = false
      })
  })

  // Special vs custom split (same logic MessageContextMenu used to apply)
  const specialFolderTypes = ['inbox', 'sent', 'drafts', 'archive', 'trash', 'spam', 'all']
  const availableFolders = $derived(
    folders.filter((f) => {
      // Only exclude the source folder when the dropdown is on the source account
      if (selectedAccountId === initialAccountId && f.id === excludeFolderId) return false
      return true
    })
  )
  const specialFolders = $derived(availableFolders.filter((f) => specialFolderTypes.includes(f.type)))
  const customFolders = $derived(availableFolders.filter((f) => !specialFolderTypes.includes(f.type)))

  // Combine and sort all folders by path for hierarchy display
  const allFolders = $derived(
    [...specialFolders, ...customFolders].sort((a, b) => a.path.localeCompare(b.path))
  )

  const suggestedFolderIds = $derived(
    new Set(
      getMoveSuggestions(senderEmail, allFolders, selectedAccountId)
        .map((suggestion) => suggestion.folderId)
    )
  )

  const suggestedFolders = $derived(
    getMoveSuggestions(senderEmail, allFolders, selectedAccountId)
      .map((suggestion) => allFolders.find((f) => f.id === suggestion.folderId))
      .filter((f): f is folder.Folder => !!f)
  )

  // Detect the IMAP delimiter from folder paths
  const delimiter = $derived(() => {
    for (const f of allFolders) {
      if (f.path.includes('/')) return '/'
      if (f.path.includes('.')) return '.'
    }
    return '/'
  })

  // Calculate depth for each folder based on path separators
  function getDepth(path: string): number {
    const d = delimiter()
    // Don't count depth for paths like [Gmail]/Sent — treat [Gmail] prefix as depth 0
    const normalized = path.replace(/^\[.*?\]\//, '')
    if (!normalized.includes(d)) return 0
    return normalized.split(d).length - 1
  }

  // Format path for search results (readable breadcrumb)
  function formatPath(path: string): string {
    const d = delimiter()
    return path.replace(/\[.*?\]\//g, '').split(d).join(' / ')
  }

  // Filtered folders when searching
  const isSearching = $derived(searchQuery.trim().length > 0)
  const displayFolders = $derived(() => {
    if (!isSearching) {
      return [
        ...suggestedFolders,
        ...allFolders.filter((f) => !suggestedFolderIds.has(f.id)),
      ]
    }
    const query = searchQuery.trim().toLowerCase()
    return allFolders.filter(f =>
      f.name.toLowerCase().includes(query) || f.path.toLowerCase().includes(query)
    )
  })

  // Reset state when dialog opens/closes
  $effect(() => {
    if (!open) {
      active = false
      searchQuery = ''
      return
    }
    const folders = displayFolders()
    focusedIndex = folders.length > 0 ? 0 : -1
    active = false
    const timer = setTimeout(() => {
      active = true
      searchInput?.focus()
    }, 0)
    return () => clearTimeout(timer)
  })

  // Reset focus when search changes or folders change (e.g., account switch)
  $effect(() => {
    void searchQuery
    void allFolders
    const folders = displayFolders()
    focusedIndex = folders.length > 0 ? 0 : -1
  })

  // Scroll focused item into view
  $effect(() => {
    if (focusedIndex < 0 || !listEl) return
    const buttons = listEl.querySelectorAll('button')
    buttons[focusedIndex]?.scrollIntoView({ block: 'nearest' })
  })

  const folderIcons: Record<string, string> = {
    inbox: 'mdi:inbox',
    sent: 'mdi:send',
    drafts: 'mdi:file-document-edit-outline',
    trash: 'mdi:delete-outline',
    archive: 'mdi:archive-outline',
    spam: 'mdi:alert-octagon-outline',
    all: 'mdi:email-multiple-outline',
    starred: 'mdi:star-outline',
    folder: 'mdi:folder-outline',
  }

  function getFolderIcon(type: string): string {
    return folderIcons[type] || folderIcons.folder
  }

  function handleKeydown(e: KeyboardEvent) {
    if (!active) return
    const folders = displayFolders()
    if (folders.length === 0) return

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        e.stopPropagation()
        focusedIndex = (focusedIndex + 1) % folders.length
        break
      case 'ArrowUp':
        e.preventDefault()
        e.stopPropagation()
        focusedIndex = (focusedIndex - 1 + folders.length) % folders.length
        break
      case 'Enter':
        e.preventDefault()
        e.stopPropagation()
        if (focusedIndex >= 0 && focusedIndex < folders.length) {
          const f = folders[focusedIndex]
          onSelect(f.id, f.name, selectedAccountId, f.path)
        }
        break
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-sm shadow-2xl" showOverlay={false}>
    <Dialog.Header>
      <Dialog.Title>{title}</Dialog.Title>
    </Dialog.Header>

    <!-- Search input -->
    <div class="relative">
      <Icon icon="mdi:magnify" class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
      <input
        bind:this={searchInput}
        bind:value={searchQuery}
        placeholder={$_('contextMenu.searchFolders')}
        class="flex h-10 w-full rounded-md border border-input bg-background pl-9 pr-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
      />
    </div>

    <!-- Account dropdown — switching reloads the folder list -->
    {#if accounts.length > 1}
      {@const selectedAccount = accounts.find(a => a.id === selectedAccountId)}
      <Select.Root
        value={selectedAccountId}
        onValueChange={(v) => { if (v) selectedAccountId = v }}
      >
        <Select.Trigger>
          <Select.Value placeholder={$_('contextMenu.account')}>
            {selectedAccount?.name ?? ''}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each accounts as acc (acc.id)}
            <Select.Item value={acc.id} label={acc.name} />
          {/each}
        </Select.Content>
      </Select.Root>
    {/if}

    <div
      class="border border-border rounded-md max-h-64 overflow-y-auto"
      bind:this={listEl}
      role="listbox"
    >
      {#if foldersLoading}
        <div class="flex items-center gap-2 p-3 text-muted-foreground text-sm">
          <Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />
          {$_('common.loading')}
        </div>
      {:else if displayFolders().length === 0}
        <div class="p-3 text-sm text-muted-foreground">
          {$_('contextMenu.noFoldersAvailable')}
        </div>
      {:else}
        {#each displayFolders() as f, i (f.id)}
          {@const depth = isSearching ? 0 : getDepth(f.path)}
          <button
            type="button"
            role="option"
            aria-selected={i === focusedIndex}
            class="w-full flex items-center gap-2 py-2 pr-3 text-left text-sm hover:bg-muted/50 transition-colors {i === focusedIndex ? 'bg-muted/50' : ''}"
            style="padding-left: {12 + depth * 16}px"
            onclick={() => onSelect(f.id, f.name, selectedAccountId, f.path)}
          >
            <Icon icon={getFolderIcon(f.type)} class="h-4 w-4 shrink-0" />
            {#if isSearching}
              <span class="truncate">{formatPath(f.path)}</span>
            {:else}
              <span class="truncate">{f.name}</span>
            {/if}
            {#if !isSearching && suggestedFolderIds.has(f.id)}
              <span class="ml-auto text-[11px] text-primary">{$_('contextMenu.suggested')}</span>
            {/if}
          </button>
        {/each}
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="destructive" onclick={() => (open = false)}>
        {$_('common.cancel')}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
