<script lang="ts">
  import Icon from '@iconify/svelte'
  import { formatRelativeDate } from '$lib/utils/date'
  import { _ } from '$lib/i18n'
  // @ts-ignore - wailsjs path
  import { message } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { Star, Unstar } from '../../../../wailsjs/go/app/App'
  import MessageContextMenu from '$lib/components/common/MessageContextMenu.svelte'
  import { toasts } from '$lib/stores/toast'
  import { getAccentBarUnread, getShowMessageListCircles } from '$lib/stores/settings.svelte'

  interface Props {
    conversation: message.Conversation
    density?: 'micro' | 'compact' | 'standard' | 'large'
    selected: boolean
    checked: boolean
    accountId: string
    folderId: string
    folderType: string
    selectedMessageIds: string[]  // All message IDs from checked conversations (for multi-select)
    selectedIsStarred: boolean    // Aggregated star state for multi-select
    selectedIsRead: boolean       // Aggregated read state for multi-select
    showAccountIndicator?: boolean  // Show account color dot in unified inbox view
    accountColor?: string           // Account color for the indicator
    accountName?: string            // Account name for tooltip
    highlightedSubject?: string     // Subject with <mark> tags for search highlighting
    highlightedSnippet?: string     // Snippet with <mark> tags for search highlighting
    highlightedFromName?: string    // From name with <mark> tags for search highlighting
    searchFolderName?: string       // Folder name to display in search results
    searchFolderType?: string       // Folder type for icon in search results
    isNonLocal?: boolean            // Show cloud icon for non-local server search results
    onSelect: (e?: MouseEvent) => void
    onCheck: (checked: boolean, event?: MouseEvent) => void
    onClearSelection: () => void  // Clear multi-select when right-clicking unchecked row
    onActionComplete?: (autoSelectNext?: boolean) => void
    onReply?: (mode: 'reply' | 'reply-all' | 'forward', messageId: string) => void
  }

  let {
    conversation,
    density = 'standard',
    selected,
    checked,
    accountId,
    folderId,
    folderType,
    selectedMessageIds,
    selectedIsStarred,
    selectedIsRead,
    showAccountIndicator = false,
    accountColor = '',
    accountName = '',
    highlightedSubject = '',
    highlightedSnippet = '',
    highlightedFromName = '',
    searchFolderName = '',
    searchFolderType: _searchFolderType = '',
    isNonLocal = false,
    onSelect,
    onCheck,
    onClearSelection,
    onActionComplete,
    onReply,
  }: Props = $props()

  // Check if we're in search mode (have highlighted content)
  const isSearchResult = $derived(!!highlightedSubject || !!highlightedSnippet)

  // Density-based class mappings
  // micro = smallest (power users), compact = small, standard = default, large = accessibility
  const densityClasses = {
    row: {
      micro: 'px-3 py-2 gap-2',
      compact: 'px-4 py-3 gap-3',
      standard: 'px-5 py-4 gap-4',
      large: 'px-6 py-5 gap-5',
    },
    avatar: {
      micro: 'w-8 h-8 text-xs',
      compact: 'w-10 h-10 text-sm',
      standard: 'w-12 h-12 text-base',
      large: 'w-14 h-14 text-lg',
    },
    senderText: {
      micro: 'text-xs',
      compact: 'text-sm',
      standard: 'text-base',
      large: 'text-lg',
    },
    text: {
      micro: 'text-[10px]',
      compact: 'text-xs',
      standard: 'text-sm',
      large: 'text-base',
    },
    dateText: {
      micro: 'text-[10px]',
      compact: 'text-xs',
      standard: 'text-sm',
      large: 'text-base',
    },
    icon: {
      micro: 'w-3 h-3',
      compact: 'w-3.5 h-3.5',
      standard: 'w-4 h-4',
      large: 'w-5 h-5',
    },
    starIcon: {
      micro: 'w-3.5 h-3.5',
      compact: 'w-4 h-4',
      standard: 'w-5 h-5',
      large: 'w-6 h-6',
    },
    badge: {
      micro: 'px-1 py-0 text-[10px]',
      compact: 'px-1.5 py-0.5 text-xs',
      standard: 'px-2 py-1 text-xs',
      large: 'px-2.5 py-1 text-sm',
    },
    checkbox: {
      micro: 'w-4 h-4',
      compact: 'w-5 h-5',
      standard: 'w-6 h-6',
      large: 'w-7 h-7',
    },
    checkboxInner: {
      micro: 'w-3 h-3',
      compact: 'w-4 h-4',
      standard: 'w-5 h-5',
      large: 'w-6 h-6',
    },
    checkIcon: {
      micro: 'w-2 h-2',
      compact: 'w-3 h-3',
      standard: 'w-4 h-4',
      large: 'w-5 h-5',
    },
  }

  // Get display name for participants
  function getParticipantNames(): string {
    if (!conversation.participants || conversation.participants.length === 0) {
      return $_('viewer.unknown')
    }

    const names = conversation.participants.map((p) => p.name || p.email.split('@')[0])

    if (names.length === 1) {
      return names[0]
    } else if (names.length === 2) {
      return names.join(', ')
    } else {
      return `${names[0]}, ${names[1]} +${names.length - 2}`
    }
  }

  function getInitials(conv: message.Conversation): string {
    if (!conv.participants || conv.participants.length === 0) {
      return '?'
    }
    const first = conv.participants[0]
    const name = first.name || first.email
    return name
      .split(' ')
      .map((n) => n[0])
      .join('')
      .toUpperCase()
      .slice(0, 2)
  }

  function getAvatarColor(conv: message.Conversation): string {
    const colors = [
      'bg-red-500',
      'bg-orange-500',
      'bg-amber-500',
      'bg-yellow-500',
      'bg-lime-500',
      'bg-green-500',
      'bg-emerald-500',
      'bg-teal-500',
      'bg-cyan-500',
      'bg-sky-500',
      'bg-blue-500',
      'bg-indigo-500',
      'bg-violet-500',
      'bg-purple-500',
      'bg-fuchsia-500',
      'bg-pink-500',
    ]
    const email = conv.participants?.[0]?.email || conv.threadId
    let hash = 0
    for (let i = 0; i < email.length; i++) {
      hash = email.charCodeAt(i) + ((hash << 5) - hash)
    }
    return colors[Math.abs(hash) % colors.length]
  }

  async function handleStarClick(e: MouseEvent) {
    e.stopPropagation()
    const starring = !conversation.isStarred
    try {
      if (starring) {
        await Star(ownMessageIds)
        toasts.success($_('toast.starred'))
      }
      if (!starring) {
        await Unstar(ownMessageIds)
        toasts.success($_('toast.starRemoved'))
      }
      onActionComplete?.()
    } catch (err) {
      console.error('Star toggle failed:', err)
      toasts.error($_('toast.failedToUpdateStar'))
    }
  }

  function handleCheckboxClick(e: MouseEvent) {
    e.stopPropagation()
    onCheck(!checked, e)
  }

  const hasUnread = $derived((conversation.unreadCount || 0) > 0)

  // Get message IDs from the conversation for context menu
  // Use messageIds field (populated by ListConversationsByFolder), fallback to messages array
  const ownMessageIds = $derived(
    conversation.messageIds || conversation.messages?.map((m) => m.id) || []
  )

  // Determine star/read state from this conversation
  const ownIsStarred = $derived(conversation.isStarred ?? false)
  const ownIsRead = $derived((conversation.unreadCount || 0) === 0)

  // Context menu state - determines whether to use multi-select or single row
  let useMultiSelect = $state(false)

  // Handle right-click to determine context menu behavior
  function handleContextMenu() {
    if (checked) {
      // This row is part of multi-select - use all selected message IDs
      useMultiSelect = true
    } else {
      // This row is NOT checked - clear selection and act on this row only
      onClearSelection()
      useMultiSelect = false
    }
  }

  // Computed values for context menu based on selection state
  const contextMenuMessageIds = $derived(useMultiSelect ? selectedMessageIds : ownMessageIds)
  const contextMenuIsStarred = $derived(useMultiSelect ? selectedIsStarred : ownIsStarred)
  const contextMenuIsRead = $derived(useMultiSelect ? selectedIsRead : ownIsRead)
</script>

<MessageContextMenu
  messageIds={contextMenuMessageIds}
  {accountId}
  currentFolderId={folderId}
  {folderType}
  isStarred={contextMenuIsStarred}
  isRead={contextMenuIsRead}
  {onActionComplete}
  onReply={useMultiSelect ? undefined : onReply}
  onOpenChange={(open: boolean) => { if (open) handleContextMenu() }}
>
  <div
    data-conversation-row
    class="group w-full flex items-start {densityClasses.row[density]} text-left border-b border-border transition-colors duration-300 cursor-pointer outline-none {selected
      ? 'bg-primary/20'
      : 'hover:bg-muted/50'} {getAccentBarUnread() && hasUnread ? 'border-l-2 border-l-primary' : ''}"
    onclick={(e) => onSelect(e)}
    onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect() }}}
    role="button"
    tabindex="0"
  >
    <!-- Checkbox (visible on hover or when checked) -->
    <div
      class="{densityClasses.checkbox[density]} flex-shrink-0 flex items-center justify-center self-center {checked
        ? 'opacity-100'
        : 'opacity-0 group-hover:opacity-40 hover:!opacity-100 max-[767px]:opacity-40 max-[767px]:active:opacity-100'} transition-opacity duration-200"
    >
      <button
        class="{densityClasses.checkboxInner[density]} rounded border {checked
          ? 'bg-primary border-primary'
          : 'border-muted-foreground hover:border-primary'} flex items-center justify-center transition-colors duration-200"
        onclick={handleCheckboxClick}
      >
        {#if checked}
          <Icon icon="mdi:check" class="{densityClasses.checkIcon[density]} text-primary-foreground" />
        {/if}
      </button>
    </div>

    <!-- Sender circle (colored, with initials) -->
    {#if getShowMessageListCircles()}
      <div
        class="{densityClasses.avatar[density]} rounded-full flex-shrink-0 flex items-center justify-center text-white font-medium {getAvatarColor(
          conversation
        )}"
      >
        {getInitials(conversation)}
      </div>
    {/if}

    <!-- Content -->
    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-2 mb-0.5">
        <!-- Account Indicator (for unified inbox) -->
        {#if showAccountIndicator && accountColor}
          <span
            class="w-2 h-2 rounded-full flex-shrink-0"
            style="background-color: {accountColor}"
            title={accountName}
          ></span>
        {/if}

        <!-- Participant Names (with highlighting if in search mode) -->
        {#if highlightedFromName}
          <span class="{densityClasses.senderText[density]} truncate {hasUnread ? 'font-semibold text-foreground' : 'text-foreground'}">
            <!-- eslint-disable-next-line svelte/no-at-html-tags -- highlightMatches only inserts <mark> around already-escaped text -->
            {@html highlightedFromName}
          </span>
        {:else}
          <span class="{densityClasses.senderText[density]} truncate {hasUnread ? 'font-semibold text-foreground' : 'text-foreground'}">
            {getParticipantNames()}
          </span>
        {/if}

        <!-- Message Count Badge -->
        {#if conversation.messageCount > 1}
          <span
            class="flex-shrink-0 {densityClasses.badge[density]} rounded-full bg-muted text-muted-foreground"
          >
            {conversation.messageCount}
          </span>
        {/if}

        <!-- Folder Badge (for search results) -->
        {#if isSearchResult && searchFolderName}
          <span
            class="flex-shrink-0 {densityClasses.badge[density]} rounded bg-muted/50 text-muted-foreground flex items-center gap-1"
            title={$_('messageList.foundIn', { values: { folder: searchFolderName } })}
          >
            <Icon icon="mdi:folder-outline" class="w-3 h-3" />
            {searchFolderName}
          </span>
        {/if}

        <!-- Indicators -->
        <div class="flex items-center gap-1 flex-shrink-0">
          {#if isNonLocal}
            <span title={$_('search.notSyncedLocally')}>
              <Icon icon="mdi:cloud-outline" class="{densityClasses.icon[density]} text-muted-foreground" />
            </span>
          {/if}
          {#if conversation.hasAttachments}
            <Icon icon="mdi:paperclip" class="{densityClasses.icon[density]} text-muted-foreground" />
          {/if}
        </div>

        <!-- Date -->
        <span class="{densityClasses.dateText[density]} text-muted-foreground flex-shrink-0 ml-auto">
          {formatRelativeDate(new Date(conversation.latestDate))}
        </span>
      </div>

      <!-- Subject (with highlighting if in search mode) -->
      {#if highlightedSubject}
        <p
          class="truncate {densityClasses.text[density]} {hasUnread ? 'font-medium text-foreground' : 'text-muted-foreground'}"
        >
          <!-- eslint-disable-next-line svelte/no-at-html-tags -- highlightMatches only inserts <mark> around already-escaped text -->
          {@html highlightedSubject}
        </p>
      {:else}
        <p
          class="truncate {densityClasses.text[density]} {hasUnread ? 'font-medium text-foreground' : 'text-muted-foreground'}"
        >
          {conversation.subject || $_('viewer.noSubject')}
        </p>
      {/if}

      <!-- Snippet (with highlighting if in search mode) -->
      {#if highlightedSnippet}
        <p class="truncate {densityClasses.text[density]} text-muted-foreground">
          <!-- eslint-disable-next-line svelte/no-at-html-tags -- highlightMatches only inserts <mark> around already-escaped text -->
          {@html highlightedSnippet}
        </p>
      {:else if conversation.snippet}
        <p class="truncate {densityClasses.text[density]} text-muted-foreground">
          {conversation.snippet}
        </p>
      {:else if conversation.isEncrypted}
        <p class="truncate {densityClasses.text[density]} text-muted-foreground italic">
          {$_('messageList.encryptedContent')}
        </p>
      {:else}
        <p class="truncate {densityClasses.text[density]} text-muted-foreground italic">
          {$_('messageList.noContent')}
        </p>
      {/if}
    </div>

    <!-- Star -->
    <button
      class="flex-shrink-0 p-1 -mr-1 rounded hover:bg-muted transition-colors duration-200"
      onclick={handleStarClick}
    >
      <Icon
        icon={conversation.isStarred ? 'mdi:star' : 'mdi:star-outline'}
        class="{densityClasses.starIcon[density]} {conversation.isStarred ? 'text-yellow-500' : 'text-muted-foreground'}"
      />
    </button>
  </div>
</MessageContextMenu>
