<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import Icon from '@iconify/svelte'
  // @ts-ignore - wailsjs bindings
  import { GetConversation, GetReadReceiptResponsePolicy, SendReadReceipt, IgnoreReadReceipt, GetMarkAsReadDelay, GetMessageSource, ProcessSMIMEMessage, ProcessPGPMessage, FetchMessageBody } from '../../../../wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import { MarkAsRead, MarkAsUnread, Star, Unstar, Archive, Trash, MarkAsSpam, MarkAsNotSpam, DeletePermanently, Undo } from '../../../../wailsjs/go/app/App'
  // @ts-ignore - wailsjs path
  import { EventsOn } from '../../../../wailsjs/runtime/runtime'
  // @ts-ignore - wailsjs path
  import { message as messageModels } from '../../../../wailsjs/go/models'
  import AttachmentList from './AttachmentList.svelte'
  import EmailBody from './EmailBody.svelte'
  import { toasts } from '$lib/stores/toast'
  import { setFocusedPane, isInputElement, isComposerOpen } from '$lib/stores/keyboard.svelte'
  import { ConfirmDialog } from '$lib/components/ui/confirm-dialog'
  import MessageContextMenu from '$lib/components/common/MessageContextMenu.svelte'
  import { _ } from '$lib/i18n'
  import { isDialogGuardActive } from '$lib/stores/dialogGuard'
  import { getShowViewerCircles, getDarkMailContent } from '$lib/stores/settings.svelte'
  import { getIsDarkActive } from '$lib/stores/theme.svelte'

  interface Props {
    threadId?: string | null
    folderId?: string | null
    folderType?: string | null
    accountId?: string | null
    onReply?: (mode: 'reply' | 'reply-all' | 'forward', messageId: string, imagesLoaded?: boolean) => void
    onComposeToAddress?: (toAddress: string) => void
    onEditDraft?: (draftId: string) => void
    onActionComplete?: (autoSelectNext?: boolean) => void
    isFocused?: boolean
    isFlashing?: boolean
    showBackButton?: boolean
    onBack?: () => void
    // Focus mode (whole thread or single message takes full window)
    inFocusMode?: boolean
    focusModeKind?: 'thread' | 'message' | null
    focusedMessageIdInFocus?: string | null
    onToggleThreadFocus?: () => void
    onToggleMessageFocus?: (messageId: string) => void
  }

  let {
    threadId = null,
    folderId = null,
    folderType = null,
    accountId = null,
    onReply,
    onComposeToAddress,
    onEditDraft,
    onActionComplete,
    isFocused = false,
    isFlashing = false,
    showBackButton = false,
    onBack,
    inFocusMode = false,
    focusModeKind = null,
    focusedMessageIdInFocus = null,
    onToggleThreadFocus,
    onToggleMessageFocus,
  }: Props = $props()

  // Track which messages have had their remote images loaded by the user
  const messagesWithImagesLoaded = new Set<string>()

  // Decrypted attachment metadata
  interface DecryptedAttachment {
    filename: string
    contentType: string
    size: number
    isInline: boolean
    contentId: string
  }

  // S/MIME on-view processing result type
  interface SMIMEViewResult {
    bodyHtml: string
    bodyText: string
    smimeStatus: string
    smimeSignerEmail: string
    smimeSignerSubject: string
    smimeEncrypted: boolean
    inlineAttachments?: Record<string, string>
    attachments?: DecryptedAttachment[]
  }

  // PGP on-view processing result type
  interface PGPViewResult {
    bodyHtml: string
    bodyText: string
    pgpStatus: string
    pgpSignerEmail: string
    pgpSignerKeyId: string
    pgpEncrypted: boolean
    inlineAttachments?: Record<string, string>
    attachments?: DecryptedAttachment[]
  }

  // State
  let conversation = $state<messageModels.Conversation | null>(null)
  // Per-message EmailBody refs, used to pull each rendered body for printing.
  let emailBodyRefs: Record<string, { getPrintableHtml(): Promise<string> }> = {}
  let loading = $state(false)
  let error = $state<string | null>(null)

  // S/MIME on-view processing results per message
  let smimeResults = $state<Record<string, SMIMEViewResult>>({})
  let smimeLoading = $state<Set<string>>(new Set())

  // PGP on-view processing results per message
  let pgpResults = $state<Record<string, PGPViewResult>>({})
  let pgpLoading = $state<Set<string>>(new Set())

  // Track which messages are expanded (unread messages auto-expand)
  let expandedMessages = $state<Set<string>>(new Set())

  // Per-message override for the dark-mail-content filter. Runtime-only,
  // resets when the conversation changes. Truthy value = user explicitly
  // disabled the filter for this message.
  let darkMailOverrides = $state<Record<string, boolean>>({})

  function shouldDarkenMessage(msgId: string): boolean {
    if (!getDarkMailContent()) return false
    if (!getIsDarkActive()) return false
    return !darkMailOverrides[msgId]
  }

  function toggleDarkMailOverride(msgId: string) {
    darkMailOverrides = { ...darkMailOverrides, [msgId]: !darkMailOverrides[msgId] }
  }

  // Track focused message for keyboard deletion
  let focusedMessageId = $state<string | null>(null)

  // Read receipt policy and tracking
  let readReceiptPolicy = $state<'never' | 'ask' | 'always'>('ask')
  let handledReadReceipts = $state<Set<string>>(new Set()) // Track locally handled receipts
  let sendingReadReceipt = $state<Set<string>>(new Set()) // Track in-flight sends

  // Delete confirmation state
  let showDeleteConfirm = $state(false)

  // Auto-mark-as-read state
  let markAsReadDelay = $state(1000) // Default 1 second, loaded from settings
  let markAsReadTimer: ReturnType<typeof setTimeout> | null = null
  let pendingMarkAsReadIds = $state<Set<string>>(new Set()) // Track message IDs we're marking as read

  // Debounce timer for refreshConversation (coalesces rapid sync events)
  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  let pendingRefresh: { tid: string; fid: string } | null = null
  let dialogGuardInterval: ReturnType<typeof setInterval> | null = null

  // Event listener cleanup functions
  let cleanupFunctions: (() => void)[] = []

  // Load settings and set up event listeners on mount
  onMount(async () => {
    try {
      const [policy, delay] = await Promise.all([
        GetReadReceiptResponsePolicy(),
        GetMarkAsReadDelay(),
      ])
      readReceiptPolicy = policy as 'never' | 'ask' | 'always'
      markAsReadDelay = delay
    } catch (err) {
      console.error('Failed to load settings:', err)
    }

    // Listen for message changes from backend
    cleanupFunctions.push(
      EventsOn('messages:readChanged', (data: { messageIds: string[], isRead: boolean }) => {
        // Check if this is our own mark-as-read operation
        const isOwnOperation = data.messageIds.every(id => pendingMarkAsReadIds.has(id))

        if (isOwnOperation) {
          // Clear pending IDs and update local state
          pendingMarkAsReadIds = new Set()
          if (conversation?.messages) {
            // Update isRead flag locally
            for (const m of conversation.messages) {
              if (data.messageIds.includes(m.id)) {
                m.isRead = data.isRead
              }
            }
            // Update conversation unread count
            const delta = data.isRead ? -data.messageIds.length : data.messageIds.length
            conversation.unreadCount = Math.max(0, (conversation.unreadCount || 0) + delta)
            // Trigger reactivity
            conversation = conversation
          }
        } else {
          // External change on displayed conversation
          if (conversation?.messages?.some(m => data.messageIds.includes(m.id))) {
            if (!data.isRead) {
              // Marked as unread externally — close the conversation to prevent
              // scheduleMarkAsRead from re-marking it read
              if (markAsReadTimer) {
                clearTimeout(markAsReadTimer)
                markAsReadTimer = null
              }
              conversation = null
              return
            }
            if (threadId && folderId) {
              loadConversation(threadId, folderId)
            }
          }
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('messages:moved', (data: { messageIds: string[], destFolderId: string }) => {
        if (!conversation?.messages?.some(m => data.messageIds.includes(m.id))) return

        const movedCount = conversation.messages.filter(m => data.messageIds.includes(m.id)).length
        const remainingCount = conversation.messages.length - movedCount

        if (remainingCount === 0) {
          // All messages moved out — dismiss and auto-select next
          dismissConversation(true)
          return
        }
        // Some messages remain — reload conversation
        if (threadId && folderId) {
          loadConversation(threadId, folderId)
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('messages:deleted', async (messageIds: string[]) => {
        if (conversation?.messages?.some(m => messageIds.includes(m.id))) {
          // Check how many messages were deleted
          const deletedCount = conversation.messages.filter(m => messageIds.includes(m.id)).length
          const remainingCount = conversation.messages.length - deletedCount

          if (remainingCount === 0) {
            // All messages deleted - navigate away
            conversation = null
            onActionComplete?.(true)
          } else {
            // Some messages remain - reload conversation
            if (threadId && folderId) {
              await loadConversation(threadId, folderId)
            }
          }
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('undo:completed', () => {
        // Reload conversation after undo
        if (threadId && folderId) {
          loadConversation(threadId, folderId)
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('messages:updated', (data: { accountId: string; folderId: string }) => {
        if (threadId && folderId && accountId && data.accountId === accountId && data.folderId === folderId) {
          scheduleRefresh(threadId, folderId)
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('folder:synced', (data: { accountId: string; folderId: string }) => {
        if (threadId && folderId && accountId && data.accountId === accountId && data.folderId === folderId) {
          // If conversation hasn't loaded yet or errored, do a full load
          if (!conversation || error) {
            loadConversation(threadId, folderId)
            return
          }
          // Otherwise, smart refresh: only update DOM if messages actually changed.
          scheduleRefresh(threadId, folderId)
        }
      })
    )

    cleanupFunctions.push(
      EventsOn('sent:synced', (data: { accountId: string }) => {
        if (threadId && folderId && accountId && data.accountId === accountId) {
          scheduleRefresh(threadId, folderId)
        }
      })
    )

    // Keyboard handler for message navigation and deletion
    const handleKeyDown = (e: KeyboardEvent) => {
      // Only handle if viewer pane is focused
      if (!isFocused) return

      // Handle Tab for message navigation. preventDefault is called only when
      // we actually navigate, so at the first/last boundary native Tab passes
      // through and the user can leave the viewer normally.
      if (e.key === 'Tab' && conversation?.messages) {
        const messageIds = conversation.messages.map(m => m.id)
        const currentIndex = focusedMessageId ? messageIds.indexOf(focusedMessageId) : -1

        if (e.shiftKey) {
          if (currentIndex > 0) {
            e.preventDefault()
            focusedMessageId = messageIds[currentIndex - 1]
            ;(document.querySelector(`[data-message-id="${focusedMessageId}"]`) as HTMLElement)?.focus()
          }
          // currentIndex <= 0: let native Shift+Tab navigate out of the viewer
          return
        }

        if (currentIndex >= 0 && currentIndex < messageIds.length - 1) {
          e.preventDefault()
          focusedMessageId = messageIds[currentIndex + 1]
          ;(document.querySelector(`[data-message-id="${focusedMessageId}"]`) as HTMLElement)?.focus()
          return
        }
        if (currentIndex === -1 && messageIds.length > 0) {
          e.preventDefault()
          focusedMessageId = messageIds[0]
          ;(document.querySelector(`[data-message-id="${focusedMessageId}"]`) as HTMLElement)?.focus()
        }
        // At last message (currentIndex === messageIds.length - 1): let native Tab navigate out
        return
      }

      // Handle delete for focused message. Guard against the composer-mount
      // focus race (a keystroke fired between Reply click and TipTap focus)
      // and against any input/contenteditable having focus.
      if (focusedMessageId && (e.key === 'Delete' || e.key === 'Backspace')) {
        if (isComposerOpen()) return
        if (isInputElement(e.target) || isInputElement(document.activeElement)) return
        e.preventDefault()
        handleDeleteFocusedMessage()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    cleanupFunctions.push(() => {
      window.removeEventListener('keydown', handleKeyDown)
    })

    // Flush deferred refreshes once dialogs close
    dialogGuardInterval = setInterval(() => {
      if (pendingRefresh && !isDialogGuardActive()) {
        const { tid, fid } = pendingRefresh
        pendingRefresh = null
        scheduleRefresh(tid, fid)
      }
    }, 500)
  })

  onDestroy(() => {
    // Clean up timers
    if (markAsReadTimer) {
      clearTimeout(markAsReadTimer)
      markAsReadTimer = null
    }
    if (refreshTimer) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
    if (dialogGuardInterval) clearInterval(dialogGuardInterval)
    // Clean up all event listeners
    cleanupFunctions.forEach(cleanup => cleanup())
  })

  // Load conversation when threadId changes
  $effect(() => {
    // Clear pending refresh timer on any threadId change (full load supersedes refresh)
    if (refreshTimer) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
    messagesWithImagesLoaded.clear()
    darkMailOverrides = {}
    // Reset focused message on thread change so opening a thread starts fresh.
    // Same-thread refreshes (handled via scheduleRefresh) preserve focus.
    focusedMessageId = null

    if (threadId && folderId) {
      // Setting is already loaded on mount - no need to fetch on every conversation switch
      loadConversation(threadId, folderId)
    }

    if (!threadId || !folderId) {
      // Clear any pending mark-as-read timer when navigating away
      if (markAsReadTimer) {
        clearTimeout(markAsReadTimer)
        markAsReadTimer = null
      }
      conversation = null
      expandedMessages = new Set()
    }
  })

  // Debounced refresh: coalesces rapid sync events (e.g. folder:synced + messages:updated)
  // into a single refreshConversation call, reducing Wails bridge pressure.
  // Defers if a dialog guard is active (e.g. folder picker open).
  function scheduleRefresh(tid: string, fid: string) {
    if (isDialogGuardActive()) {
      pendingRefresh = { tid, fid }
      return
    }
    if (refreshTimer) clearTimeout(refreshTimer)
    refreshTimer = setTimeout(() => {
      refreshTimer = null
      refreshConversation(tid, fid)
    }, 300)
  }

  // Lightweight refresh: fetches the conversation but only updates the DOM
  // if something actually changed (new messages, different count, etc.).
  // This avoids the visible flash/re-render when a sync completes with no changes.
  async function refreshConversation(tid: string, fid: string) {
    try {
      const updated = await GetConversation(tid, fid)

      // Stale guard: user navigated away while we were fetching
      if (threadId !== tid) return

      if (!updated?.messages || updated.messages.length === 0) {
        dismissConversation(true)
        return
      }
      if (!conversation?.messages) return

      // Compare message count and latest message ID to detect actual changes
      if (updated.messages.length === conversation.messages.length) {
        const currentLatestId = conversation.messages[conversation.messages.length - 1]?.id
        const updatedLatestId = updated.messages[updated.messages.length - 1]?.id
        if (currentLatestId === updatedLatestId) return
      }

      // Something changed — update conversation, preserving scroll position
      const scrollTop = contentContainerRef?.scrollTop ?? 0
      conversation = updated

      // Expand any new unread messages
      if (conversation.messages) {
        const newExpanded = new Set(expandedMessages)
        conversation.messages.forEach((m, i) => {
          if (!m.isRead || i === conversation!.messages!.length - 1) {
            newExpanded.add(m.id)
          }
        })
        expandedMessages = newExpanded
        scheduleMarkAsRead(tid, conversation.messages)
        processSMIMEMessages(conversation.messages)
        processPGPMessages(conversation.messages)
      }

      await tick()
      if (contentContainerRef) {
        contentContainerRef.scrollTop = scrollTop
      }
    } catch (err) {
      console.error('Failed to refresh conversation:', err)
    }
  }

  async function loadConversation(tid: string, fid: string) {
    // Clear any pending mark-as-read timer from previous conversation
    if (markAsReadTimer) {
      clearTimeout(markAsReadTimer)
      markAsReadTimer = null
    }

    loading = true
    error = null

    try {
      const result = await GetConversation(tid, fid)

      // Stale guard: user navigated away while we were fetching
      if (threadId !== tid) return

      conversation = result

      // Auto-expand unread messages and the last message
      if (conversation?.messages) {
        const newExpanded = new Set<string>()
        conversation.messages.forEach((m, i) => {
          // Expand if unread or if it's the last message
          if (!m.isRead || i === conversation!.messages!.length - 1) {
            newExpanded.add(m.id)
          }
        })
        expandedMessages = newExpanded

        // Schedule auto-mark-as-read for unread messages
        scheduleMarkAsRead(tid, conversation.messages)

        // Process S/MIME messages on-view
        processSMIMEMessages(conversation.messages)

        // Process PGP messages on-view
        processPGPMessages(conversation.messages)

        // Fetch bodies for messages that don't have them yet (on-demand)
        fetchUnfetchedBodies(conversation.messages)
      }
    } catch (err) {
      console.error('Failed to load conversation:', err)
      error = $_('viewer.failedToLoad')
    } finally {
      loading = false
      // Scroll to bottom to show the latest message
      await tick()
      if (contentContainerRef) {
        contentContainerRef.scrollTop = contentContainerRef.scrollHeight
      }
    }
  }

  /** Dismiss the current conversation from the viewer.
   *  Cancels any pending mark-as-read timer, clears the conversation state,
   *  and optionally tells the message list to auto-select the next item. */
  function dismissConversation(autoSelectNext: boolean) {
    if (markAsReadTimer) {
      clearTimeout(markAsReadTimer)
      markAsReadTimer = null
    }
    conversation = null
    if (autoSelectNext) {
      onActionComplete?.(true)
    }
  }

  // Process S/MIME messages on-view (verify/decrypt fresh each time)
  // Fetch bodies on-demand for messages that don't have them yet
  async function fetchUnfetchedBodies(messages: messageModels.Message[]) {
    for (const msg of messages) {
      if ((msg as any).bodyFetched === false && !msg.bodyHtml && !msg.bodyText) {
        try {
          const updated = await FetchMessageBody(msg.id)
          // Update the message in the conversation if still viewing
          if (conversation?.messages) {
            const idx = conversation.messages.findIndex(m => m.id === msg.id)
            if (idx >= 0 && updated) {
              conversation.messages[idx] = updated
              conversation = conversation // trigger reactivity
            }
          }
        } catch (err) {
          console.error('Failed to fetch body for message:', msg.id, err)
          // Message may have been deleted from server — remove from conversation display
          if (conversation?.messages) {
            conversation.messages = conversation.messages.filter(m => m.id !== msg.id)
            conversation = conversation // trigger reactivity
          }
        }
      }
    }
  }

  function processSMIMEMessages(messages: messageModels.Message[]) {
    // Clear previous results
    smimeResults = {}
    smimeLoading = new Set()

    for (const msg of messages) {
      if (!msg.hasSMIME) continue
      smimeLoading = new Set([...smimeLoading, msg.id])

      ProcessSMIMEMessage(msg.id).then(result => {
        smimeResults = { ...smimeResults, [msg.id]: result }
        const next = new Set(smimeLoading)
        next.delete(msg.id)
        smimeLoading = next
      }).catch(err => {
        console.error('Failed to process S/MIME message:', msg.id, err)
        const next = new Set(smimeLoading)
        next.delete(msg.id)
        smimeLoading = next
      })
    }
  }

  // Process PGP messages on-view (verify/decrypt fresh each time)
  function processPGPMessages(messages: messageModels.Message[]) {
    pgpResults = {}
    pgpLoading = new Set()

    for (const msg of messages) {
      if (!msg.hasPGP) continue
      pgpLoading = new Set([...pgpLoading, msg.id])

      ProcessPGPMessage(msg.id).then(result => {
        pgpResults = { ...pgpResults, [msg.id]: result }
        const next = new Set(pgpLoading)
        next.delete(msg.id)
        pgpLoading = next
      }).catch(err => {
        console.error('Failed to process PGP message:', msg.id, err)
        const next = new Set(pgpLoading)
        next.delete(msg.id)
        pgpLoading = next
      })
    }
  }

  // Schedule marking messages as read based on user's delay setting
  function scheduleMarkAsRead(capturedThreadId: string, messages: messageModels.Message[]) {
    // Clear any existing timer to prevent stale fires
    if (markAsReadTimer) {
      clearTimeout(markAsReadTimer)
      markAsReadTimer = null
    }

    // Get unread message IDs
    const unreadIds = messages.filter(m => !m.isRead).map(m => m.id)

    if (unreadIds.length === 0) {
      return // No unread messages
    }

    // markAsReadDelay: -1 = manual only, 0 = immediate, >0 = delay in ms
    if (markAsReadDelay < 0) {
      return // Manual only, don't auto-mark
    }

    // Track these IDs as pending
    pendingMarkAsReadIds = new Set(unreadIds)

    if (markAsReadDelay === 0) {
      // Immediate
      MarkAsRead(unreadIds).catch(err => {
        console.error('Failed to mark messages as read:', err)
        pendingMarkAsReadIds = new Set() // Clear on error
      })
    } else {
      // With delay
      markAsReadTimer = setTimeout(() => {
        // Verify we're still viewing the same conversation
        if (threadId === capturedThreadId) {
          MarkAsRead(unreadIds).catch(err => {
            console.error('Failed to mark messages as read:', err)
            pendingMarkAsReadIds = new Set() // Clear on error
          })
        } else {
          pendingMarkAsReadIds = new Set() // Clear if we navigated away
        }
      }, markAsReadDelay)
    }
  }

  function toggleMessage(messageId: string) {
    const newSet = new Set(expandedMessages)
    const wasExpanded = newSet.has(messageId)

    if (wasExpanded) {
      newSet.delete(messageId)
    } else {
      newSet.add(messageId)

      // Check for auto-send read receipt on expand
      if (readReceiptPolicy === 'always' && conversation?.messages) {
        const msg = conversation.messages.find(m => m.id === messageId)
        if (msg) {
          handleMessageExpanded(msg)
        }
      }
    }
    expandedMessages = newSet
  }

  function expandAll() {
    if (conversation?.messages) {
      expandedMessages = new Set(conversation.messages.map(m => m.id))
    }
  }

  function collapseAll() {
    // Keep only the last message expanded
    if (conversation?.messages && conversation.messages.length > 0) {
      expandedMessages = new Set([conversation.messages[conversation.messages.length - 1].id])
    }
  }

  function formatDate(dateStr: any): string {
    const date = new Date(dateStr)
    return `${date.toLocaleDateString()} at ${date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
  }

  function getInitials(name: string): string {
    return name
      .split(' ')
      .map((n) => n[0])
      .join('')
      .toUpperCase()
      .slice(0, 2)
  }

  function getAvatarColor(email: string): string {
    // Returns a theme-driven avatar class (.avatar-1 .. .avatar-14, defined in themes.css).
    let hash = 0
    for (let i = 0; i < email.length; i++) {
      hash = email.charCodeAt(i) + ((hash << 5) - hash)
    }
    return `avatar-${(Math.abs(hash) % 14) + 1}`
  }

  // Parse recipient list (JSON array format from backend)
  function parseRecipients(recipientStr: string | undefined): Array<{ name: string; email: string }> {
    if (!recipientStr) return []
    try {
      const parsed = JSON.parse(recipientStr)
      if (Array.isArray(parsed)) {
        return parsed.map((r: any) => ({
          name: r.name || '',
          email: r.email || ''
        }))
      }
      return []
    } catch {
      return []
    }
  }

  // Get the last message ID in the conversation (for reply actions)
  // Exported for keyboard shortcut use from App.svelte
  export function hasFocusedMessage(): boolean {
    return focusedMessageId !== null
  }

  export function getFocusedMessageId(): string | null {
    return focusedMessageId
  }

  export function selectAllText() {
    const targetId = focusedMessageId ?? getLastMessageId()
    if (!targetId || !expandedMessages.has(targetId)) return
    const messageEl = document.querySelector(`[data-message-id="${targetId}"]`)
    if (!messageEl) return
    const iframe = messageEl.querySelector('iframe') as HTMLIFrameElement | null
    if (!iframe?.contentWindow) return
    iframe.contentWindow.postMessage({ type: 'select-all' }, '*')
  }

  export function getLastMessageId(): string | null {
    if (!conversation?.messages || conversation.messages.length === 0) return null
    return conversation.messages[conversation.messages.length - 1].id
  }

  // Re-fetch conversation to pick up flag changes (star, read) from external actions
  export async function refreshFlags() {
    if (!threadId || !folderId) return
    try {
      conversation = await GetConversation(threadId, folderId)
    } catch {
      // Silent — flag refresh is best-effort
    }
  }

  // Get the target message ID for actions:
  // Use focused message if one is focused, otherwise fall back to last message
  function getTargetMessageId(): string | null {
    return focusedMessageId ?? getLastMessageId()
  }

  // Action button handlers
  function handleReply() {
    const messageId = getTargetMessageId()
    if (messageId && onReply) {
      onReply('reply', messageId, messagesWithImagesLoaded.has(messageId))
    }
  }

  function handleReplyAll() {
    const messageId = getTargetMessageId()
    if (messageId && onReply) {
      onReply('reply-all', messageId, messagesWithImagesLoaded.has(messageId))
    }
  }

  function handleForward() {
    const messageId = getTargetMessageId()
    if (messageId && onReply) {
      onReply('forward', messageId, messagesWithImagesLoaded.has(messageId))
    }
  }

  async function handleArchive() {
    if (!conversation?.messages) return
    const messageIds = conversation.messages.map(m => m.id)

    try {
      await Archive(messageIds)
      toasts.success($_('toast.conversationArchived'), [
        { label: $_('common.undo'), onClick: handleUndo }
      ])
      onActionComplete?.(true)
    } catch (err) {
      console.error('Archive failed:', err)
      toasts.error($_('toast.failedToArchive'))
    }
  }

  async function handleDelete() {
    if (!conversation?.messages) return

    if (isTrashFolder) {
      // Show confirmation dialog for permanent delete
      showDeleteConfirm = true
    } else {
      // Move to trash (undoable)
      const messageIds = conversation.messages.map(m => m.id)
      try {
        const movedToTrash = await Trash(messageIds)
        const toastMsg = movedToTrash ? $_('toast.movedToTrash') : $_('toast.deletedFromFolder')
        const actions = movedToTrash ? [{ label: $_('common.undo'), onClick: handleUndo }] : []
        toasts.success(toastMsg, actions)
        onActionComplete?.(true)
      } catch (err) {
        console.error('Delete failed:', err)
        toasts.error($_('toast.failedToDelete'))
      }
    }
  }

  async function handleConfirmPermanentDelete() {
    if (!conversation?.messages) return
    const messageIds = conversation.messages.map(m => m.id)

    try {
      await DeletePermanently(messageIds)
      toasts.success($_('toast.permanentlyDeleted'))
      showDeleteConfirm = false
      onActionComplete?.(true)
    } catch (err) {
      console.error('Permanent delete failed:', err)
      toasts.error($_('toast.failedToDelete'))
      showDeleteConfirm = false
    }
  }

  // Delete the currently focused message (via keyboard)
  async function handleDeleteFocusedMessage() {
    if (!focusedMessageId) return

    if (isTrashFolder) {
      // Permanent delete from trash
      try {
        await DeletePermanently([focusedMessageId])
        toasts.success($_('toast.permanentlyDeleted'))
        focusedMessageId = null
        // Will auto-reload via messages:deleted event
      } catch (err) {
        console.error('Permanent delete failed:', err)
        toasts.error($_('toast.failedToDelete'))
      }
    } else {
      // Move to trash (undoable)
      try {
        const movedToTrash = await Trash([focusedMessageId])
        const toastMsg = movedToTrash ? $_('toast.movedToTrash') : $_('toast.deletedFromFolder')
        const actions = movedToTrash ? [{ label: $_('common.undo'), onClick: handleUndo }] : []
        toasts.success(toastMsg, actions)
        focusedMessageId = null
        // Will auto-reload via messages:deleted event
      } catch (err) {
        console.error('Delete failed:', err)
        toasts.error($_('toast.failedToDelete'))
      }
    }
  }

  async function handleSpam() {
    if (!conversation?.messages) return
    const messageIds = conversation.messages.map(m => m.id)

    try {
      if (isSpamFolder) {
        // If we're in spam folder, mark as NOT spam
        await MarkAsNotSpam(messageIds)
        toasts.success($_('toast.markedAsNotSpam'), [
          { label: $_('common.undo'), onClick: handleUndo }
        ])
        onActionComplete?.(true)
        return
      }
      // Otherwise, mark as spam
      const movedToSpam = await MarkAsSpam(messageIds)
      const toastMsg = movedToSpam ? $_('toast.markedAsSpam') : $_('toast.deletedFromFolder')
      const actions = movedToSpam ? [{ label: $_('common.undo'), onClick: handleUndo }] : []
      toasts.success(toastMsg, actions)
      onActionComplete?.(true)
    } catch (err) {
      console.error('Spam toggle failed:', err)
      toasts.error($_(isSpamFolder ? 'toast.failedToMarkAsNotSpam' : 'toast.failedToMarkAsSpam'))
    }
  }

  async function handleStar() {
    if (!conversation?.messages || !threadId || !folderId) return

    // Toggle based on current state - star if any unstarred, unstar if all starred
    const wasAllStarred = conversation.messages.every(m => m.isStarred)
    const messageIds = conversation.messages.map(m => m.id)

    try {
      if (wasAllStarred) {
        await Unstar(messageIds)
        toasts.success($_('toast.removedStar'))
      }
      if (!wasAllStarred) {
        await Star(messageIds)
        toasts.success($_('toast.starred'))
      }
      conversation = await GetConversation(threadId, folderId)
      onActionComplete?.()
    } catch (err) {
      console.error('Star toggle failed:', err)
      toasts.error($_('toast.failedToUpdateStar'))
    }
  }

  async function handleMarkRead() {
    if (!conversation?.messages) return

    // Toggle based on current state
    const allRead = conversation.messages.every(m => m.isRead)
    const messageIds = conversation.messages.map(m => m.id)

    // Tag these as our own operation so the readChanged listener treats
    // the resulting event as a local toggle (update flags + counts) rather
    // than an external mark-unread (which closes the conversation to stop
    // the auto-mark-as-read timer).
    pendingMarkAsReadIds = new Set(messageIds)

    try {
      if (allRead) {
        await MarkAsUnread(messageIds)
        toasts.success($_('toast.markedAsUnread'))
      }
      if (!allRead) {
        await MarkAsRead(messageIds)
        toasts.success($_('toast.markedAsRead'))
      }
    } catch (err) {
      console.error('Read status toggle failed:', err)
      toasts.error($_('toast.failedToUpdateReadStatus'))
      pendingMarkAsReadIds = new Set()
    }
  }

  async function handleUndo() {
    try {
      const description = await Undo()
      toasts.success($_('toast.undone', { values: { description } }))
      // Reload conversation to show updated state
      if (threadId && folderId) {
        await loadConversation(threadId, folderId)
      }
      onActionComplete?.()
    } catch (err) {
      console.error('Undo failed:', err)
      toasts.error($_('toast.undoFailed'))
    }
  }

  function escapeHtmlText(s: string): string {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  }

  function printRecipientsRow(label: string, raw: string | undefined): string {
    const list = parseRecipients(raw)
    if (list.length === 0) return ''
    const people = list.map((r) => escapeHtmlText(formatEmailForCopy(r.name, r.email))).join(', ')
    return `<tr><td class="lbl">${label}</td><td>${people}</td></tr>`
  }

  function buildPrintBlock(msg: messageModels.Message, bodyHtml: string): string {
    const header = `<table class="hdr">
      <tr><td class="lbl">From</td><td>${escapeHtmlText(formatEmailForCopy(msg.fromName, msg.fromEmail))}</td></tr>
      ${printRecipientsRow('To', msg.toList)}
      ${printRecipientsRow('Cc', msg.ccList)}
      <tr><td class="lbl">Date</td><td>${escapeHtmlText(formatDate(msg.date))}</td></tr>
    </table>`
    return `<section class="msg">${header}<div class="body">${bodyHtml}</div></section>`
  }

  function printDocument(subject: string, blocks: string[]) {
    const doc = `<!DOCTYPE html><html><head><meta charset="utf-8">
      <style>
        body { font-family: -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color: #000; background: #fff; margin: 0; padding: 0; font-size: 12px; line-height: 1.45; }
        h1 { font-size: 15px; margin: 0 0 14px; }
        .msg { margin-bottom: 20px; }
        .msg + .msg { border-top: 2px solid #ddd; padding-top: 14px; }
        .hdr { border-collapse: collapse; margin-bottom: 10px; font-size: 11px; }
        .hdr td { vertical-align: top; padding: 1px 0; }
        .hdr td.lbl { color: #555; font-weight: 600; padding-right: 12px; white-space: nowrap; }
        .body { font-size: 12px; }
        .body img { max-width: 100%; height: auto; }
        @page { margin: 14mm; }
      </style></head>
      <body><h1>${escapeHtmlText(subject)}</h1>${blocks.join('')}</body></html>`

    const frame = document.createElement('iframe')
    frame.setAttribute('aria-hidden', 'true')
    frame.style.cssText = 'position:fixed;right:0;bottom:0;width:0;height:0;border:0;'
    frame.srcdoc = doc
    frame.onload = () => {
      const win = frame.contentWindow
      if (!win) {
        frame.remove()
        return
      }
      win.addEventListener('afterprint', () => setTimeout(() => frame.remove(), 500))
      win.focus()
      win.print()
      // Fallback cleanup if afterprint never fires (some webviews)
      setTimeout(() => frame.remove(), 60000)
    }
    document.body.appendChild(frame)
  }

  async function handlePrint() {
    // Print only the messages currently rendered (those with an EmailBody).
    const msgs = visibleMessages.filter((m) => emailBodyRefs[m.id])
    if (msgs.length === 0) {
      window.print()
      return
    }
    const blocks: string[] = []
    for (const m of msgs) {
      let body: string
      try {
        body = await emailBodyRefs[m.id].getPrintableHtml()
      } catch {
        body = ''
      }
      blocks.push(buildPrintBlock(m, body))
    }
    printDocument(conversation?.subject ?? '', blocks)
  }

  // Read receipt handling
  async function handleSendReadReceipt(messageId: string, accountId: string) {
    if (sendingReadReceipt.has(messageId)) return

    sendingReadReceipt = new Set([...sendingReadReceipt, messageId])

    try {
      await SendReadReceipt(accountId, messageId)
      handledReadReceipts = new Set([...handledReadReceipts, messageId])
      toasts.success($_('viewer.readReceiptSent'))
    } catch (err) {
      console.error('Failed to send read receipt:', err)
      toasts.error($_('viewer.failedToSendReceipt'))
    } finally {
      const newSet = new Set(sendingReadReceipt)
      newSet.delete(messageId)
      sendingReadReceipt = newSet
    }
  }

  async function handleIgnoreReadReceipt(messageId: string, accountId: string) {
    try {
      await IgnoreReadReceipt(accountId, messageId)
      handledReadReceipts = new Set([...handledReadReceipts, messageId])
    } catch (err) {
      console.error('Failed to ignore read receipt:', err)
    }
  }

  // Check if message should show read receipt banner
  function shouldShowReadReceiptBanner(msg: messageModels.Message): boolean {
    // Don't show if policy is 'never'
    if (readReceiptPolicy === 'never') return false

    // Don't show if no read receipt requested
    if (!msg.readReceiptTo) return false

    // Don't show if already handled (from server or locally)
    if (msg.readReceiptHandled || handledReadReceipts.has(msg.id)) return false

    return true
  }

  // Auto-send read receipt when message is expanded (for 'always' policy)
  function handleMessageExpanded(msg: messageModels.Message) {
    if (readReceiptPolicy === 'always' && shouldShowReadReceiptBanner(msg)) {
      handleSendReadReceipt(msg.id, msg.accountId)
    }
  }

  // Computed: are all messages in the conversation starred?
  const allStarred = $derived(
    conversation?.messages?.every(m => m.isStarred) ?? false
  )

  // Computed: are all messages in the conversation read?
  const allRead = $derived(
    conversation?.messages?.every(m => m.isRead) ?? false
  )

  // Computed: is this the Trash folder?
  const isTrashFolder = $derived(folderType === 'trash')

  // Computed: is this the Drafts folder?
  const isDraftsFolder = $derived(folderType === 'drafts')

  // Computed: is this the Spam folder?
  const isSpamFolder = $derived(folderType === 'spam')

  // Computed: messages visible in the viewer.
  // In message-focus mode, narrow to the single targeted message.
  // Otherwise show the whole thread.
  const visibleMessages = $derived(
    inFocusMode && focusModeKind === 'message' && focusedMessageIdInFocus
      ? (conversation?.messages?.filter(m => m.id === focusedMessageIdInFocus) ?? [])
      : (conversation?.messages ?? [])
  )

  // Reference to the scrollable content area
  let contentContainerRef = $state<HTMLDivElement | null>(null)
  const SCROLL_AMOUNT = 100 // pixels to scroll per keypress

  // Scroll the viewer up (exposed for keyboard navigation)
  export function scrollUp() {
    if (contentContainerRef) {
      contentContainerRef.scrollBy({ top: -SCROLL_AMOUNT, behavior: 'smooth' })
    }
  }

  // Scroll the viewer down (exposed for keyboard navigation)
  export function scrollDown() {
    if (contentContainerRef) {
      contentContainerRef.scrollBy({ top: SCROLL_AMOUNT, behavior: 'smooth' })
    }
  }

  // Expose action functions for keyboard shortcuts
  export function toggleStar() {
    handleStar()
  }

  export function markRead() {
    handleMarkRead()
  }

  export function markUnread() {
    // Invert the read state
    if (allRead) return // Already handled by handleMarkRead toggle
    handleMarkRead()
  }

  export function archive() {
    handleArchive()
  }

  export function spam() {
    handleSpam()
  }

  export function trash() {
    if (focusedMessageId) {
      handleDeleteFocusedMessage()
      return
    }
    handleDelete()
  }

  export function deletePermanently() {
    if (focusedMessageId) {
      handleDeleteFocusedMessage()
      return
    }
    handleConfirmPermanentDelete()
  }

  export function reply() {
    handleReply()
  }

  export function replyAll() {
    handleReplyAll()
  }

  export function forward() {
    handleForward()
  }

  export function isImagesLoaded(messageId: string): boolean {
    return messagesWithImagesLoaded.has(messageId)
  }

  export function loadImages() {
    // Dispatch custom event that EmailBody components listen to
    window.dispatchEvent(new CustomEvent('load-remote-images'))
  }

  export function openAlwaysLoadDropdown() {
    // Dispatch custom event that EmailBody components listen to
    window.dispatchEvent(new CustomEvent('open-always-load-dropdown'))
  }

  // Open context menu for the focused (or last) message
  export function openContextMenu() {
    const targetId = focusedMessageId ?? getLastMessageId()
    if (!targetId) return
    const messageEl = document.querySelector(`[data-message-id="${targetId}"]`) as HTMLElement | null
    if (!messageEl) return
    const rect = messageEl.getBoundingClientRect()
    messageEl.dispatchEvent(new MouseEvent('contextmenu', {
      bubbles: true,
      clientX: rect.right,
      clientY: rect.top + rect.height / 2,
    }))
  }

  // Handle action completion from context menu (per-message)
  async function handleContextMenuActionComplete() {
    // Reload conversation after context menu action
    if (threadId && folderId) {
      try {
        await loadConversation(threadId, folderId)

        // If conversation no longer exists or has no messages, navigate away
        if (!conversation || !conversation.messages || conversation.messages.length === 0) {
          onActionComplete?.(true) // Auto-select next conversation
        }
      } catch {
        // Conversation deleted or error loading - navigate away
        onActionComplete?.(true)
      }
    }
  }

  // Copy text to clipboard with toast feedback
  async function copyToClipboard(text: string, label: string = 'Text') {
    try {
      await navigator.clipboard.writeText(text)
      toasts.success($_('viewer.copiedToClipboard', { values: { label } }))
    } catch {
      toasts.error($_('viewer.failedToCopy'))
    }
  }

  // Format email for display/copy: "Name <email>" or just "email"
  function formatEmailForCopy(name: string | undefined, email: string): string {
    if (name && name.trim()) {
      return `${name} <${email}>`
    }
    return email
  }

  // View source state
  let viewingSourceMessageId = $state<string | null>(null)
  let messageSource = $state<string | null>(null)
  let loadingSource = $state(false)

  // Toggle view source for a message
  async function toggleViewSource(msgId: string) {
    if (viewingSourceMessageId === msgId) {
      // Close source view
      viewingSourceMessageId = null
      messageSource = null
      return
    }

    viewingSourceMessageId = msgId
    loadingSource = true
    messageSource = null

    try {
      const source = await GetMessageSource(msgId)
      messageSource = source
    } catch {
      toasts.error($_('viewer.failedToLoadSource'))
      viewingSourceMessageId = null
    } finally {
      loadingSource = false
    }
  }

</script>

<div class="flex flex-col h-full {isFlashing ? 'pane-focus-flash' : ''}">
  {#if !threadId}
    <!-- No conversation selected -->
    <div class="flex flex-col items-center justify-center h-full text-muted-foreground">
      <Icon icon="mdi:email-open-outline" class="w-16 h-16 mb-4" />
      <p class="text-lg">{$_('viewer.selectConversation')}</p>
    </div>
  {:else if loading}
    <!-- Loading -->
    <div class="flex items-center justify-center h-full">
      <Icon icon="mdi:loading" class="w-8 h-8 animate-spin text-muted-foreground" />
    </div>
  {:else if error}
    <!-- Error -->
    <div class="flex flex-col items-center justify-center h-full text-center px-4">
      <Icon icon="mdi:alert-circle-outline" class="w-12 h-12 text-destructive mb-3" />
      <p class="text-destructive mb-2">{$_('viewer.failedToLoad')}</p>
      <p class="text-sm text-muted-foreground">{error}</p>
      <button
        class="mt-4 text-sm text-primary hover:underline"
        onclick={() => loadConversation(threadId!, folderId!)}
      >
        {$_('viewer.tryAgain')}
      </button>
    </div>
  {:else if conversation}
    <div class="conversation-viewer-content flex flex-col h-full">
    <!-- Header with Actions -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-border">
      <div class="flex items-center gap-2">
        {#if showBackButton}
          <button
            class="p-2 rounded-md hover:bg-muted transition-colors mr-1"
            title={$_('responsive.back')}
            aria-label={$_('aria.backToList')}
            onclick={onBack}
          >
            <Icon icon="mdi:arrow-left" class="w-5 h-5 text-muted-foreground" />
          </button>
          <div class="w-px h-5 bg-border mx-1"></div>
        {/if}
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('viewer.reply')}
          onclick={handleReply}
        >
          <Icon icon="mdi:reply" class="w-5 h-5 text-muted-foreground" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('viewer.replyAll')}
          onclick={handleReplyAll}
        >
          <Icon icon="mdi:reply-all" class="w-5 h-5 text-muted-foreground" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('viewer.forward')}
          onclick={handleForward}
        >
          <Icon icon="mdi:share" class="w-5 h-5 text-muted-foreground" />
        </button>

        <div class="w-px h-5 bg-border mx-1"></div>

        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('viewer.archive')}
          onclick={handleArchive}
        >
          <Icon icon="mdi:archive-outline" class="w-5 h-5 text-muted-foreground" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_(isTrashFolder ? 'viewer.deletePermanently' : 'viewer.delete')}
          onclick={handleDelete}
        >
          <Icon icon={isTrashFolder ? 'mdi:delete-forever' : 'mdi:delete-outline'} class="w-5 h-5 text-muted-foreground" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_(isSpamFolder ? 'viewer.markAsNotSpam' : 'viewer.markAsSpam')}
          onclick={handleSpam}
        >
          <Icon icon={isSpamFolder ? 'mdi:email-check-outline' : 'mdi:alert-octagon-outline'} class="w-5 h-5 text-muted-foreground" />
        </button>

        <div class="w-px h-5 bg-border mx-1"></div>

        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_(allStarred ? 'viewer.removeStar' : 'viewer.star')}
          onclick={handleStar}
        >
          <Icon icon={allStarred ? 'mdi:star' : 'mdi:star-outline'} class="w-5 h-5 {allStarred ? 'text-yellow-500' : 'text-muted-foreground'}" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_(allRead ? 'viewer.markAsUnread' : 'viewer.markAsRead')}
          onclick={handleMarkRead}
        >
          <Icon icon={allRead ? 'mdi:email-open-outline' : 'mdi:email-outline'} class="w-5 h-5 text-muted-foreground" />
        </button>
      </div>

      <div class="flex items-center gap-2">
        {#if conversation.messages && conversation.messages.length > 1}
          <button
            class="p-2 rounded-md hover:bg-muted transition-colors"
            title={$_('viewer.expandAll')}
            onclick={expandAll}
          >
            <Icon icon="mdi:unfold-more-horizontal" class="w-5 h-5 text-muted-foreground" />
          </button>
          <button
            class="p-2 rounded-md hover:bg-muted transition-colors"
            title={$_('viewer.collapseAll')}
            onclick={collapseAll}
          >
            <Icon icon="mdi:unfold-less-horizontal" class="w-5 h-5 text-muted-foreground" />
          </button>
        {/if}
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={inFocusMode && focusModeKind === 'thread' ? $_('viewer.exitFocus') : $_('viewer.focusThread')}
          onclick={onToggleThreadFocus}
        >
          <Icon icon={inFocusMode && focusModeKind === 'thread' ? 'mdi:fullscreen-exit' : 'mdi:fullscreen'} class="w-5 h-5 text-muted-foreground" />
        </button>
        <button
          class="p-2 rounded-md hover:bg-muted transition-colors"
          title={$_('viewer.print')}
          onclick={handlePrint}
        >
          <Icon icon="mdi:printer-outline" class="w-5 h-5 text-muted-foreground" />
        </button>
      </div>
    </div>

    <!-- Conversation Content -->
    <div bind:this={contentContainerRef} class="flex-1 min-h-0 overflow-y-auto scrollbar-thin" onfocusin={() => setFocusedPane('viewer')}>
      <div class="p-6">
        <!-- Subject -->
        <h1 class="text-xl font-semibold text-foreground mb-4">
          {conversation.subject || $_('viewer.noSubject')}
        </h1>

        <!-- Message Count Badge -->
        {#if conversation.messages && conversation.messages.length > 1}
          <div class="mb-4 text-sm text-muted-foreground">
            {$_('viewer.messagesInConversation', { values: { count: conversation.messages.length } })}
          </div>
        {/if}

        <!-- Stacked Messages -->
        {#if conversation.messages}
          <div class="space-y-4">
            {#each visibleMessages as msg, _index (msg.id)}
              {@const isExpanded = expandedMessages.has(msg.id)}
              {@const isFocusedMsg = inFocusMode && focusModeKind === 'message' && focusedMessageIdInFocus === msg.id}

              <!-- Wrap each message in its own context menu -->
              <MessageContextMenu
                messageIds={[msg.id]}
                accountId={accountId || ''}
                currentFolderId={folderId || ''}
                folderType={folderType || 'inbox'}
                isStarred={msg.isStarred}
                isRead={msg.isRead}
                onActionComplete={handleContextMenuActionComplete}
                {onReply}
              >
                <div
                  class="border rounded-lg overflow-hidden transition-all {focusedMessageId === msg.id ? 'border-primary ring-2 ring-primary/20' : 'border-border'}"
                  data-message-id={msg.id}
                  tabindex="-1"
                  role="button"
                  aria-expanded={isExpanded}
                  onfocus={() => focusedMessageId = msg.id}
                  onblur={() => { if (focusedMessageId === msg.id) focusedMessageId = null }}
                  onkeydown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      toggleMessage(msg.id)
                    }
                  }}
                >
                <!-- Message Header (always visible, clickable to expand/collapse) -->
                <div
                  class="w-full flex items-start gap-3 p-4 text-left hover:bg-muted/50 transition-colors cursor-pointer {!isExpanded ? 'bg-muted/30' : ''}"
                  onclick={() => toggleMessage(msg.id)}
                  onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') toggleMessage(msg.id) }}
                  onfocus={() => focusedMessageId = msg.id}
                  role="button"
                  tabindex="0"
                >
                  <!-- Sender circle (colored, with initials) -->
                  {#if getShowViewerCircles()}
                    <div
                      class="w-10 h-10 rounded-full flex-shrink-0 flex items-center justify-center text-sm font-medium {getAvatarColor(msg.fromEmail)}"
                    >
                      {getInitials(msg.fromName || msg.fromEmail)}
                    </div>
                  {/if}

                  <!-- Header Info -->
                  <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="font-medium text-foreground">{msg.fromName || $_('viewer.unknown')}</span>
                      <span
                        role="button"
                        tabindex="0"
                        class="text-sm text-muted-foreground hover:text-primary hover:underline cursor-pointer"
                        title={$_('viewer.copyEmail')}
                        onclick={(e) => { e.stopPropagation(); copyToClipboard(formatEmailForCopy(msg.fromName, msg.fromEmail), $_('viewer.from')) }}
                        onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); copyToClipboard(formatEmailForCopy(msg.fromName, msg.fromEmail), $_('viewer.from')) }}}
                      >&lt;{msg.fromEmail}&gt;</span>

                      <!-- Unread indicator -->
                      {#if !msg.isRead}
                        <span class="w-2 h-2 rounded-full bg-primary flex-shrink-0"></span>
                      {/if}
                    </div>

                    {#if msg.replyTo && msg.replyTo.toLowerCase() !== msg.fromEmail.toLowerCase()}
                      <div class="text-sm text-muted-foreground flex flex-wrap items-center gap-1">
                        <span class="opacity-60">{$_('viewer.replyTo')}</span>
                        <span
                          role="button"
                          tabindex="0"
                          class="hover:text-primary hover:underline cursor-pointer text-muted-foreground"
                          title={$_('viewer.copyEmail')}
                          onclick={(e) => { e.stopPropagation(); copyToClipboard(msg.replyTo!, $_('viewer.replyTo')) }}
                          onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); copyToClipboard(msg.replyTo!, $_('viewer.replyTo')) }}}
                        >{msg.replyTo}</span>
                      </div>
                    {/if}

                    {#if msg.toList}
                      {@const recipients = parseRecipients(msg.toList)}
                      <div class="text-sm text-muted-foreground flex flex-wrap items-center gap-1">
                        <span class="opacity-60">{$_('viewer.to')}</span>
                        {#each recipients as recipient, i (recipient.email + ':' + i)}
                          <span
                            role="button"
                            tabindex="0"
                            class="hover:text-primary hover:underline cursor-pointer text-muted-foreground"
                            title={$_('viewer.copyEmail')}
                            onclick={(e) => { e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.to')) }}
                            onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.to')) }}}
                          >{recipient.name || recipient.email}{i < recipients.length - 1 ? ',' : ''}</span>
                        {/each}
                      </div>
                    {/if}

                    {#if msg.ccList}
                      {@const ccRecipients = parseRecipients(msg.ccList)}
                      {#if ccRecipients.length > 0}
                        <div class="text-sm text-muted-foreground flex flex-wrap items-center gap-1">
                          <span class="opacity-60">{$_('viewer.cc')}</span>
                          {#each ccRecipients as recipient, i (recipient.email + ':' + i)}
                            <span
                              role="button"
                              tabindex="0"
                              class="hover:text-primary hover:underline cursor-pointer text-muted-foreground"
                              title={$_('viewer.copyEmail')}
                              onclick={(e) => { e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.cc')) }}
                              onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.cc')) }}}
                            >{recipient.name || recipient.email}{i < ccRecipients.length - 1 ? ',' : ''}</span>
                          {/each}
                        </div>
                      {/if}
                    {/if}

                    {#if msg.bccList}
                      {@const bccRecipients = parseRecipients(msg.bccList)}
                      {#if bccRecipients.length > 0}
                        <div class="text-sm text-muted-foreground flex flex-wrap items-center gap-1">
                          <span class="opacity-60">{$_('viewer.bcc')}</span>
                          {#each bccRecipients as recipient, i (recipient.email + ':' + i)}
                            <span
                              role="button"
                              tabindex="0"
                              class="hover:text-primary hover:underline cursor-pointer text-muted-foreground"
                              title={$_('viewer.copyEmail')}
                              onclick={(e) => { e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.bcc')) }}
                              onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); copyToClipboard(formatEmailForCopy(recipient.name, recipient.email), $_('viewer.bcc')) }}}
                            >{recipient.name || recipient.email}{i < bccRecipients.length - 1 ? ',' : ''}</span>
                          {/each}
                        </div>
                      {/if}
                    {/if}

                    {#if !isExpanded}
                      <!-- Show snippet when collapsed -->
                      <p class="text-sm text-muted-foreground truncate mt-1">
                        {msg.snippet || ''}
                      </p>
                    {/if}
                  </div>

                  <!-- Date, edit button (drafts), and expand icon -->
                  <div class="flex flex-col items-end gap-1 flex-shrink-0">
                    <div class="flex items-center gap-2">
                      <span class="text-sm text-muted-foreground">
                        {formatDate(msg.date)}
                      </span>
                      {#if isDraftsFolder}
                        <button
                          class="p-1 rounded hover:bg-muted transition-colors"
                          title={$_('viewer.editDraft')}
                          onclick={(e) => { e.stopPropagation(); onEditDraft?.(msg.id) }}
                        >
                          <Icon icon="mdi:pencil" class="w-4 h-4 text-muted-foreground" />
                        </button>
                      {/if}
                      <button
                        class="p-1 rounded hover:bg-muted transition-colors"
                        title={isFocusedMsg ? $_('viewer.exitFocus') : $_('viewer.focusMessage')}
                        onclick={(e) => { e.stopPropagation(); onToggleMessageFocus?.(msg.id) }}
                      >
                        <Icon icon={isFocusedMsg ? 'mdi:fullscreen-exit' : 'mdi:fullscreen'} class="w-4 h-4 text-muted-foreground" />
                      </button>
                      <Icon
                        icon={isExpanded ? 'mdi:chevron-up' : 'mdi:chevron-down'}
                        class="w-5 h-5 text-muted-foreground"
                      />
                    </div>
                    {#if getDarkMailContent() && getIsDarkActive()}
                      <button
                        class="text-xs leading-none px-1.5 py-0.5 rounded hover:bg-muted transition-colors"
                        title={shouldDarkenMessage(msg.id) ? $_('viewer.darkMailToLight') : $_('viewer.darkMailToDark')}
                        onclick={(e) => { e.stopPropagation(); toggleDarkMailOverride(msg.id) }}
                      >
                        {shouldDarkenMessage(msg.id) ? '☀️' : '🌛'}
                      </button>
                    {/if}
                  </div>
                </div>

                <!-- Message Body (visible when expanded) -->
                {#if isExpanded}
                  <div class="px-4 pb-4 pt-0">
                    <div class="ml-13 pl-3 border-l-2 border-border">
                      <!-- Read Receipt Banner -->
                      {#if shouldShowReadReceiptBanner(msg) && readReceiptPolicy === 'ask'}
                        <div class="flex items-center justify-between gap-3 px-3 py-2 mb-4 bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 rounded-md">
                          <div class="flex items-center gap-2 text-sm text-blue-700 dark:text-blue-300">
                            <Icon icon="mdi:email-check-outline" class="w-4 h-4 flex-shrink-0" />
                            <span>{$_('viewer.readReceiptRequested')}</span>
                          </div>
                          <div class="flex items-center gap-2">
                            <button
                              onclick={() => handleSendReadReceipt(msg.id, msg.accountId)}
                              disabled={sendingReadReceipt.has(msg.id)}
                              class="px-3 py-1 text-xs font-medium text-white bg-blue-600 hover:bg-blue-700 rounded transition-colors disabled:opacity-50"
                            >
                              {#if sendingReadReceipt.has(msg.id)}
                                <Icon icon="mdi:loading" class="w-3 h-3 animate-spin" />
                              {:else}
                                {$_('viewer.sendReceipt')}
                              {/if}
                            </button>
                            <button
                              onclick={() => handleIgnoreReadReceipt(msg.id, msg.accountId)}
                              class="px-3 py-1 text-xs font-medium text-blue-700 dark:text-blue-300 hover:bg-blue-100 dark:hover:bg-blue-900/50 rounded transition-colors"
                            >
                              {$_('viewer.ignoreReceipt')}
                            </button>
                          </div>
                        </div>
                      {:else if shouldShowReadReceiptBanner(msg) && readReceiptPolicy === 'always' && sendingReadReceipt.has(msg.id)}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md text-sm text-green-700 dark:text-green-300">
                          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
                          <span>{$_('viewer.sendingReadReceipt')}</span>
                        </div>
                      {/if}

                      <!-- S/MIME Loading Spinner (on-view processing) -->
                      {#if smimeLoading.has(msg.id)}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-muted/50 border border-border rounded-md text-sm text-muted-foreground">
                          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin flex-shrink-0" />
                          <span>{$_('viewer.processingSMIME')}</span>
                        </div>
                      {/if}

                      <!-- S/MIME Encryption Banner -->
                      {#if smimeResults[msg.id]?.smimeEncrypted}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 rounded-md text-sm text-blue-700 dark:text-blue-300">
                          <Icon icon="mdi:lock-check" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeEncryptedWith')}</span>
                        </div>
                      {/if}

                      <!-- S/MIME Signature Banner (on-view result for S/MIME messages, cached for non-S/MIME) -->
                      {#if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'signed'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md text-sm text-green-700 dark:text-green-300">
                          <Icon icon="mdi:shield-check" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeSignedBy', { values: { email: (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerEmail : msg.smimeSignerEmail) || (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerSubject : msg.smimeSignerSubject) || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'unknown_signer'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-700 dark:text-amber-300">
                          <Icon icon="mdi:shield-alert" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeUnknownSigner', { values: { email: (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerEmail : msg.smimeSignerEmail) || (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerSubject : msg.smimeSignerSubject) || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'self_signed'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-700 dark:text-amber-300">
                          <Icon icon="mdi:shield-alert" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeSelfSigned', { values: { email: (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerEmail : msg.smimeSignerEmail) || (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerSubject : msg.smimeSignerSubject) || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'expired_cert'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:shield-off" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeExpiredCert', { values: { email: (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerEmail : msg.smimeSignerEmail) || (msg.hasSMIME ? smimeResults[msg.id]?.smimeSignerSubject : msg.smimeSignerSubject) || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'invalid'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:shield-off" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeInvalid')}</span>
                        </div>
                      {:else if (msg.hasSMIME ? smimeResults[msg.id]?.smimeStatus : msg.smimeStatus) === 'decrypt_failed'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:lock-off" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.smimeDecryptFailed')}</span>
                        </div>
                      {/if}

                      <!-- PGP Loading Spinner (on-view processing) -->
                      {#if pgpLoading.has(msg.id)}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-muted/50 border border-border rounded-md text-sm text-muted-foreground">
                          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin flex-shrink-0" />
                          <span>{$_('viewer.processingPGP')}</span>
                        </div>
                      {/if}

                      <!-- PGP Encryption Banner -->
                      {#if pgpResults[msg.id]?.pgpEncrypted}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 rounded-md text-sm text-blue-700 dark:text-blue-300">
                          <Icon icon="mdi:lock-check" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpEncryptedWith')}</span>
                        </div>
                      {/if}

                      <!-- PGP Signature Banner -->
                      {#if pgpResults[msg.id]?.pgpStatus === 'signed'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md text-sm text-green-700 dark:text-green-300">
                          <Icon icon="mdi:key-check" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpSignedBy', { values: { email: pgpResults[msg.id]?.pgpSignerEmail || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if pgpResults[msg.id]?.pgpStatus === 'unknown_key'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-700 dark:text-amber-300">
                          <Icon icon="mdi:key-alert" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpUnknownKey', { values: { keyId: pgpResults[msg.id]?.pgpSignerKeyId || '' } })}</span>
                        </div>
                      {:else if pgpResults[msg.id]?.pgpStatus === 'expired_key'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-700 dark:text-amber-300">
                          <Icon icon="mdi:key-alert" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpExpiredKey', { values: { email: pgpResults[msg.id]?.pgpSignerEmail || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if pgpResults[msg.id]?.pgpStatus === 'revoked_key'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:key-remove" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpRevokedKey', { values: { email: pgpResults[msg.id]?.pgpSignerEmail || $_('viewer.unknown').toLowerCase() } })}</span>
                        </div>
                      {:else if pgpResults[msg.id]?.pgpStatus === 'invalid'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:key-remove" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpInvalid')}</span>
                        </div>
                      {:else if pgpResults[msg.id]?.pgpStatus === 'decrypt_failed'}
                        <div class="flex items-center gap-2 px-3 py-2 mb-4 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
                          <Icon icon="mdi:lock-off" class="w-4 h-4 flex-shrink-0" />
                          <span>{$_('viewer.pgpDecryptFailed')}</span>
                        </div>
                      {/if}

                      <!-- Body (use on-view result for S/MIME or PGP messages) -->
                      <div class="mb-4">
                        {#if (msg as any).bodyFetched === false && !msg.bodyHtml && !msg.bodyText}
                          <!-- Body not yet fetched (IDLE synced headers only) -->
                          <div class="flex items-center gap-2 text-muted-foreground text-sm italic py-4">
                            <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
                            {$_('viewer.downloadingContent')}
                          </div>
                        {:else if (msg.hasSMIME && !smimeResults[msg.id] && smimeLoading.has(msg.id)) || (msg.hasPGP && !pgpResults[msg.id] && pgpLoading.has(msg.id))}
                          <!-- Show placeholder while processing -->
                          <div class="text-muted-foreground text-sm italic py-4">{$_('viewer.decryptingMessage')}</div>
                        {:else}
                          <EmailBody
                            bind:this={emailBodyRefs[msg.id]}
                            messageId={msg.id}
                            accountId={msg.accountId}
                            bodyHtml={msg.hasPGP && pgpResults[msg.id] ? pgpResults[msg.id].bodyHtml : msg.hasSMIME && smimeResults[msg.id] ? smimeResults[msg.id].bodyHtml : msg.bodyHtml}
                            bodyText={msg.hasPGP && pgpResults[msg.id] ? pgpResults[msg.id].bodyText : msg.hasSMIME && smimeResults[msg.id] ? smimeResults[msg.id].bodyText : msg.bodyText}
                            fromEmail={msg.fromEmail}
                            onCompose={onComposeToAddress}
                            onImagesLoaded={() => messagesWithImagesLoaded.add(msg.id)}
                            encryptedInlineAttachments={pgpResults[msg.id]?.inlineAttachments ?? smimeResults[msg.id]?.inlineAttachments}
                            darken={shouldDarkenMessage(msg.id)}
                          />
                        {/if}
                      </div>

                      <!-- Attachments -->
                      {#if msg.hasAttachments || (pgpResults[msg.id]?.attachments?.length ?? 0) > 0 || (smimeResults[msg.id]?.attachments?.length ?? 0) > 0}
                        <div class="border-t border-border pt-4 mt-4">
                          <h3 class="text-sm font-medium text-foreground mb-3 flex items-center gap-2">
                            <Icon icon="mdi:paperclip" class="w-4 h-4" />
                            {$_('viewer.attachments')}
                          </h3>
                          <AttachmentList
                            messageId={msg.id}
                            encryptedAttachments={pgpResults[msg.id]?.attachments ?? smimeResults[msg.id]?.attachments}
                          />
                        </div>
                      {/if}

                      <!-- View Source Button -->
                      <div class="border-t border-border pt-4 mt-4">
                        <button
                          onclick={() => toggleViewSource(msg.id)}
                          class="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
                        >
                          <Icon icon={viewingSourceMessageId === msg.id ? 'mdi:code-tags' : 'mdi:code-tags'} class="w-4 h-4" />
                          {viewingSourceMessageId === msg.id ? $_('viewer.hideSource') : $_('viewer.viewSource')}
                        </button>

                        {#if viewingSourceMessageId === msg.id}
                          <div class="mt-3">
                            {#if loadingSource}
                              <div class="flex items-center gap-2 text-sm text-muted-foreground">
                                <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
                                {$_('viewer.loadingSource')}
                              </div>
                            {:else if messageSource}
                              <div class="relative">
                                <button
                                  onclick={() => copyToClipboard(messageSource || '', $_('viewer.viewSource'))}
                                  class="absolute top-2 right-2 p-1.5 rounded bg-muted hover:bg-muted/80 transition-colors"
                                  title={$_('viewer.copySource')}
                                >
                                  <Icon icon="mdi:content-copy" class="w-4 h-4" />
                                </button>
                                <pre class="text-xs bg-muted/50 p-4 rounded-md overflow-x-auto max-h-96 overflow-y-auto whitespace-pre-wrap break-all font-mono">{messageSource}</pre>
                              </div>
                            {/if}
                          </div>
                        {/if}
                      </div>
                    </div>
                  </div>
                {/if}
                </div>
              </MessageContextMenu>
            {/each}
          </div>
        {/if}
      </div>
    </div>
    </div>
  {/if}
</div>

<!-- Permanent Delete Confirmation Dialog -->
<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_('viewer.deleteConversationTitle')}
  description={$_('viewer.deleteConversationDescription')}
  confirmLabel={$_('viewer.deletePermanently')}
  variant="destructive"
  onConfirm={handleConfirmPermanentDelete}
  onCancel={() => showDeleteConfirm = false}
/>
