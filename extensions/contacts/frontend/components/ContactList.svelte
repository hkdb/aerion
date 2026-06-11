<script lang="ts">
  // ContactList — mirrors mail's MessageList toolbar pattern:
  //
  //   [Title  |  Search input (when open)]      [Search]  [Sort]
  //
  // Search bar is HIDDEN by default and toggled open via the search button
  // or Ctrl+S. toggleSearchFocus() is exposed to ListPane's onFocusSearch
  // so the global Ctrl+S handler cycles through the same three states mail
  // uses: closed → focused → closed.
  //
  // Sort is local component state (A-Z / Z-A). Lists are small enough that
  // sorting client-side via $derived is cheaper than a backend round-trip.
  // Filter UI is intentionally omitted — comes when contacts gains tags or
  // groups in a later phase.

  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import ListPane from '$lib/components/kit/ListPane.svelte'
  import ListRow from '$lib/components/kit/ListRow.svelte'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  import ConfirmDialog from '$lib/components/kit/ConfirmDialog.svelte'
  import { contactsView, reloadContacts, focusContact, activateContact, setSearchQuery, deleteLocalContact } from '$extensions/contacts/frontend/stores/contactsView.svelte'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { toasts } from '$lib/stores/toast'
  import WriteAccessBanner from './WriteAccessBanner.svelte'
  // Canonical list toolbar — owns hamburger placement, title styling, count
  // badge, search-mode swap. Extension just supplies label/count + per-extension
  // search markup + trailing action buttons.
  import ListHeader from '$lib/components/kit/ListHeader.svelte'
  // @ts-ignore - wailsjs bindings
  import type { v1 } from '$wailsjs/go/models'

  interface Props {
    onAdd?: () => void
  }

  let { onAdd }: Props = $props()

  type SortOrder = 'name-asc' | 'name-desc'

  let showSearch = $state(false)
  let searchInput = $state('')
  // Plain `let` (not $state) — same as App.svelte's component refs. The
  // ref is only read inside event handlers (focus / blur / select / equality
  // check against document.activeElement), never in a reactive context, so
  // making it $state adds overhead without benefit.
  // svelte-ignore non_reactive_update
  let searchInputEl: HTMLInputElement | null = null
  let sortOrder = $state<SortOrder>('name-asc')

  // Delete-confirmation state for keyboard-triggered deletes. ContactDetail
  // has its own button-triggered confirm dialog; this one fires when the user
  // has the LIST focused and hits Delete/Backspace on the highlighted row.
  // Mirrors ContactDetail's writability gate: local always writable; CardDAV
  // gated on the source's writable flag.
  let showDeleteConfirm = $state(false)
  let pendingDelete = $state<v1.Contact | null>(null)
  let deleting = $state(false)

  function requestDelete(id: string) {
    const found = contactsView.contacts.find(c => c.id === id)
    if (!found) return
    const writable =
      found.sourceId === 'aerion' || contactSourcesStore.isSourceWritable(found.sourceId)
    if (!writable) return
    pendingDelete = found
    showDeleteConfirm = true
  }

  async function confirmDelete() {
    if (!pendingDelete) return
    deleting = true
    try {
      await deleteLocalContact(pendingDelete!.id)
      toasts.success($_('contacts.toast.deleted'))
    } catch (err) {
      console.error('Failed to delete contact:', err)
      toasts.error($_('contacts.toast.failedDelete'))
    } finally {
      deleting = false
      pendingDelete = null
    }
  }

  let debounce: ReturnType<typeof setTimeout> | null = null
  function onSearchInput(e: Event) {
    searchInput = (e.currentTarget as HTMLInputElement).value
    if (debounce) clearTimeout(debounce)
    debounce = setTimeout(() => {
      setSearchQuery(searchInput)
      reloadContacts()
    }, 200)
  }

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      // Match mail: Enter blurs the input and hands focus to the list so j/k
      // navigation works immediately on filtered results.
      e.preventDefault()
      searchInputEl?.blur()
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      clearSearch()
    }
  }

  function clearSearch() {
    searchInput = ''
    setSearchQuery('')
    showSearch = false
    if (debounce) clearTimeout(debounce)
    reloadContacts()
  }

  // Three-state Ctrl+S toggle (matches MessageList.toggleSearchFocus):
  //   closed                  → open + focus
  //   open but unfocused      → focus
  //   open and focused        → close
  function toggleSearchFocus() {
    if (!showSearch) {
      showSearch = true
      setTimeout(() => {
        searchInputEl?.focus()
        searchInputEl?.select()
      }, 50)
      return
    }
    if (document.activeElement !== searchInputEl) {
      searchInputEl?.focus()
      searchInputEl?.select()
      return
    }
    clearSearch()
  }

  function toggleSort() {
    sortOrder = sortOrder === 'name-asc' ? 'name-desc' : 'name-asc'
  }

  function primaryEmail(c: v1.Contact): string {
    return c.emails && c.emails.length > 0 ? c.emails[0] : ''
  }

  function rowKey(c: v1.Contact): string {
    return (c.name || primaryEmail(c) || '').toLowerCase()
  }

  // Client-side sort of the already-loaded list. Backend handles query
  // filtering; sort is purely a view concern.
  const sortedContacts = $derived.by(() => {
    const items = [...contactsView.contacts]
    const dir = sortOrder === 'name-asc' ? 1 : -1
    items.sort((a, b) => {
      const ka = rowKey(a)
      const kb = rowKey(b)
      if (ka < kb) return -1 * dir
      if (ka > kb) return 1 * dir
      return 0
    })
    return items
  })

  // Header label tracks the sidebar's selected category — mirrors mail's
  // MessageList showing the active folder name. Local sub-sources reuse the
  // sidebar i18n keys (so labels stay consistent). CardDAV sources resolve
  // to the user-given source name; unknown ids fall back to the generic
  // "Contacts" label so the header is never empty.
  const headerLabel = $derived.by(() => {
    const sel = contactsView.selectedSourceId
    if (sel === '') return $_('contacts.sidebar.all')
    if (sel === 'local') return $_('contacts.sidebar.localAll')
    if (sel === 'local:manual') return $_('contacts.sidebar.localManual')
    if (sel === 'local:collected') return $_('contacts.sidebar.localCollected')
    const src = contactSourcesStore.sources.find(s => s.id === sel)
    return src?.name || $_('contacts.list.header')
  })
</script>

<div class="flex-1 min-w-0 min-h-0 flex flex-col border-r border-border bg-background">
  <ListHeader
    label={headerLabel}
    count={contactsView.contacts.length}
    searchMode={showSearch}
  >
    {#snippet search()}
      <div class="flex items-center gap-1 bg-muted rounded-md px-2 flex-1 min-w-0">
        <Icon icon="mdi:magnify" class="w-4 h-4 text-muted-foreground flex-shrink-0" />
        <input
          bind:this={searchInputEl}
          type="text"
          placeholder={$_('contacts.list.searchPlaceholder')}
          class="bg-transparent border-none outline-none text-sm py-1.5 w-full min-w-[200px] text-foreground"
          value={searchInput}
          oninput={onSearchInput}
          onkeydown={handleSearchKeydown}
        />
        {#if searchInput}
          <button
            onclick={clearSearch}
            class="p-0.5 hover:bg-muted-foreground/20 rounded"
            title={$_('contacts.list.searchClear')}
            type="button"
          >
            <Icon icon="mdi:close" class="w-4 h-4 text-muted-foreground" />
          </button>
        {/if}
      </div>
    {/snippet}

    {#snippet actions()}
      <button
        class="p-2 rounded-md hover:bg-muted transition-colors {showSearch ? 'bg-muted' : ''}"
        title={showSearch ? $_('contacts.list.searchClose') : $_('contacts.list.searchOpen')}
        onclick={toggleSearchFocus}
        type="button"
      >
        <Icon icon={showSearch ? 'mdi:close' : 'mdi:magnify'} class="w-5 h-5 text-muted-foreground" />
      </button>
      <button
        class="p-2 rounded-md hover:bg-muted transition-colors"
        title={sortOrder === 'name-asc' ? $_('contacts.list.sortAsc') : $_('contacts.list.sortDesc')}
        onclick={toggleSort}
        type="button"
      >
        <Icon
          icon={sortOrder === 'name-asc' ? 'mdi:sort-alphabetical-ascending' : 'mdi:sort-alphabetical-descending'}
          class="w-5 h-5 text-muted-foreground"
        />
      </button>
      {#if onAdd}
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('contacts.list.addTooltip')}
          onclick={onAdd}
          type="button"
        >
          <Icon icon="mdi:plus" class="w-5 h-5 text-muted-foreground" />
        </button>
      {/if}
    {/snippet}
  </ListHeader>

  <WriteAccessBanner />

  <ListPane
    items={sortedContacts}
    selectedId={contactsView.selectedContactId}
    focusSlot="messageList"
    label={$_('contacts.list.label')}
    loading={contactsView.loading}
    onSelect={(id) => focusContact(id)}
    onActivate={(id) => activateContact(id)}
    onDelete={requestDelete}
    onFocusSearch={toggleSearchFocus}
  >
    {#snippet row(c: v1.Contact, { selected })}
      <ListRow {selected} onclick={() => activateContact(c.id)}>
        <Avatar email={primaryEmail(c)} name={c.name} density="standard" />
        <span class="flex flex-col min-w-0 flex-1">
          <span class="font-medium truncate text-foreground">{c.name || primaryEmail(c) || $_('contacts.common.unnamed')}</span>
          {#if primaryEmail(c) && primaryEmail(c) !== c.name}
            <span class="text-xs text-muted-foreground truncate">{primaryEmail(c)}</span>
          {/if}
        </span>
      </ListRow>
    {/snippet}

    {#snippet empty()}
      <p class="m-4 text-sm text-muted-foreground">
        {searchInput ? $_('contacts.list.emptySearch') : $_('contacts.list.empty')}
      </p>
    {/snippet}
  </ListPane>
</div>

<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_('contacts.delete.title')}
  description={pendingDelete
    ? $_(
        pendingDelete.sourceId === 'aerion'
          ? 'contacts.delete.descriptionLocal'
          : 'contacts.delete.descriptionCardDAV',
        {
          values: {
            name: pendingDelete.name || (pendingDelete.emails && pendingDelete.emails[0]) || $_('contacts.common.unnamed'),
          },
        },
      )
    : ''}
  confirmLabel={$_('contacts.common.delete')}
  cancelLabel={$_('contacts.common.cancel')}
  variant="destructive"
  loading={deleting}
  onConfirm={confirmDelete}
/>
