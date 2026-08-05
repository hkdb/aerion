<script module lang="ts">
  // Shared across all row instances so one wheel-swipe's momentum tail
  // can't fire a second toggle on a neighboring row.
  let wheelCooldownUntil = 0
  // Last vertical-dominant wheel event anywhere in the list — shared because
  // scrolling moves different rows under the pointer. Horizontal deltas are
  // ignored until the list has been vertically quiet for a beat, so scroll
  // residue can't accumulate into a swipe.
  let lastVerticalWheelAt = 0
</script>

<script lang="ts">
  import Icon from '@iconify/svelte'
  import { formatRelativeDate } from '$lib/utils/date'
  import { _ } from '$lib/i18n'
  // @ts-ignore - wailsjs path
  import { message } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { Star, Unstar } from '../../../../wailsjs/go/app/App'
  import MessageContextMenu from '$lib/components/common/MessageContextMenu.svelte'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  import { toasts } from '$lib/stores/toast'
  import { getAccentBarUnread, getShowMessageListCircles, getShowMessageListProfilePics, getAlwaysShowMessageCheckbox } from '$lib/stores/settings.svelte'
  import { getLayoutMode } from '$lib/stores/layout.svelte'
  import { contactPhotos } from '$lib/stores/contactPhotos.svelte'

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
    onDelete?: (messageIds: string[]) => void  // Swipe-left delete (routes through MessageList.requestDelete)
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
    onDelete,
  }: Props = $props()

  // Check if we're in search mode (have highlighted content)
  const isSearchResult = $derived(!!highlightedSubject || !!highlightedSnippet)

  // Forward keyboard access (Alt+M / Alt+C) to this row's context-menu folder picker
  let contextMenuRef: MessageContextMenu | null = null

  export function isFolderPickerOpen(): boolean {
    return contextMenuRef?.isFolderPickerOpen() ?? false
  }

  export function toggleFolderPicker(mode: 'move' | 'copy') {
    if (!contextMenuRef) return
    // Same target rule as the Delete key: any checked messages act as a set,
    // otherwise the focused row. Applied only on the press that OPENS the
    // picker (close/switch presses keep the target locked), and never
    // mutates the selection — canceling the dialog keeps checkboxes intact.
    if (!contextMenuRef.isFolderPickerOpen()) {
      useMultiSelect = selectedMessageIds.length > 0
    }
    contextMenuRef.toggleFolderPicker(mode)
  }

  // Density-based class mappings
  // micro = smallest (power users), compact = small, standard = default, large = accessibility
  const densityClasses = {
    row: {
      // Left padding is a half-step wider than the right — symmetric px reads
      // as left-light next to the right edge's date/star column
      micro: 'pl-3.5 pr-3 py-2 gap-2',
      compact: 'pl-[1.125rem] pr-4 py-3 gap-3',
      standard: 'pl-[1.375rem] pr-5 py-4 gap-4',
      large: 'pl-[1.625rem] pr-6 py-5 gap-5',
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
    // Hidden state: zero width, negative right margin cancels the row's flex
    // gap (gap-2/3/4/5 per density); group-hover reveals in every layout
    // (on touch, WebKit sticky hover means a tap also reveals — accepted).
    // No opacity states — the checkbox is always fully opaque; visibility is
    // purely the width clip (overflow-hidden). The slide IS the reveal.
    checkboxHidden: {
      micro: 'w-0 h-4 -mr-2 group-hover:w-4 group-hover:mr-0',
      compact: 'w-0 h-5 -mr-3 group-hover:w-5 group-hover:mr-0',
      standard: 'w-0 h-6 -mr-4 group-hover:w-6 group-hover:mr-0',
      large: 'w-0 h-7 -mr-5 group-hover:w-7 group-hover:mr-0',
    },
    // Legacy always-reserved column ("Always show checkbox" setting ON):
    // opacity fade instead of width reveal — invisible until hover on
    // desktop, faintly visible on narrow (#30)
    checkboxAlways: 'opacity-0 group-hover:opacity-40 hover:!opacity-100 max-[767px]:opacity-40 max-[767px]:active:opacity-100',
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

  // Pixel sizes matching densityClasses.avatar (w-8/10/12/14) so the photo
  // avatar keeps the exact footprint of the colored circle it replaces.
  const AVATAR_PX = { micro: 32, compact: 40, standard: 48, large: 56 } as const

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
    // Returns a theme-driven avatar class (.avatar-1 .. .avatar-14, defined in themes.css).
    const email = conv.participants?.[0]?.email || conv.threadId
    let hash = 0
    for (let i = 0; i < email.length; i++) {
      hash = email.charCodeAt(i) + ((hash << 5) - hash)
    }
    return `avatar-${(Math.abs(hash) % 14) + 1}`
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

  // Drag start handler: stash messageIds + sourceAccountId in dataTransfer so
  // the folder drop target can move them via MoveToFolder(). If this row is
  // part of the multi-select, drag the whole checked set; otherwise drag just
  // this row's messages (matches the context-menu selection rule).
  function handleDragStart(e: DragEvent) {
    if (!e.dataTransfer) return
    const messageIds = checked ? selectedMessageIds : ownMessageIds
    if (messageIds.length === 0) return
    const payload = JSON.stringify({ messageIds, sourceAccountId: accountId })
    e.dataTransfer.setData('application/x-aerion-messages', payload)
    e.dataTransfer.effectAllowed = 'move'
  }

  // --- Swipe gestures ---
  // Touch (narrow layout): swipe right on a row toggles its checkbox; swipe
  // left deletes (same flow as the Delete key — Trash with Undo, or the
  // permanent-delete confirm in Trash folders). Trackpad (any layout):
  // two-finger horizontal swipes (wheel deltaX) do the same. No
  // preventDefault anywhere — Svelte 5 wheel/touch attribute handlers are
  // passive; touch-pan-y on the row lets the browser own vertical scrolling
  // (it fires touchcancel when it takes over).
  const DEBUG_SWIPE = false            // set true to toast raw gesture data during testing
  const TOUCH_LOCK_PX = 10             // movement before axis lock decision
  const TOUCH_FIRE_PX = 48             // horizontal travel that fires the action
  const WHEEL_FIRE_PX = 60             // accumulated horizontal delta that fires
  const WHEEL_RIGHT_SIGN = 1           // finger-right => positive deltaX (verified live on WebKitGTK)
  const WHEEL_COOLDOWN_MS = 500        // swallow momentum tail after firing
  const WHEEL_LINE_PX = 16             // deltaMode===1 (lines) -> px normalization
  const WHEEL_DOMINANCE = 2            // |deltaX| must beat |deltaY| by this factor to count as horizontal
  const WHEEL_MIN_EVENTS = 3           // sustained-gesture requirement: a single mouse notch (~126px) can't fire
  const WHEEL_VERTICAL_QUIET_MS = 300  // horizontal deltas ignored this soon after vertical scrolling

  const SWIPE_ANIM_MS = 700            // keep in sync with the swipe keyframe durations

  type SwipeKind = 'none' | 'select' | 'delete'

  let touchStartX = 0
  let touchStartY = 0
  let touchAxis: 'none' | 'h' | 'v' = 'none'
  let touchFired = false
  let swipeConsumedClick = false
  let wheelAccum = 0
  let wheelStreak = 0
  let wheelResetTimer: ReturnType<typeof setTimeout> | null = null
  // Plays the nudge-and-bounce keyframe on the row; the action lands at the
  // bounce (wheel deltas arrive too fast for live proportional tracking)
  let swipeAnim = $state<SwipeKind>('none')

  function armWheelReset() {
    if (wheelResetTimer) clearTimeout(wheelResetTimer)
    wheelResetTimer = setTimeout(() => {
      wheelAccum = 0
      wheelStreak = 0
    }, 350)
  }

  function fireSwipeAction(kind: SwipeKind) {
    switch (kind) {
      case 'select':
        onCheck(!checked)   // no event => plain toggle, no shift-range semantics
        return
      case 'delete':
        // Same target rule as drag + context menu: a checked row acts on the
        // whole selection, an unchecked row acts on itself only
        onDelete?.(checked ? selectedMessageIds : ownMessageIds)
        return
    }
  }

  function playSwipeFeedback(kind: SwipeKind) {
    swipeAnim = kind
    setTimeout(() => {
      swipeAnim = 'none'
      fireSwipeAction(kind)
    }, SWIPE_ANIM_MS)
  }

  function handleTouchStart(e: TouchEvent) {
    if (getLayoutMode() !== 'narrow') return
    touchStartX = e.touches[0].clientX
    touchStartY = e.touches[0].clientY
    touchAxis = 'none'
    touchFired = false
  }

  function handleTouchMove(e: TouchEvent) {
    if (getLayoutMode() !== 'narrow') return
    if (touchFired) return
    const dx = e.touches[0].clientX - touchStartX
    const dy = e.touches[0].clientY - touchStartY
    if (touchAxis === 'v') return
    if (touchAxis === 'none' && Math.max(Math.abs(dx), Math.abs(dy)) < TOUCH_LOCK_PX) return
    if (touchAxis === 'none') touchAxis = Math.abs(dx) > Math.abs(dy) ? 'h' : 'v'
    if (touchAxis !== 'h') return
    if (Math.abs(dx) < TOUCH_FIRE_PX) return
    touchFired = true
    swipeConsumedClick = true
    playSwipeFeedback(dx > 0 ? 'select' : 'delete')
  }

  function handleTouchEnd() {
    touchAxis = 'none'
    touchFired = false
  }

  function handleWheel(e: WheelEvent) {
    if (DEBUG_SWIPE) toasts.info(`wheel dx=${e.deltaX.toFixed(1)} dy=${e.deltaY.toFixed(1)} mode=${e.deltaMode} shift=${e.shiftKey}`)
    if (Date.now() < wheelCooldownUntil) return
    // Shift+wheel is the browser's horizontal-SCROLL idiom (a vertical notch
    // arrives as deltaX) — scroll intent, never a swipe gesture
    const scrollIntent = e.shiftKey ||
      Math.abs(e.deltaX) <= WHEEL_DOMINANCE * Math.abs(e.deltaY)
    if (scrollIntent) {
      lastVerticalWheelAt = Date.now()
      wheelAccum = 0
      wheelStreak = 0
      return
    }
    // Ignore horizontal residue during/right after vertical scrolling
    if (Date.now() - lastVerticalWheelAt < WHEEL_VERTICAL_QUIET_MS) return
    const px = e.deltaMode === 1 ? e.deltaX * WHEEL_LINE_PX : e.deltaX
    const rightward = px * WHEEL_RIGHT_SIGN
    // Signed accumulation: rightward positive (select), leftward negative
    // (delete). A direction change restarts the gesture from zero.
    if (rightward > 0 && wheelAccum < 0) {
      wheelAccum = 0
      wheelStreak = 0
    }
    if (rightward < 0 && wheelAccum > 0) {
      wheelAccum = 0
      wheelStreak = 0
    }
    wheelAccum += rightward
    wheelStreak += 1
    armWheelReset()
    // A real trackpad gesture is a sustained stream of events — a lone mouse
    // notch mapped to deltaX (tilt click, quirky driver) can't fire
    if (wheelStreak < WHEEL_MIN_EVENTS) return
    if (Math.abs(wheelAccum) < WHEEL_FIRE_PX) return
    const kind: SwipeKind = wheelAccum > 0 ? 'select' : 'delete'
    wheelAccum = 0
    wheelStreak = 0
    wheelCooldownUntil = Date.now() + WHEEL_COOLDOWN_MS
    playSwipeFeedback(kind)
  }

  function handleRowClick(e: MouseEvent) {
    if (swipeConsumedClick) {
      swipeConsumedClick = false
      return
    }
    onSelect(e)
  }
</script>

<MessageContextMenu
  bind:this={contextMenuRef}
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
    draggable={getLayoutMode() !== 'narrow'}
    class="group relative w-full flex items-start touch-pan-y {densityClasses.row[density]} text-left border-b border-border transition-colors duration-300 cursor-pointer outline-none {selected
      ? 'bg-primary/20'
      : 'hover:bg-muted/50'} {getAccentBarUnread() && hasUnread ? 'border-l-2 border-l-primary' : ''} {swipeAnim === 'select' ? 'swipe-select-anim' : ''} {swipeAnim === 'delete' ? 'swipe-delete-anim' : ''}"
    onclick={handleRowClick}
    onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect() }}}
    ondragstart={handleDragStart}
    ontouchstart={handleTouchStart}
    ontouchmove={handleTouchMove}
    ontouchend={handleTouchEnd}
    ontouchcancel={handleTouchEnd}
    onwheel={handleWheel}
    role="button"
    tabindex="0"
  >
    {#if swipeAnim === 'select'}
      <!-- Swipe feedback: select/deselect bubble pops in at the left edge
           while the row nudges right, fading out as the row settles -->
      <div class="swipe-select-indicator-track pointer-events-none absolute inset-y-0 left-1 z-10 flex items-center">
        <div class="swipe-select-indicator flex items-center justify-center rounded-full bg-primary text-primary-foreground w-6 h-6 shadow-md">
          <Icon icon={checked ? 'mdi:close' : 'mdi:check'} class="w-4 h-4" />
        </div>
      </div>
    {/if}

    {#if swipeAnim === 'delete'}
      <!-- Swipe feedback: delete bubble pops in at the right edge while the
           row nudges left, fading out as the row settles -->
      <div class="swipe-delete-indicator-track pointer-events-none absolute inset-y-0 right-1 z-10 flex items-center">
        <div class="swipe-select-indicator flex items-center justify-center rounded-full bg-destructive text-destructive-foreground w-6 h-6 shadow-md">
          <Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
        </div>
      </div>
    {/if}

    <!-- Checkbox column. Setting OFF (default): hidden (width-clipped) until
         row hover on desktop, revealed while checked, toggled by swipe on
         narrow/touch. Setting ON: legacy always-reserved column with opacity
         fade (invisible until hover on desktop, faint on narrow). -->
    <div
      class="flex-shrink-0 flex items-center justify-center self-center {getAlwaysShowMessageCheckbox()
        ? `${densityClasses.checkbox[density]} transition-opacity duration-200 ${checked ? 'opacity-100' : densityClasses.checkboxAlways}`
        : `overflow-hidden transition-all duration-200 ${checked ? densityClasses.checkbox[density] : densityClasses.checkboxHidden[density]}`}"
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

    <!-- Sender avatar: colored circle, or the contact's photo when enabled
         (falling back to the colored circle when the contact has no photo) -->
    {#if getShowMessageListCircles()}
      {#if getShowMessageListProfilePics()}
        {@const avatarPhoto = contactPhotos.get(conversation.participants?.[0]?.email ?? '')}
        <Avatar
          email={conversation.participants?.[0]?.email || conversation.threadId}
          name={conversation.participants?.[0]?.name}
          size={AVATAR_PX[density]}
          photoData={avatarPhoto?.data}
          photoMediaType={avatarPhoto?.mediaType}
        />
      {:else}
        <div
          class="{densityClasses.avatar[density]} rounded-full flex-shrink-0 flex items-center justify-center font-medium {getAvatarColor(
            conversation
          )}"
        >
          {getInitials(conversation)}
        </div>
      {/if}
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

<style>
  /* Swipe-to-select feedback: nudge right, then bounce back; the checkbox
     checks as the row settles. Duration must match SWIPE_ANIM_MS. Both the
     rule and keyframes are global because the class is applied through a
     dynamic string, which Svelte's scoped-CSS pruning can't see. */
  @keyframes -global-swipe-select {
    0% {
      transform: translateX(0);
    }
    35% {
      transform: translateX(48px);
    }
    55% {
      transform: translateX(48px);
    }
    100% {
      transform: translateX(0);
    }
  }

  :global(.swipe-select-anim) {
    animation: swipe-select 700ms ease-out;
  }

  /* Mirror of swipe-select: nudge LEFT for swipe-to-delete */
  @keyframes -global-swipe-delete {
    0% {
      transform: translateX(0);
    }
    35% {
      transform: translateX(-48px);
    }
    55% {
      transform: translateX(-48px);
    }
    100% {
      transform: translateX(0);
    }
  }

  :global(.swipe-delete-anim) {
    animation: swipe-delete 700ms ease-out;
  }

  /* Counter-translation keeping the delete bubble stationary at the right
     edge while the row slides left out from under it */
  .swipe-delete-indicator-track {
    animation: swipe-delete-counter 700ms ease-out forwards;
  }

  @keyframes swipe-delete-counter {
    0% {
      transform: translateX(0);
    }
    35% {
      transform: translateX(48px);
    }
    55% {
      transform: translateX(48px);
    }
    100% {
      transform: translateX(0);
    }
  }

  /* The bubble lives INSIDE the row, so it would ride along with the nudge —
     this counter-translation (mirror of swipe-select) keeps it visually
     stationary in the strip the row vacates. */
  .swipe-select-indicator-track {
    animation: swipe-select-counter 700ms ease-out forwards;
  }

  @keyframes swipe-select-counter {
    0% {
      transform: translateX(0);
    }
    35% {
      transform: translateX(-48px);
    }
    55% {
      transform: translateX(-48px);
    }
    100% {
      transform: translateX(0);
    }
  }

  /* Select/deselect bubble: pop in, hold, fade out — fill-mode forwards keeps
     it at opacity 0 until the {#if} removes it. Fades fully out by 80% of the
     nudge so it never overlaps the checkbox as the row settles and checks. */
  .swipe-select-indicator {
    animation: swipe-select-pop 700ms ease-out forwards;
  }

  @keyframes swipe-select-pop {
    0% {
      opacity: 0;
      transform: scale(0.4);
    }
    25% {
      opacity: 1;
      transform: scale(1.15);
    }
    75% {
      opacity: 1;
      transform: scale(1);
    }
    95% {
      opacity: 0;
      transform: scale(0.8);
    }
    100% {
      opacity: 0;
      transform: scale(0.8);
    }
  }
</style>
