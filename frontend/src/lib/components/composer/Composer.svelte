<script lang="ts">
  import { onMount, onDestroy, getContext, setContext, untrack } from 'svelte'
  import Icon from '@iconify/svelte'
  import type { Editor } from '@tiptap/core'
  import { createComposerEditor } from './composerEditor'
  // @ts-ignore - Wails generated imports
  import { smtp, account, contact, app } from '../../../../wailsjs/go/models'
  // @ts-ignore - Wails runtime for events
  import { EventsOn, EventsOff } from '../../../../wailsjs/runtime/runtime.js'
  import { type ComposerApi, COMPOSER_API_KEY, createMainWindowApi } from '$lib/composerApi'
  import { isImageAllowedSync } from '$lib/stores/imageAllowlist.svelte'
  import { getAlwaysLoadImages } from '$lib/stores/settings.svelte'
  
  // Attachment type from backend
  interface ComposerAttachment {
    filename: string
    contentType: string
    size: number
    data: string // base64 encoded
  }
  
  // Inline image type - for images pasted/dropped into the editor
  interface InlineImage {
    cid: string  // Content-ID (e.g., "image1@aerion")
    dataUrl: string  // Full data URL for display in editor
    contentType: string
    data: string  // Base64 data only (without data URL prefix)
    filename: string
  }
  import RecipientInput from './RecipientInput.svelte'
  import EditorToolbar from './EditorToolbar.svelte'
  import ComposerAttachmentList from './ComposerAttachmentList.svelte'
  import {
    addParagraphStyles,
    stripParagraphStyles,
    base64ToBytes,
    htmlToPlainText,
    parseFileUris,
    plainTextToHtml,
    readFileAsBase64,
    readFileAsDataUrl,
    textMentionsAttachment,
  } from './composerUtils'
  import {
    SIGNATURE_MARKER,
    buildSignatureHtml,
    shouldAppendSignature,
    insertSignatureIntoContent,
    removeSignatureFromContent,
    hasSignatureMarker,
    type ComposeMode,
  } from './composerSignature'
  import * as Select from '$lib/components/ui/select'
  import * as AlertDialog from '$lib/components/ui/alert-dialog'
  import Switch from '$lib/components/ui/switch/Switch.svelte'
  import { ConfirmDialog, ThreeOptionDialog } from '$lib/components/ui/confirm-dialog'
  import { addToast } from '$lib/stores/toast'
  import { getComposerFormat } from '$lib/stores/settings.svelte'
  import { _ } from '$lib/i18n'

  // Props
  interface Props {
    accountId: string
    /** Pre-populated message from backend (for reply/forward), or null for new message */
    initialMessage?: smtp.ComposeMessage | null
    /** Existing draft ID if editing a draft */
    draftId?: string | null
    /** Original message ID for reply/forward (needed for pop-out) */
    messageId?: string | null
    onClose?: () => void
    onSent?: () => void
    /** Optional API override - if not provided, uses context or creates main window API */
    api?: ComposerApi
    /** Whether this composer is in a detached window (hides pop-out button) */
    isDetached?: boolean
    /** Signal from parent (detached window) to trigger close flow */
    closeRequested?: boolean
    /** Callback when close request has been handled */
    onCloseHandled?: () => void
    /** Callback when recipient or subject changes (for dynamic window title) */
    onTitleChange?: (to: string, subject: string) => void
    /** Whether remote images were loaded in the viewer before reply/forward */
    imagesLoaded?: boolean
  }

  let { accountId, initialMessage = null, draftId = null, messageId = null, onClose, onSent, api: propApi, isDetached = false, closeRequested = false, onCloseHandled, onTitleChange, imagesLoaded = false }: Props = $props()

  // Get API from context, props, or create default main window API
  const contextApi = getContext<ComposerApi | undefined>(COMPOSER_API_KEY)
  const defaultApi = createMainWindowApi()
  // Resolve once at init — the API never changes after mount
  // svelte-ignore state_referenced_locally
  const resolvedApi: ComposerApi = propApi || contextApi || defaultApi
  // Use $derived so propApi changes are detected (even though it typically doesn't change after mount)
  const api: ComposerApi = $derived(propApi || contextApi || defaultApi)

  // Propagate the resolved API to child components (e.g. RecipientInput)
  // so they can access it via getContext instead of falling back to the main window API
  setContext(COMPOSER_API_KEY, resolvedApi)

  // State
  let allGroups = $state<app.AccountIdentityGroup[]>([])  // All accounts + identities (main window only)
  let identities = $state<account.Identity[]>([])  // Flat list of all identities (union)
  let selectedIdentityId = $state<string>('')

  // Derive the active account ID from the selected identity's accountId.
  // Falls back to the prop accountId if no identity is selected yet.
  let activeAccountId = $derived.by(() => {
    if (!selectedIdentityId) return accountId
    const identity = identities.find(i => i.id === selectedIdentityId)
    return identity?.accountId || accountId
  })
  let toRecipients = $state<smtp.Address[]>([])
  let ccRecipients = $state<smtp.Address[]>([])
  let bccRecipients = $state<smtp.Address[]>([])
  let subject = $state('')
  let showCc = $state(false)
  let showBcc = $state(false)
  let sending = $state(false)
  let poppingOut = $state(false)  // Pop-out in progress
  let editorElement = $state<HTMLElement | null>(null)
  let editor = $state<Editor | null>(null)
  
  // Track In-Reply-To and References for threading
  let inReplyTo = $state<string | undefined>(undefined)
  let references = $state<string[]>([])
  
  // Attachments
  let attachments = $state<ComposerAttachment[]>([])
  let isDraggingOver = $state(false)
  
  // Inline images (embedded in HTML body)
  let inlineImages = $state<InlineImage[]>([])
  let inlineImageCounter = 0  // Counter for generating unique CIDs
  
  // Read receipt request
  let requestReadReceipt = $state(false)
  let showReadReceiptOption = $state(false)  // Show checkbox when policy is 'ask'

  // S/MIME signing
  let signMessage = $state(false)
  let showSignOption = $state(false)  // Only show if account has a cert

  // S/MIME encryption
  let encryptMessage = $state(false)
  let showEncryptOption = $state(false)  // Only show if account has a cert
  let recipientCertStatus = $state<Record<string, boolean>>({})
  let missingCertRecipients = $derived.by(() => {
    if (!encryptMessage) return []
    const allRecipients = [...toRecipients, ...ccRecipients, ...bccRecipients]
    return allRecipients
      .map(r => r.address)
      .filter(email => email && recipientCertStatus[email] === false)
  })

  // PGP signing
  let pgpSignMessage = $state(false)
  let showPGPSignOption = $state(false)  // Only show if account has a PGP key

  // PGP encryption
  let pgpEncryptMessage = $state(false)
  let showPGPEncryptOption = $state(false)  // Only show if account has a PGP key
  let recipientPGPKeyStatus = $state<Record<string, boolean>>({})
  let missingPGPKeyRecipients = $derived.by(() => {
    if (!pgpEncryptMessage) return []
    const allRecipients = [...toRecipients, ...ccRecipients, ...bccRecipients]
    return allRecipients
      .map(r => r.address)
      .filter(email => email && recipientPGPKeyStatus[email] === false)
  })

  // Identity-aware cert/key info (for display in security bars)
  let smimeCertFingerprint = $state<string>('')  // First 8 hex chars of fingerprint
  let pgpKeyId = $state<string>('')  // Last 8 hex chars of fingerprint (short key ID)

  // Security mode for keyboard shortcuts (Alt+P / Alt+S activate, then s/e toggle sign/encrypt)
  let securityMode = $state<'pgp' | 'smime' | null>(null)

  // Plain text mode toggle (default from user setting, can be toggled per-message)
  let isPlainTextMode = $state(getComposerFormat() === 'plain')
  let plainTextContent = $state('')  // Store plain text when in plain text mode

  // Component refs
  let toolbarRef = $state<{ focus: () => void } | null>(null)
  let toInputRef = $state<{ focus: () => void } | null>(null)

  // Draft auto-save state
  let currentDraftId = $state<string | null>(null)
  let saveStatus = $state<'idle' | 'saving' | 'saved' | 'error'>('idle')

  // Initialize currentDraftId from prop (runs once on mount)
  $effect(() => {
    if (draftId && !currentDraftId) {
      currentDraftId = draftId
    }
  })

  // Check recipient certs when encrypt is toggled on or recipients change
  $effect(() => {
    if (!encryptMessage) return
    const allEmails = [...toRecipients, ...ccRecipients, ...bccRecipients]
      .map(r => r.address)
      .filter(Boolean)
    if (allEmails.length === 0) return
    checkRecipientCertsDebounced(allEmails)
  })

  let certCheckTimeout: ReturnType<typeof setTimeout> | null = null
  function checkRecipientCertsDebounced(emails: string[]) {
    if (certCheckTimeout) clearTimeout(certCheckTimeout)
    certCheckTimeout = setTimeout(async () => {
      try {
        recipientCertStatus = await api.checkRecipientCerts(emails)
      } catch (err) {
        console.error('Failed to check recipient certs:', err)
      }
    }, 300)
  }

  // Check recipient PGP keys when encrypt is toggled on or recipients change
  $effect(() => {
    if (!pgpEncryptMessage) return
    const allEmails = [...toRecipients, ...ccRecipients, ...bccRecipients]
      .map(r => r.address)
      .filter(Boolean)
    if (allEmails.length === 0) return
    checkRecipientPGPKeysDebounced(allEmails)
  })

  let pgpKeyCheckTimeout: ReturnType<typeof setTimeout> | null = null
  function checkRecipientPGPKeysDebounced(emails: string[]) {
    if (pgpKeyCheckTimeout) clearTimeout(pgpKeyCheckTimeout)
    pgpKeyCheckTimeout = setTimeout(async () => {
      try {
        recipientPGPKeyStatus = await api.checkRecipientPGPKeys(emails)

        // Auto-discover missing keys via unified WKD+HKP lookup
        const missingEmails = emails.filter(e => !recipientPGPKeyStatus[e])
        for (const email of missingEmails) {
          try {
            const armored = await api.lookupPGPKey(email)
            if (armored) {
              recipientPGPKeyStatus = { ...recipientPGPKeyStatus, [email]: true }
            }
          } catch { /* silent — lookup failure is not an error for the user */ }
        }
      } catch (err) {
        console.error('Failed to check recipient PGP keys:', err)
      }
    }, 300)
  }

  async function handleImportRecipientCert() {
    try {
      const filePath = await api.pickRecipientCertFile()
      if (!filePath) return
      // Import for the first missing recipient
      if (missingCertRecipients.length > 0) {
        await api.importRecipientCert(missingCertRecipients[0], filePath)
        addToast({ type: 'success', message: $_('composer.certImported', { values: { email: missingCertRecipients[0] } }) })
        // Re-check certs
        const allEmails = [...toRecipients, ...ccRecipients, ...bccRecipients]
          .map(r => r.address).filter(Boolean)
        recipientCertStatus = await api.checkRecipientCerts(allEmails)
      }
    } catch (err) {
      console.error('Failed to import recipient cert:', err)
      addToast({ type: 'error', message: $_('composer.failedToImportCert') })
    }
  }

  async function handleImportRecipientPGPKey() {
    try {
      const filePath = await api.pickRecipientPGPKeyFile()
      if (!filePath) return
      if (missingPGPKeyRecipients.length > 0) {
        await api.importRecipientPGPKey(missingPGPKeyRecipients[0], filePath)
        addToast({ type: 'success', message: $_('composer.pgpKeyImported', { values: { email: missingPGPKeyRecipients[0] } }) })
        const allEmails = [...toRecipients, ...ccRecipients, ...bccRecipients]
          .map(r => r.address).filter(Boolean)
        recipientPGPKeyStatus = await api.checkRecipientPGPKeys(allEmails)
      }
    } catch (err) {
      console.error('Failed to import recipient PGP key:', err)
      addToast({ type: 'error', message: $_('composer.failedToImportPGPKey') })
    }
  }

  let syncStatus = $state<'pending' | 'synced' | 'failed'>('pending') // IMAP sync status
  let lastSavedAt = $state<Date | null>(null)
  let saveTimeoutId: ReturnType<typeof setTimeout> | null = null
  let lastContent = ''  // Track content changes to avoid unnecessary saves

  // Computed draft status indicator
  let draftStatusIcon = $derived.by(() => {
    if (saveStatus === 'saving') return 'mdi:loading'
    if (saveStatus === 'error') return 'mdi:alert-circle'
    if (saveStatus !== 'saved' || !lastSavedAt) return ''
    if (encryptMessage || pgpEncryptMessage) {
      return syncStatus === 'synced' ? 'mdi:lock-check' : 'mdi:lock'
    }
    switch (syncStatus) {
      case 'synced': return 'mdi:cloud-check'
      case 'pending': return 'mdi:cloud-upload'
      case 'failed': return 'mdi:cloud-off-outline'
      default: return ''
    }
  })
  let draftStatusColor = $derived.by(() => {
    if (saveStatus === 'saving') return ''
    if (saveStatus === 'error') return 'text-red-500'
    if (saveStatus !== 'saved' || !lastSavedAt) return ''
    switch (syncStatus) {
      case 'synced': return 'text-green-500'
      case 'pending': return 'text-blue-500'
      case 'failed': return 'text-yellow-500'
      default: return ''
    }
  })
  let draftStatusLabel = $derived.by(() => {
    if (saveStatus === 'saving') return (encryptMessage || pgpEncryptMessage) ? $_('composer.encrypting') : $_('composer.saving')
    if (saveStatus === 'error') return $_('composer.saveFailed')
    if (saveStatus !== 'saved' || !lastSavedAt) return ''
    if (encryptMessage || pgpEncryptMessage) {
      switch (syncStatus) {
        case 'synced': return $_('composer.encryptedSynced')
        case 'pending': return $_('composer.encryptedDraft')
        case 'failed': return $_('composer.encryptedOffline')
        default: return ''
      }
    }
    switch (syncStatus) {
      case 'synced': return $_('composer.synced')
      case 'pending': return $_('composer.savedLocally')
      case 'failed': return $_('composer.savedLocallyOffline')
      default: return ''
    }
  })
  
  // 10-second debounce like Geary
  const DRAFT_SAVE_DELAY = 10000

  // Max inline image size (10 MB) — larger files should be added as regular attachments
  const MAX_INLINE_IMAGE_SIZE = 10 * 1024 * 1024

  // Whether remote images are blocked in the composer's quoted content
  let composerImagesBlocked = $state(false)

  // Max attachment size (100 MB) — server enforces its own limits for smaller caps
  const MAX_ATTACHMENT_SIZE = 100 * 1024 * 1024
  
  // Confirmation dialogs state
  let showEmptySubjectDialog = $state(false)
  let showMissingAttachmentDialog = $state(false)
  let showFlatpakDndDialog = $state(false)
  let showCloseConfirm = $state(false)
  let closeLoading = $state<'discard' | 'save' | null>(null)
  
  // Get only the user-composed text, excluding quoted/forwarded content
  function getUserComposedText(): string {
    const FWD_SEPARATOR = '---------- Forwarded message ----------'
    if (isPlainTextMode) {
      // Reply: citation line ending in "wrote:"
      const wroteIndex = plainTextContent.search(/^.*wrote:\s*$/m)
      // Forward: separator line
      const fwdIndex = plainTextContent.indexOf(FWD_SEPARATOR)
      // Take the earliest match
      const cutoff = [wroteIndex, fwdIndex].filter(i => i > -1)
      if (cutoff.length > 0) return plainTextContent.substring(0, Math.min(...cutoff))
      return plainTextContent
    }
    // In rich text, find the earliest of <blockquote (reply) or the forwarded message separator
    const html = editor?.getHTML() || ''
    const blockquoteIndex = html.indexOf('<blockquote')
    const fwdIndex = html.indexOf(FWD_SEPARATOR)
    const cutoffs = [blockquoteIndex, fwdIndex].filter(i => i > -1)
    const userHtml = cutoffs.length > 0 ? html.substring(0, Math.min(...cutoffs)) : html
    const tmp = document.createElement('div')
    tmp.innerHTML = userHtml
    return tmp.textContent || ''
  }

  // Check if the email body contains keywords that suggest an attachment should be present
  function bodyMentionsAttachment(): boolean {
    const combinedText = getUserComposedText() + ' ' + subject
    return textMentionsAttachment(combinedText)
  }

  // Determine display mode from initialMessage
  function getDisplayMode(): 'new' | 'reply' | 'reply-all' | 'forward' {
    if (!initialMessage) return 'new'
    if (initialMessage.subject?.startsWith('Fwd:')) return 'forward'
    if (initialMessage.in_reply_to) {
      // reply-all if there are multiple To recipients or any Cc
      if ((initialMessage.to?.length || 0) > 1 || (initialMessage.cc?.length || 0) > 0) {
        return 'reply-all'
      }
      return 'reply'
    }
    return 'new'
  }

  // Check if the composer has any meaningful content worth saving
  function hasContent(): boolean {
    const bodyText = isPlainTextMode ? plainTextContent.trim() : (editor?.getText()?.trim() || '')
    return toRecipients.length > 0 || ccRecipients.length > 0 || bccRecipients.length > 0 ||
           subject.trim() !== '' || bodyText !== '' || attachments.length > 0
  }

  // Collect any images in the editor DOM that aren't tracked in inlineImages.
  // WebKitGTK doesn't expose pasted screenshots via clipboardData, so TipTap's
  // default handler inserts them with a webkit-fake-url:// src. This function
  // extracts the pixel data via canvas and registers them for CID conversion.
  function collectUnregisteredInlineImages(html: string): string {
    const editorEl = editor?.view?.dom
    if (!editorEl) return html

    const imgs = editorEl.querySelectorAll('img')
    let result = html

    for (const img of imgs) {
      const src = img.getAttribute('src') || ''

      // Skip tracked, cid:, http(s):, and blocked remote images
      if (src.startsWith('cid:') || src.startsWith('http://') || src.startsWith('https://')) continue
      if (img.hasAttribute('data-original-src')) continue
      if (inlineImages.some(i => i.dataUrl === src)) continue

      // data: URLs — parse and register directly
      if (src.startsWith('data:')) {
        const match = src.match(/^data:([^;]+);base64,(.+)$/)
        if (!match) continue
        const cid = generateCID()
        inlineImages = [...inlineImages, {
          cid,
          dataUrl: src,
          contentType: match[1],
          data: match[2],
          filename: `pasted-image${inlineImageCounter}.${match[1].split('/')[1] || 'png'}`,
        }]
        continue
      }

      // webkit-fake-url://, blob:, etc. — extract via canvas
      if (!img.complete || img.naturalWidth === 0) continue
      try {
        const canvas = document.createElement('canvas')
        canvas.width = img.naturalWidth
        canvas.height = img.naturalHeight
        const ctx = canvas.getContext('2d')
        if (!ctx) continue
        ctx.drawImage(img, 0, 0)
        const dataUrl = canvas.toDataURL('image/png')
        const base64Data = dataUrl.split(',')[1]

        // Replace non-standard src with data URL in the HTML string
        result = result.replaceAll(src, dataUrl)

        const cid = generateCID()
        inlineImages = [...inlineImages, {
          cid,
          dataUrl,
          contentType: 'image/png',
          data: base64Data,
          filename: `pasted-image${inlineImageCounter}.png`,
        }]
      } catch {
        continue
      }
    }

    return result
  }

  // Convert HTML with data URLs to use CID references for inline images
  function convertDataUrlsToCid(html: string): string {
    let result = html
    
    // For each inline image, replace its data URL with cid: reference
    for (const img of inlineImages) {
      result = result.replaceAll(img.dataUrl, `cid:${img.cid}`)
    }
    
    return result
  }

  // Build message object from current composer state
  function buildMessage(): smtp.ComposeMessage {
    const selectedIdentity = identities.find(i => i.id === selectedIdentityId)
    
    // Handle plain text vs rich text mode
    let htmlContent: string
    let textContent: string
    
    if (isPlainTextMode) {
      // In plain text mode, we only have plain text
      textContent = plainTextContent
      htmlContent = ''  // No HTML version when composing in plain text
    } else {
      // In rich text mode, we have both
      // Collect any untracked pasted images (WebKitGTK webkit-fake-url, etc.),
      // add paragraph styles for email clients, then convert data URLs to CID references
      const rawHtml = collectUnregisteredInlineImages(editor?.getHTML() || '')
      htmlContent = convertDataUrlsToCid(addParagraphStyles(rawHtml))
      textContent = editor?.getText() || ''
    }

    // Restore blocked remote images for sending — replace placeholder with original URL
    htmlContent = htmlContent.replace(
      /<img([^>]*)\sdata-original-src="([^"]+)"([^>]*)>/gi,
      (match, _before, originalSrc, _after) => {
        return match
          .replace(/src="[^"]*"/, `src="${originalSrc}"`)
          .replace(/\s*data-original-src="[^"]*"/, '')
      }
    )

    // Convert ComposerAttachment to smtp.Attachment format (regular attachments)
    // Use content_base64 (string) instead of content (number[]) to avoid
    // pathologically slow JSON serialization of large byte arrays through Wails RPC.
    const smtpAttachments: smtp.Attachment[] = attachments.map(att => new smtp.Attachment({
      filename: att.filename,
      content_type: att.contentType,
      content_base64: att.data,
      content_id: '',
      inline: false,
    }))

    // Add inline images as inline attachments with Content-ID
    for (const img of inlineImages) {
      smtpAttachments.push(new smtp.Attachment({
        filename: img.filename,
        content_type: img.contentType,
        content_base64: img.data,
        content_id: img.cid,
        inline: true,
      }))
    }

    return new smtp.ComposeMessage({
      from: new smtp.Address({
        name: selectedIdentity?.name || '',
        address: selectedIdentity?.email || '',
      }),
      to: toRecipients,
      cc: ccRecipients,
      bcc: bccRecipients,
      subject: subject,
      html_body: htmlContent,
      text_body: textContent,
      attachments: smtpAttachments,
      in_reply_to: inReplyTo,
      references: references,
      request_read_receipt: requestReadReceipt,
      sign_message: signMessage,
      encrypt_message: encryptMessage,
      pgp_sign_message: pgpSignMessage,
      pgp_encrypt_message: pgpEncryptMessage,
    })
  }
  
  // Get a content hash to detect meaningful changes
  function getContentHash(): string {
    const bodyContent = isPlainTextMode ? plainTextContent : (editor?.getHTML() || '')
    const attachmentNames = attachments.map(a => a.filename).join(',')
    return `${toRecipients.length}|${ccRecipients.length}|${bccRecipients.length}|${subject}|${bodyContent}|${attachmentNames}|${isPlainTextMode}`
  }

  // Schedule a draft save (debounced)
  // Note: All expensive operations (hasContent, getContentHash) are inside the timeout
  // to avoid lag on every keystroke
  function scheduleDraftSave() {
    // Clear any pending save
    if (saveTimeoutId) {
      clearTimeout(saveTimeoutId)
    }

    // Reset indicator immediately when content changes (makes it disappear on input)
    if (saveStatus === 'saved') {
      saveStatus = 'idle'
    }

    saveTimeoutId = setTimeout(async () => {
      // Only save if there's content
      if (!hasContent()) {
        return
      }

      // Check if content actually changed
      const currentHash = getContentHash()
      if (currentHash === lastContent) {
        return
      }

      await saveDraft()
    }, DRAFT_SAVE_DELAY)
  }

  // Guard to prevent concurrent save requests (which cause orphaned drafts)
  let isSaving = false
  let discarding = false
  let savingComplete: Promise<void> = Promise.resolve()

  // Actually save the draft
  async function saveDraft() {
    if (discarding) return
    if (!hasContent()) return

    // If a save is already in flight, skip — next edit will trigger a fresh save
    if (isSaving) return

    // Check again for content changes before saving
    const currentHash = getContentHash()
    if (currentHash === lastContent && currentDraftId) {
      return  // No changes since last save
    }

    let resolveSaving: () => void
    savingComplete = new Promise<void>(resolve => { resolveSaving = resolve })

    isSaving = true
    saveStatus = 'saving'
    try {
      const message = buildMessage()
      const result = await api.saveDraft(activeAccountId, message, currentDraftId || '')
      currentDraftId = result.id
      lastContent = currentHash
      saveStatus = 'saved'
      syncStatus = result.syncStatus as 'pending' | 'synced' | 'failed'
      lastSavedAt = new Date()
    } catch (err) {
      console.error('Failed to save draft:', err)
      saveStatus = 'error'
    } finally {
      isSaving = false
      resolveSaving!()
    }
  }

  // Load blocked remote images in the composer editor
  function loadComposerImages() {
    if (!editor) return
    const imgs = editor.view.dom.querySelectorAll('img[data-original-src]')
    imgs.forEach(img => {
      const originalSrc = img.getAttribute('data-original-src')
      if (originalSrc) {
        img.setAttribute('src', originalSrc)
        img.removeAttribute('data-original-src')
      }
    })
    composerImagesBlocked = false
  }

  // Delete the current draft
  async function deleteDraft() {
    if (!currentDraftId) return

    try {
      await api.deleteDraft(currentDraftId)
      currentDraftId = null
    } catch (err) {
      console.error('Failed to delete draft:', err)
    }
  }

  // Watch for content changes and trigger auto-save
  $effect(() => {
    // Dependencies to watch
    const _ = [toRecipients, ccRecipients, bccRecipients, subject, signMessage, encryptMessage, pgpSignMessage, pgpEncryptMessage]
    // untrack prevents $effect from creating a reactive dependency on saveStatus
    // (which scheduleDraftSave reads), avoiding a circular re-run that causes flash
    untrack(() => scheduleDraftSave())
  })

  // Watch for close request from parent (detached window)
  $effect(() => {
    if (closeRequested) {
      handleClose()
    }
  })

  // Emit title info when recipients or subject change (for dynamic window title)
  $effect(() => {
    if (!onTitleChange) return
    const firstTo = toRecipients[0]
    const displayTo = firstTo?.name || firstTo?.address || ''
    onTitleChange(displayTo, subject)
  })

  // Track current signature for swapping when identity changes
  let currentSignatureHtml = $state<string>('')

  // Apply read receipt policy from account settings
  function applyReadReceiptPolicy(policy: string) {
    switch (policy) {
      case 'always':
        requestReadReceipt = true
        showReadReceiptOption = false
        break
      case 'ask':
        requestReadReceipt = false
        showReadReceiptOption = true
        break
      default:
        requestReadReceipt = false
        showReadReceiptOption = false
    }
  }

  // Initialize
  onMount(async () => {
    // Load identities — try cross-account first (main window), fall back to single-account (detached)
    try {
      if (api.getAllAccountIdentities) {
        const groups = await api.getAllAccountIdentities()
        allGroups = groups || []
        identities = allGroups.flatMap(g => g.identities || [])
      }
      if (!api.getAllAccountIdentities) {
        // Detached window — single account only
        identities = await api.getIdentities(accountId)
      }

      // Select identity: match reply recipient or use default for the initial account
      const matchedIdentity = selectIdentityForReply()
      const accountIdentities = identities.filter(i => i.accountId === accountId)
      const defaultIdentity = accountIdentities.find(i => i.isDefault) || accountIdentities[0]
      const selectedIdentity = matchedIdentity || defaultIdentity || identities[0]
      if (selectedIdentity) {
        selectedIdentityId = selectedIdentity.id
      }
    } catch (err) {
      console.error('Failed to load identities:', err)
    }

    // Load account's read receipt request policy (use activeAccountId which derives from selected identity)
    try {
      const acc = await api.getAccount(activeAccountId)
      applyReadReceiptPolicy(acc.readReceiptRequestPolicy || 'never')
    } catch (err) {
      console.error('Failed to load account settings:', err)
    }

    // Load S/MIME and PGP availability for the selected identity's email
    {
      const selectedIdentity = identities.find(i => i.id === selectedIdentityId)
      if (selectedIdentity) {
        await updateSecurityForIdentity(selectedIdentity.email)
      }
    }

    // Initialize TipTap editor
    if (editorElement) {
      editor = createComposerEditor(editorElement, {
        onUpdate: scheduleDraftSave,
        onPasteImage: handleInlineImageFile,
        onDropImage: handleInlineImageFile,
        onDropFile: handleDroppedFile,
        onDropFilePaths: handleDroppedFilePaths,
        onShiftTab: () => document.getElementById('composer-subject')?.focus(),
      })
    }

    // Initialize from initialMessage if provided (reply/forward)
    if (initialMessage) {
      initializeFromMessage()
      // Store initial content hash so we don't immediately save
      lastContent = getContentHash()
    }

    // Append signature for the selected identity (after editor is ready)
    // Only if signature doesn't already exist in content (e.g., from loaded draft)
    // Then focus the To field once everything is initialized
    setTimeout(() => {
      const identity = identities.find(i => i.id === selectedIdentityId)
      if (identity) {
        const content = editor?.getHTML() || ''
        // Don't append if signature marker already exists in the user's compose area.
        // Only check content before the quoted section — markers inside quoted history
        // (from previous replies with signatures) should not prevent injection.
        const quoteIdx = content.indexOf('<blockquote')
        const wroteIdx = content.search(/wrote:\s*(<br[^>]*>)?\s*<\/p>/i)
        const fwdIdx = content.indexOf('---------- Forwarded message ----------')
        const boundaries = [quoteIdx, wroteIdx, fwdIdx].filter(i => i > -1)
        const quoteBoundary = boundaries.length > 0 ? Math.min(...boundaries) : content.length
        const preQuoteContent = content.substring(0, quoteBoundary)
        if (!hasSignatureMarker(preQuoteContent)) {
          appendSignatureForIdentity(identity)
        }
      }
      // Focus editor body for reply/reply-all, To field for new/forward
      const mode = getDisplayMode()
      switch (mode) {
        case 'reply':
        case 'reply-all':
          editor?.commands.focus('start')
          break
        default:
          toInputRef?.focus()
      }
    }, 50)

    // Listen for draft sync status changes from backend
    EventsOn('draft:syncStatusChanged', (data: { draftId: string, syncStatus: string, imapUid: number, error: string }) => {
      if (data.draftId === currentDraftId) {
        syncStatus = data.syncStatus as 'pending' | 'synced' | 'failed'
      }
    })
  })

  // Select identity based on the From address the backend determined for reply/forward
  function selectIdentityForReply(): account.Identity | null {
    if (!initialMessage) return null

    // PrepareReply already determines the correct From based on the account
    // that owns the message. Match it to a local identity.
    const fromEmail = ((initialMessage.from as any)?.address || (initialMessage.from as any)?.email || '').toLowerCase()
    if (!fromEmail) return null

    return identities.find(identity =>
      identity.email.toLowerCase() === fromEmail
    ) || null
  }

  // Append signature for the current identity based on compose mode
  function appendSignatureForIdentity(identity: account.Identity) {
    if (!editor) return

    const mode = getDisplayMode()
    if (!shouldAppendSignature(identity, mode)) return

    const signatureHtml = buildSignatureHtml(identity)
    if (!signatureHtml) return

    currentSignatureHtml = signatureHtml

    const content = editor.getHTML()
    const newContent = insertSignatureIntoContent(
      content,
      signatureHtml,
      mode,
      identity.signaturePlacement || 'above'
    )

    editor.commands.setContent(newContent)
  }

  // Update security bar visibility based on the selected identity's email
  async function loadSMIMEForEmail(email: string) {
    const acctId = activeAccountId
    const cert = await api.getSMIMECertificateForEmail(acctId, email)
    if (!cert || cert.isExpired) {
      signMessage = false
      encryptMessage = false
      return
    }

    showSignOption = true
    showEncryptOption = true
    smimeCertFingerprint = cert.fingerprint ? cert.fingerprint.substring(0, 8).toUpperCase() : ''

    const [signPolicy, encryptPolicy] = await Promise.all([
      api.getSMIMESignPolicy(acctId),
      api.getSMIMEEncryptPolicy(acctId),
    ])
    signMessage = signPolicy === 'always'
    encryptMessage = encryptPolicy === 'always'
  }

  async function loadPGPForEmail(email: string) {
    const acctId = activeAccountId
    const key = await api.getPGPKeyForEmail(acctId, email)
    if (!key || key.isExpired) {
      pgpSignMessage = false
      pgpEncryptMessage = false
      return
    }

    showPGPSignOption = true
    showPGPEncryptOption = true
    pgpKeyId = key.fingerprint ? key.fingerprint.slice(-8).toUpperCase() : ''

    const [pgpSignPolicy, pgpEncryptPolicy] = await Promise.all([
      api.getPGPSignPolicy(acctId),
      api.getPGPEncryptPolicy(acctId),
    ])
    // Only enable PGP defaults if S/MIME is not already active (mutual exclusivity)
    pgpSignMessage = !signMessage && pgpSignPolicy === 'always'
    pgpEncryptMessage = !encryptMessage && pgpEncryptPolicy === 'always'
  }

  async function updateSecurityForIdentity(email: string) {
    // Reset all security state
    showSignOption = false
    showEncryptOption = false
    showPGPSignOption = false
    showPGPEncryptOption = false
    signMessage = false
    encryptMessage = false
    pgpSignMessage = false
    pgpEncryptMessage = false
    smimeCertFingerprint = ''
    pgpKeyId = ''

    if (!email) return

    try { await loadSMIMEForEmail(email) } catch (err) {
      console.error('Failed to load S/MIME settings:', err)
    }

    try { await loadPGPForEmail(email) } catch (err) {
      console.error('Failed to load PGP settings:', err)
    }
  }

  // Handle identity change from the From dropdown
  function handleIdentityChange(newIdentityId: string) {
    if (newIdentityId === selectedIdentityId) return

    const newIdentity = identities.find(i => i.id === newIdentityId)
    const oldAccountId = activeAccountId
    selectedIdentityId = newIdentityId

    if (!editor || !newIdentity) return

    // If account changed, reload read receipt policy and migrate draft
    if (newIdentity.accountId !== oldAccountId) {
      api.getAccount(newIdentity.accountId).then(acc => {
        applyReadReceiptPolicy(acc.readReceiptRequestPolicy || 'never')
      }).catch(err => {
        console.error('Failed to load account settings:', err)
      })

      // Delete old draft (belongs to previous account) and clear ID
      // so the next save creates a fresh draft under the new account
      if (currentDraftId) {
        const oldDraftId = currentDraftId
        currentDraftId = null
        lastContent = ''
        api.deleteDraft(oldDraftId).catch(err => {
          console.error('Failed to delete old account draft:', err)
        })
      }
    }

    // Update security bars for the new identity (uses activeAccountId which is now updated)
    updateSecurityForIdentity(newIdentity.email)

    // Remove old signature and apply new one
    const content = removeSignatureFromContent(editor.getHTML())
    editor.commands.setContent(content)

    appendSignatureForIdentity(newIdentity)
    scheduleDraftSave()
  }

  onDestroy(() => {
    // Unsubscribe from draft sync events
    EventsOff('draft:syncStatusChanged')
    // Clear any pending save timeout
    if (saveTimeoutId) {
      clearTimeout(saveTimeoutId)
    }
    editor?.destroy()
  })

  // Helper to ensure proper smtp.Address object (handles both 'address' and 'email' field names)
  function toSmtpAddress(addr: any): smtp.Address {
    if (!addr) return new smtp.Address({ name: '', address: '' })
    return new smtp.Address({
      name: addr.name || '',
      address: addr.address || addr.email || ''
    })
  }

  // Initialize composer fields from the pre-built message (from backend)
  function initializeFromMessage() {
    if (!initialMessage) return

    // Set recipients - ensure proper smtp.Address objects
    // The backend returns smtp.Address with 'address' field, but we need to handle
    // any edge cases where plain objects come through
    toRecipients = (initialMessage.to || []).map(toSmtpAddress)
    ccRecipients = (initialMessage.cc || []).map(toSmtpAddress)
    bccRecipients = (initialMessage.bcc || []).map(toSmtpAddress)

    // Show Cc field if there are Cc recipients
    if (ccRecipients.length > 0) {
      showCc = true
    }

    // Set subject
    subject = initialMessage.subject || ''

    // Set threading headers
    inReplyTo = initialMessage.in_reply_to
    references = initialMessage.references || []

    // Restore attachments and inline images from draft/reply/forward
    // Go []byte is serialized as base64 string via JSON, but TS type says number[]
    // content_base64 is used for efficient Wails RPC transfer (inline images in replies/forwards)
    let htmlBody = initialMessage.html_body || ''
    if (initialMessage.attachments?.length > 0) {
      for (const att of initialMessage.attachments) {
        const base64Data = att.content_base64 || (att.content as unknown as string)
        if (!base64Data) continue

        if (att.inline && att.content_id) {
          // Inline image - restore to inlineImages array and replace CID with data URL
          const dataUrl = `data:${att.content_type};base64,${base64Data}`
          inlineImages = [...inlineImages, {
            cid: att.content_id,
            dataUrl,
            contentType: att.content_type,
            data: base64Data,
            filename: att.filename,
          }]
          htmlBody = htmlBody.replaceAll(`cid:${att.content_id}`, dataUrl)
        } else if (!att.inline) {
          // Regular attachment
          attachments = [...attachments, {
            filename: att.filename,
            contentType: att.content_type,
            size: base64Data.length,
            data: base64Data,
          }]
        }
      }
      // Ensure new inline images get unique CIDs
      inlineImageCounter = Math.max(inlineImageCounter, inlineImages.length)
    }

    // Set editor content (with restored data URLs for inline images)
    // Strip email-client paragraph styles so TipTap doesn't double-space empty lines
    if (editor && htmlBody) {
      editor.commands.setContent(stripParagraphStyles(htmlBody))
      // Move cursor to beginning (before the quoted content)
      editor.commands.focus('start')
    }

    // Check for blocked remote images in quoted content.
    // If the sender is allowlisted or always-load is enabled, unblock immediately.
    if (htmlBody.includes('data-original-src')) {
      composerImagesBlocked = true
      const senderEmail = ((initialMessage.from as any)?.address || (initialMessage.from as any)?.email || '').toLowerCase()
      if (getAlwaysLoadImages() || imagesLoaded || (senderEmail && isImageAllowedSync(senderEmail))) {
        // Use setTimeout to ensure editor has rendered the content first
        setTimeout(() => loadComposerImages(), 0)
      }
    }

    // Restore S/MIME toggles from draft
    if (initialMessage.sign_message) {
      signMessage = true
    }
    if (initialMessage.encrypt_message) {
      encryptMessage = true
    }

    // Restore PGP toggles from draft
    if ((initialMessage as any).pgp_sign_message) {
      pgpSignMessage = true
    }
    if ((initialMessage as any).pgp_encrypt_message) {
      pgpEncryptMessage = true
    }
  }

  // Pre-send validation - returns true if we should proceed, false if waiting for confirmation
  function validateBeforeSend(): boolean {
    // Block send if encrypt is on but recipients are missing certs
    if (encryptMessage && missingCertRecipients.length > 0) {
      addToast({
        type: 'error',
        message: $_('composer.cannotEncryptMissingCert', { values: { emails: missingCertRecipients.join(', ') } }),
      })
      return false
    }

    // Block send if PGP encrypt is on but recipients are missing keys
    if (pgpEncryptMessage && missingPGPKeyRecipients.length > 0) {
      addToast({
        type: 'error',
        message: $_('composer.cannotEncryptMissingPGPKey', { values: { emails: missingPGPKeyRecipients.join(', ') } }),
      })
      return false
    }

    // Check for missing attachment
    if (attachments.length === 0 && bodyMentionsAttachment()) {
      showMissingAttachmentDialog = true
      return false
    }
    
    // Check for empty subject
    if (!subject.trim()) {
      showEmptySubjectDialog = true
      return false
    }
    
    return true
  }

  async function handleSend() {
    if (toRecipients.length === 0) {
      addToast({
        type: 'error',
        message: $_('composer.noRecipients'),
      })
      return
    }

    const selectedIdentity = identities.find(i => i.id === selectedIdentityId)
    if (!selectedIdentity) {
      addToast({
        type: 'error',
        message: $_('composer.selectSenderIdentity'),
      })
      return
    }

    // Run validations that may show confirmation dialogs
    if (!validateBeforeSend()) {
      return
    }

    await doSend()
  }
  
  // Actually send the message (called directly or after confirmation)
  async function doSend() {
    // Cancel any pending draft save
    if (saveTimeoutId) {
      clearTimeout(saveTimeoutId)
      saveTimeoutId = null
    }

    // Wait for any in-flight draft save to complete before sending
    await savingComplete

    sending = true

    try {
      const message = buildMessage()
      await api.sendMessage(activeAccountId, message)

      // Delete the draft on successful send (fire-and-forget - don't block UI)
      if (currentDraftId) {
        deleteDraft().catch(err => console.error('Failed to delete draft after send:', err))
      }

      addToast({
        type: 'success',
        message: $_('composer.messageSent'),
      })

      onSent?.()
      onClose?.()
    } catch (err) {
      console.error('Failed to send message:', err)
      addToast({
        type: 'error',
        message: $_('composer.failedToSend'),
      })
    } finally {
      sending = false
    }
  }
  
  // Handlers for confirmation dialogs
  function handleConfirmEmptySubject() {
    showEmptySubjectDialog = false
    // Check for missing attachment next (if applicable)
    if (attachments.length === 0 && bodyMentionsAttachment()) {
      showMissingAttachmentDialog = true
    } else {
      doSend()
    }
  }
  
  function handleConfirmMissingAttachment() {
    showMissingAttachmentDialog = false
    doSend()
  }

  function handleClose() {
    // Cancel any pending draft save
    if (saveTimeoutId) {
      clearTimeout(saveTimeoutId)
      saveTimeoutId = null
    }

    // Always show confirmation dialog (even for empty content, since a draft may have been saved)
    showCloseConfirm = true
  }

  // Discard: Delete draft from local DB and IMAP, then close
  async function handleDiscardAndClose() {
    discarding = true
    if (saveTimeoutId) {
      clearTimeout(saveTimeoutId)
      saveTimeoutId = null
    }
    await savingComplete
    closeLoading = 'discard'
    try {
      if (currentDraftId) {
        await api.deleteDraft(currentDraftId)
        currentDraftId = null
      }
    } catch (err) {
      console.error('Failed to delete draft:', err)
      // Still close even if delete fails
    }
    showCloseConfirm = false
    closeLoading = null
    onCloseHandled?.()
    onClose?.()
  }

  // Save & Close: Save current content as draft, then close
  async function handleSaveAndClose() {
    closeLoading = 'save'
    try {
      if (hasContent()) {
        await saveDraft()
      }
    } catch (err) {
      console.error('Failed to save draft:', err)
      // Still close even if save fails
    }
    showCloseConfirm = false
    closeLoading = null
    onCloseHandled?.()
    onClose?.()
  }

  // Keep Editing: Just close the dialog
  function handleKeepEditing() {
    showCloseConfirm = false
    onCloseHandled?.()
  }

  // Pop out to detached window
  async function handlePopOut() {
    if (!api.openComposerWindow) {
      // Not available in detached windows
      return
    }

    poppingOut = true

    try {
      // Save draft first to get a draft ID
      const message = buildMessage()
      const result = await api.saveDraft(activeAccountId, message, currentDraftId || '')
      const savedDraftId = result.id

      // Open detached composer window with the active account
      await api.openComposerWindow(
        activeAccountId,
        getDisplayMode(),
        messageId || '',
        savedDraftId
      )

      // Close this modal/inline composer
      onClose?.()
    } catch (err) {
      console.error('Failed to pop out composer:', err)
      addToast({
        type: 'error',
        message: $_('composer.failedToOpenComposer'),
      })
      poppingOut = false
    }
  }

  // Insert image via file picker
  function insertImage() {
    // Create a hidden file input, append to DOM (required for WebKitGTK),
    // then click it to open the file picker
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = 'image/*'
    input.style.display = 'none'
    document.body.appendChild(input)
    input.onchange = async (e) => {
      input.remove()
      const file = (e.target as HTMLInputElement).files?.[0]
      if (file) {
        await handleInlineImageFile(file)
      }
    }
    input.click()
  }

  // Toggle between rich text and plain text mode
  function togglePlainTextMode() {
    if (isPlainTextMode) {
      // Switching from plain text to rich text
      const html = plainTextToHtml(plainTextContent)
      editor?.commands.setContent(html)
      isPlainTextMode = false
    } else {
      // Switching from rich text to plain text
      plainTextContent = htmlToPlainText(editor?.getHTML() || '')
      isPlainTextMode = true
    }
    scheduleDraftSave()
  }

  // Keyboard shortcuts
  function handleKeyDown(e: KeyboardEvent) {
    // Security mode key handling (must be early in handleKeyDown)
    if (securityMode) {
      if (e.key === 'Escape') {
        e.preventDefault()
        securityMode = null
        return
      }
      if (e.key === 's' || e.key === 'S') {
        e.preventDefault()
        if (securityMode === 'pgp' && showPGPSignOption) {
          pgpSignMessage = !pgpSignMessage
          if (pgpSignMessage) signMessage = false
        } else if (securityMode === 'smime' && showSignOption) {
          signMessage = !signMessage
          if (signMessage) pgpSignMessage = false
        }
        return
      }
      if (e.key === 'e' || e.key === 'E') {
        e.preventDefault()
        if (securityMode === 'pgp' && showPGPEncryptOption) {
          pgpEncryptMessage = !pgpEncryptMessage
          if (pgpEncryptMessage) encryptMessage = false
        } else if (securityMode === 'smime' && showEncryptOption) {
          encryptMessage = !encryptMessage
          if (encryptMessage) pgpEncryptMessage = false
        }
        return
      }
      // Any other key exits security mode
      securityMode = null
    }

    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      handleSend()
    }
    if (e.key === 'd' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      handlePopOut()
    }
    // Alt+T to focus toolbar (hint mode)
    if (e.key === 't' && e.altKey) {
      e.preventDefault()
      toolbarRef?.focus()
    }
    // Alt+A to attach files
    if (e.key === 'a' && e.altKey) {
      e.preventDefault()
      handleAttachFiles()
    }
    // Alt+P / Alt+S to toggle security mode
    if (e.altKey && (e.key === 'p' || e.key === 's')) {
      if (e.key === 'p' && (showPGPSignOption || showPGPEncryptOption)) {
        e.preventDefault()
        securityMode = securityMode === 'pgp' ? null : 'pgp'
        return
      }
      if (e.key === 's' && (showSignOption || showEncryptOption)) {
        e.preventDefault()
        securityMode = securityMode === 'smime' ? null : 'smime'
        return
      }
    }
    if (e.key === 'Escape') {
      handleClose()
    }
  }
  
  // Generate a unique Content-ID for inline images
  function generateCID(): string {
    inlineImageCounter++
    return `image${inlineImageCounter}-${Date.now()}@aerion`
  }
  
  // Handle an inline image file (from paste or drop)
  async function handleInlineImageFile(file: File) {
    if (file.size > MAX_INLINE_IMAGE_SIZE) {
      addToast({
        type: 'error',
        message: $_('composer.imageTooLarge'),
      })
      return
    }

    try {
      const dataUrl = await readFileAsDataUrl(file)
      const cid = generateCID()
      
      // Extract base64 data and content type from data URL
      const matches = dataUrl.match(/^data:([^;]+);base64,(.+)$/)
      if (!matches) {
        console.error('Invalid data URL format')
        return
      }
      
      const contentType = matches[1]
      const base64Data = matches[2]
      
      // Store the inline image
      const inlineImage: InlineImage = {
        cid,
        dataUrl,
        contentType,
        data: base64Data,
        filename: file.name || `image${inlineImageCounter}.${contentType.split('/')[1] || 'png'}`,
      }
      inlineImages = [...inlineImages, inlineImage]
      
      // Insert the image into the editor with the data URL (for display)
      // When sending, we'll convert data URLs to cid: references
      editor?.chain().focus().setImage({ src: dataUrl, alt: inlineImage.filename }).run()
      
      scheduleDraftSave()
    } catch (err) {
      console.error('Failed to process inline image:', err)
      addToast({
        type: 'error',
        message: $_('composer.failedToInsertImage'),
      })
    }
  }
  
  // Handle a non-image File dropped on the editor (add as attachment)
  async function handleDroppedFile(file: File) {
    if (file.size > MAX_ATTACHMENT_SIZE) {
      addToast({ type: 'error', message: $_('composer.attachmentTooLarge') })
      return
    }

    try {
      const data = await readFileAsBase64(file)
      attachments = [...attachments, {
        filename: file.name,
        contentType: file.type || 'application/octet-stream',
        size: file.size,
        data,
      }]
      scheduleDraftSave()
    } catch (err) {
      console.error('Failed to read dropped file:', err)
    }
  }

  // Handle file paths dropped on the editor (from text/uri-list parsing)
  // Images are inserted inline, other files are added as attachments
  async function handleDroppedFilePaths(paths: string[]) {
    for (const filePath of paths) {
      try {
        const att = await api.readFileAsAttachment(filePath)
        if (!att) continue

        if (att.contentType.startsWith('image/')) {
          // Check size before inserting inline
          const imageBytes = Math.ceil((att.data.length * 3) / 4) // Estimate decoded size from base64
          if (imageBytes > MAX_INLINE_IMAGE_SIZE) {
            addToast({
              type: 'error',
              message: $_('composer.imageTooLarge'),
            })
            continue
          }
          // Insert as inline image
          const dataUrl = `data:${att.contentType};base64,${att.data}`
          const cid = generateCID()
          inlineImages = [...inlineImages, {
            cid,
            dataUrl,
            contentType: att.contentType,
            data: att.data,
            filename: att.filename,
          }]
          editor?.chain().focus().setImage({ src: dataUrl, alt: att.filename }).run()
          continue
        }
        // Add as regular attachment
        if (att.size > MAX_ATTACHMENT_SIZE) {
          addToast({ type: 'error', message: $_('composer.attachmentTooLarge') })
          continue
        }
        attachments = [...attachments, {
          filename: att.filename,
          contentType: att.contentType,
          size: att.size,
          data: att.data,
        }]
      } catch {
        // Direct read failed — if Flatpak, show permission info dialog
        if (await api.isFlatpak()) {
          showFlatpakDndDialog = true
        }
        return
      }
    }
    scheduleDraftSave()
  }

  // Attachment handling — uses HTML file input so WebKitGTK routes through
  // the FileChooser portal (required for Flatpak sandbox file access)
  function handleAttachFiles() {
    // Append to DOM before clicking (required for WebKitGTK to reliably
    // open the file chooser dialog on the first click)
    const input = document.createElement('input')
    input.type = 'file'
    input.multiple = true
    input.style.display = 'none'
    document.body.appendChild(input)
    input.onchange = async (e) => {
      input.remove()
      const fileList = (e.target as HTMLInputElement).files
      if (!fileList || fileList.length === 0) return

      try {
        const newAttachments: typeof attachments = []
        for (const file of Array.from(fileList)) {
          if (file.size > MAX_ATTACHMENT_SIZE) {
            addToast({ type: 'error', message: $_('composer.attachmentTooLarge') })
            continue
          }
          const dataUrl = await readFileAsDataUrl(file)
          const matches = dataUrl.match(/^data:([^;]+);base64,(.+)$/)
          if (!matches) continue

          newAttachments.push({
            filename: file.name,
            contentType: matches[1],
            size: file.size,
            data: matches[2],
          })
        }
        if (newAttachments.length > 0) {
          attachments = [...attachments, ...newAttachments]
          scheduleDraftSave()
        }
      } catch (err) {
        console.error('Failed to attach files:', err)
        addToast({
          type: 'error',
          message: $_('composer.failedToAttachFiles'),
        })
      }
    }
    input.click()
  }
  
  function removeAttachment(index: number) {
    attachments = attachments.filter((_, i) => i !== index)
    scheduleDraftSave()
  }
  
  // Drag and drop handlers
  function handleDragOver(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()
    isDraggingOver = true
  }
  
  function handleDragLeave(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()
    isDraggingOver = false
  }
  
  async function handleDrop(e: DragEvent) {
    e.stopPropagation()
    isDraggingOver = false

    // Already handled by TipTap editor's handleDrop
    if (e.defaultPrevented) return
    e.preventDefault()

    // Case 1: File objects from browser-internal drag operations
    const files = e.dataTransfer?.files
    if (files && files.length > 0) {
      const newAttachments: ComposerAttachment[] = []
      for (const file of Array.from(files)) {
        if (file.size > MAX_ATTACHMENT_SIZE) {
          addToast({ type: 'error', message: $_('composer.attachmentTooLarge') })
          continue
        }
        try {
          const data = await readFileAsBase64(file)
          newAttachments.push({
            filename: file.name,
            contentType: file.type || 'application/octet-stream',
            size: file.size,
            data,
          })
        } catch (err) {
          console.error('Failed to read dropped file:', err)
        }
      }
      if (newAttachments.length > 0) {
        attachments = [...attachments, ...newAttachments]
        scheduleDraftSave()
      }
      return
    }

    // Case 2: File URIs (drops outside editor — all as attachments)
    const uriList = e.dataTransfer?.getData('text/uri-list')
    const textData = e.dataTransfer?.getData('text/plain')
    const pathData = uriList || textData
    if (pathData) {
      const paths = parseFileUris(pathData)
      if (paths.length > 0) {
        let directReadFailed = false
        for (const filePath of paths) {
          try {
            const att = await api.readFileAsAttachment(filePath)
            if (!att) continue
            if (att.size > MAX_ATTACHMENT_SIZE) {
              addToast({ type: 'error', message: $_('composer.attachmentTooLarge') })
              continue
            }
            attachments = [...attachments, {
              filename: att.filename,
              contentType: att.contentType,
              size: att.size,
              data: att.data,
            }]
          } catch {
            directReadFailed = true
            break
          }
        }
        if (directReadFailed) {
          // If Flatpak, show permission info dialog
          if (await api.isFlatpak()) {
            showFlatpakDndDialog = true
          }
          return
        }
        scheduleDraftSave()
      }
    }
  }
  
</script>

<svelte:window on:keydown={handleKeyDown} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="flex flex-col h-full bg-background relative"
  class:ring-2={isDraggingOver}
  class:ring-primary={isDraggingOver}
  class:ring-inset={isDraggingOver}
  ondragover={handleDragOver}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
  role="region"
  aria-label={$_('aria.emailComposer')}
>
  <!-- Header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-border">
    <div class="flex items-center gap-3">
      <h2 class="text-lg font-semibold">
        {#if getDisplayMode() === 'new'}
          {$_('composer.newMessage')}
        {:else if getDisplayMode() === 'reply'}
          {$_('composer.reply')}
        {:else if getDisplayMode() === 'reply-all'}
          {$_('composer.replyAll')}
        {:else if getDisplayMode() === 'forward'}
          {$_('composer.forward')}
        {/if}
      </h2>
      <!-- Draft status indicator -->
      {#if draftStatusLabel}
        <span class="text-xs text-muted-foreground flex items-center gap-1">
          <Icon icon={draftStatusIcon} class="w-3 h-3 {draftStatusColor} {saveStatus === 'saving' ? 'animate-spin' : ''}" />
          {draftStatusLabel}
        </span>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      <!-- Pop-out button (only shown in main window, not detached) -->
      {#if !isDetached && api.openComposerWindow}
        <button
          onclick={handlePopOut}
          disabled={poppingOut || sending}
          class="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          title={$_('composer.openInNewWindow')}
        >
          {#if poppingOut}
            <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
          {:else}
            <Icon icon="mdi:open-in-new" class="w-4 h-4" />
          {/if}
        </button>
      {/if}
      <button
        onclick={handleClose}
        disabled={poppingOut}
        class="px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors disabled:opacity-50"
      >
        {$_('composer.close')}
      </button>
      <button
        onclick={handleSend}
        disabled={sending || poppingOut || toRecipients.length === 0}
        class="px-4 py-1.5 text-sm font-medium text-primary-foreground bg-primary hover:bg-primary/90 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
      >
        {#if sending}
          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
          {$_('composer.sending')}
        {:else if poppingOut}
          <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
          {$_('composer.opening')}
        {:else}
          <Icon icon="mdi:send" class="w-4 h-4" />
          {$_('composer.send')}
        {/if}
      </button>
    </div>
  </div>

  <!-- Compose form -->
  <div class="flex-1 flex flex-col min-h-0 overflow-hidden">
    <!-- From -->
    <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
      <span class="text-sm text-muted-foreground w-16">{$_('composer.from')}:</span>
      <div class="flex-1">
        <Select.Root value={selectedIdentityId} onValueChange={handleIdentityChange}>
          <Select.Trigger class="h-8 border-0 bg-transparent shadow-none focus:ring-0">
            <Select.Value placeholder={$_('composer.selectIdentity')}>
              {#if selectedIdentityId}
                {@const identity = identities.find(i => i.id === selectedIdentityId)}
                {#if identity}
                  {@const group = allGroups.find(g => g.account?.id === identity.accountId)}
                  {#if group?.account?.color}
                    <span class="inline-block w-2 h-2 rounded-full mr-1.5 flex-shrink-0" style="background-color: {group.account.color}"></span>
                  {/if}
                  {identity.name} &lt;{identity.email}&gt;
                {/if}
              {/if}
            </Select.Value>
          </Select.Trigger>
          <Select.Content>
            {#if allGroups.length > 0}
              <!-- Cross-account: grouped by account -->
              {#each allGroups as group (group.account?.id)}
                <Select.Group>
                  <Select.GroupHeading class="flex items-center gap-1.5 px-2 py-1 text-xs font-medium text-muted-foreground">
                    {#if group.account?.color}
                      <span class="inline-block w-2 h-2 rounded-full flex-shrink-0" style="background-color: {group.account.color}"></span>
                    {/if}
                    {group.account?.name || group.account?.email}
                  </Select.GroupHeading>
                  {#each group.identities || [] as identity (identity.id)}
                    <Select.Item value={identity.id} label="{identity.name} <{identity.email}>" />
                  {/each}
                </Select.Group>
              {/each}
            {:else}
              <!-- Single-account fallback (detached window) -->
              {#each identities as identity (identity.id)}
                <Select.Item value={identity.id} label="{identity.name} <{identity.email}>" />
              {/each}
            {/if}
          </Select.Content>
        </Select.Root>
      </div>
    </div>

    <!-- To -->
    <div class="flex items-start gap-2 px-4 py-2 border-b border-border">
      <span class="text-sm text-muted-foreground w-16 pt-1">{$_('composer.to')}:</span>
      <div class="flex-1">
        <RecipientInput
          bind:this={toInputRef}
          bind:recipients={toRecipients}
          placeholder={$_('composer.addRecipients')}
        />
      </div>
      {#if !showCc || !showBcc}
        <div class="flex items-center gap-1 text-sm text-muted-foreground">
          {#if !showCc}
            <button onclick={() => showCc = true} class="hover:text-foreground">{$_('composer.cc')}</button>
          {/if}
          {#if !showBcc}
            <button onclick={() => showBcc = true} class="hover:text-foreground">{$_('composer.bcc')}</button>
          {/if}
        </div>
      {/if}
    </div>

    <!-- Cc -->
    {#if showCc}
      <div class="flex items-start gap-2 px-4 py-2 border-b border-border">
        <span class="text-sm text-muted-foreground w-16 pt-1">{$_('composer.cc')}:</span>
        <div class="flex-1">
          <RecipientInput
            bind:recipients={ccRecipients}
            placeholder={$_('composer.addCcRecipients')}
          />
        </div>
      </div>
    {/if}

    <!-- Bcc -->
    {#if showBcc}
      <div class="flex items-start gap-2 px-4 py-2 border-b border-border">
        <span class="text-sm text-muted-foreground w-16 pt-1">{$_('composer.bcc')}:</span>
        <div class="flex-1">
          <RecipientInput
            bind:recipients={bccRecipients}
            placeholder={$_('composer.addBccRecipients')}
          />
        </div>
      </div>
    {/if}

    <!-- Subject -->
    <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
      <label for="composer-subject" class="text-sm text-muted-foreground w-16">{$_('composer.subject')}:</label>
      <input
        id="composer-subject"
        bind:value={subject}
        type="text"
        placeholder={$_('composer.subject')}
        class="flex-1 bg-transparent text-sm focus:outline-none"
        onkeydown={(e) => {
          // Tab skips security rows + toolbar and goes directly to body
          if (e.key === 'Tab' && !e.shiftKey) {
            e.preventDefault()
            editor?.commands.focus('start')
          }
        }}
      />
    </div>

    <!-- Security toggles -->
    {#if showPGPSignOption || showPGPEncryptOption}
      <div class="flex items-center px-4 py-3.5 border-b border-border text-xs {securityMode === 'pgp' ? 'bg-muted/50' : ''}">
        <div class="flex items-center gap-1.5">
          <Icon icon="mdi:lock-outline" class="w-3.5 h-3.5 text-muted-foreground flex-shrink-0" />
          <span class="text-muted-foreground font-medium">PGP</span>
          {#if pgpKeyId}
            <span class="text-muted-foreground">|</span>
            <span class="text-muted-foreground font-mono">{pgpKeyId}</span>
          {/if}
        </div>
        <div class="flex items-center gap-3 ml-auto">
          {#if securityMode === 'pgp'}
            <span class="text-muted-foreground">{$_('composer.securityModeHint')}</span>
          {/if}
          {#if showPGPSignOption}
            <div class="flex items-center gap-1.5" title={$_('composer.pgpSign')}>
              <span>{$_('composer.sign')}</span>
              <Switch bind:checked={pgpSignMessage} onCheckedChange={(v) => { if (v) { signMessage = false } }} class="scale-75 origin-left" />
            </div>
          {/if}
          {#if showPGPEncryptOption}
            <div class="flex items-center gap-1.5" title={$_('composer.pgpEncrypt')}>
              <span>{$_('composer.encrypt')}</span>
              <Switch bind:checked={pgpEncryptMessage} onCheckedChange={(v) => { if (v) { encryptMessage = false } }} class="scale-75 origin-left" />
            </div>
          {/if}
        </div>
      </div>
    {/if}
    {#if showSignOption || showEncryptOption}
      <div class="flex items-center px-4 py-3.5 border-b border-border text-xs {securityMode === 'smime' ? 'bg-muted/50' : ''}">
        <div class="flex items-center gap-1.5">
          <Icon icon="mdi:shield-outline" class="w-3.5 h-3.5 text-muted-foreground flex-shrink-0" />
          <span class="text-muted-foreground font-medium">S/MIME</span>
          {#if smimeCertFingerprint}
            <span class="text-muted-foreground">|</span>
            <span class="text-muted-foreground font-mono">{smimeCertFingerprint}</span>
          {/if}
        </div>
        <div class="flex items-center gap-3 ml-auto">
          {#if securityMode === 'smime'}
            <span class="text-muted-foreground">{$_('composer.securityModeHint')}</span>
          {/if}
          {#if showSignOption}
            <div class="flex items-center gap-1.5" title={$_('composer.smimeSign')}>
              <span>{$_('composer.sign')}</span>
              <Switch bind:checked={signMessage} onCheckedChange={(v) => { if (v) { pgpSignMessage = false } }} class="scale-75 origin-left" />
            </div>
          {/if}
          {#if showEncryptOption}
            <div class="flex items-center gap-1.5" title={$_('composer.smimeEncrypt')}>
              <span>{$_('composer.encrypt')}</span>
              <Switch bind:checked={encryptMessage} onCheckedChange={(v) => { if (v) { pgpEncryptMessage = false } }} class="scale-75 origin-left" />
            </div>
          {/if}
        </div>
      </div>
    {/if}

    <!-- Toolbar - extracted to separate component for performance -->
    <!-- Alt+T to focus toolbar, Tab skips it -->
    <EditorToolbar
      bind:this={toolbarRef}
      {editor}
      {isPlainTextMode}
      onTogglePlainText={togglePlainTextMode}
      onInsertImage={insertImage}
    />

    <!-- Remote images blocked bar -->
    {#if composerImagesBlocked}
      <div class="flex items-center gap-2 px-3 py-2 mx-2 mt-2 rounded-md bg-yellow-500/10 border border-yellow-500/30 text-sm">
        <Icon icon="mdi:image-off" class="w-4 h-4 text-yellow-600 flex-shrink-0" />
        <span class="text-yellow-700 dark:text-yellow-400">{$_('viewer.remoteImagesBlocked')}</span>
        <button
          class="ml-auto px-2 py-1 text-xs font-medium rounded bg-yellow-600 text-white hover:bg-yellow-700 transition-colors"
          onclick={loadComposerImages}
        >
          {$_('viewer.loadImages')}
        </button>
      </div>
    {/if}

    <!-- Editor -->
    <div class="flex-1 overflow-auto bg-white dark:bg-zinc-900">
      {#if isPlainTextMode}
        <textarea
          bind:value={plainTextContent}
          placeholder={$_('composer.writePlaceholder')}
          class="w-full h-full p-3 bg-transparent resize-none focus:outline-none font-mono text-sm"
          oninput={scheduleDraftSave}
        ></textarea>
      {:else}
        <div bind:this={editorElement} class="h-full"></div>
      {/if}
    </div>

    <!-- Attachments List -->
    <ComposerAttachmentList {attachments} onRemove={removeAttachment} />

    <!-- Missing S/MIME cert warning -->
    {#if encryptMessage && missingCertRecipients.length > 0}
      <div class="flex items-center gap-2 text-xs px-3 py-1.5 bg-amber-50 dark:bg-amber-950/30 border-t border-amber-200 dark:border-amber-800 text-amber-700 dark:text-amber-300">
        <Icon icon="mdi:alert" class="w-3.5 h-3.5 flex-shrink-0" />
        <span class="flex-1">{$_('composer.noCertFor', { values: { emails: missingCertRecipients.join(', ') } })}</span>
        <button onclick={handleImportRecipientCert} class="px-2 py-0.5 rounded bg-amber-200 dark:bg-amber-800 hover:bg-amber-300 dark:hover:bg-amber-700 font-medium transition-colors">{$_('composer.import')}</button>
        <button onclick={() => encryptMessage = false} class="px-2 py-0.5 rounded hover:bg-amber-200 dark:hover:bg-amber-800 font-medium transition-colors">{$_('common.cancel')}</button>
      </div>
    {/if}

    <!-- Missing PGP key warning -->
    {#if pgpEncryptMessage && missingPGPKeyRecipients.length > 0}
      <div class="flex items-center gap-2 text-xs px-3 py-1.5 bg-amber-50 dark:bg-amber-950/30 border-t border-amber-200 dark:border-amber-800 text-amber-700 dark:text-amber-300">
        <Icon icon="mdi:alert" class="w-3.5 h-3.5 flex-shrink-0" />
        <span class="flex-1">{$_('composer.noPGPKeyFor', { values: { emails: missingPGPKeyRecipients.join(', ') } })}</span>
        <button onclick={handleImportRecipientPGPKey} class="px-2 py-0.5 rounded bg-amber-200 dark:bg-amber-800 hover:bg-amber-300 dark:hover:bg-amber-700 font-medium transition-colors">{$_('composer.import')}</button>
        <button onclick={() => pgpEncryptMessage = false} class="px-2 py-0.5 rounded hover:bg-amber-200 dark:hover:bg-amber-800 font-medium transition-colors">{$_('common.cancel')}</button>
      </div>
    {/if}

    <!-- Footer -->
    <div class="flex items-center gap-2 px-4 py-2 border-t border-border text-sm text-muted-foreground">
      <button
        onclick={handleAttachFiles}
        class="flex items-center gap-1 hover:text-foreground transition-colors"
      >
        <Icon icon="mdi:attachment" class="w-4 h-4" />
        {$_('composer.attachFiles')}
      </button>
      {#if attachments.length > 0}
        <span class="text-xs">
          {$_('composer.filesAttached', { values: { count: attachments.length } })}
        </span>
      {/if}
      <div class="flex-1"></div>
      {#if showReadReceiptOption}
        <label class="flex items-center gap-1.5 text-xs cursor-pointer hover:text-foreground transition-colors">
          <input
            type="checkbox"
            bind:checked={requestReadReceipt}
            class="w-3.5 h-3.5 rounded border-border accent-primary"
          />
          {$_('composer.requestReadReceipt')}
        </label>
      {/if}
      <span class="text-xs">{$_('composer.ctrlEnterToSend')}</span>
    </div>
  </div>
  
  <!-- Drag overlay -->
  {#if isDraggingOver}
    <div class="absolute inset-0 bg-primary/10 flex items-center justify-center pointer-events-none z-10">
      <div class="bg-background border-2 border-dashed border-primary rounded-lg px-8 py-6 text-center">
        <Icon icon="mdi:attachment" class="w-12 h-12 text-primary mx-auto mb-2" />
        <p class="text-lg font-medium">{$_('composer.dropToAttach')}</p>
      </div>
    </div>
  {/if}
</div>

<!-- Empty Subject Confirmation Dialog -->
<AlertDialog.Root bind:open={showEmptySubjectDialog}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>{$_('composer.emptySubjectTitle')}</AlertDialog.Title>
      <AlertDialog.Description>
        {$_('composer.emptySubjectDescription')}
      </AlertDialog.Description>
    </AlertDialog.Header>
    <AlertDialog.Footer>
      <AlertDialog.Cancel>{$_('common.cancel')}</AlertDialog.Cancel>
      <AlertDialog.Action onclick={handleConfirmEmptySubject}>{$_('composer.sendAnywayGeneric')}</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>

<!-- Missing Attachment Confirmation Dialog -->
<AlertDialog.Root bind:open={showMissingAttachmentDialog}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>{$_('composer.missingAttachmentTitle')}</AlertDialog.Title>
      <AlertDialog.Description>
        {$_('composer.missingAttachmentDescription')}
      </AlertDialog.Description>
    </AlertDialog.Header>
    <AlertDialog.Footer>
      <AlertDialog.Cancel>{$_('common.cancel')}</AlertDialog.Cancel>
      <AlertDialog.Action onclick={handleConfirmMissingAttachment}>{$_('composer.sendAnywayGeneric')}</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>

<!-- Flatpak Drag-and-Drop Info Dialog -->
<AlertDialog.Root bind:open={showFlatpakDndDialog}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>{$_('composer.flatpakDndTitle')}</AlertDialog.Title>
      <AlertDialog.Description>
        <p class="mb-3">{$_('composer.flatpakDndDescription')}</p>
        <p class="mb-2">{$_('composer.flatpakDndGrantExample')}</p>
        <code class="block bg-muted px-3 py-2 rounded text-sm font-mono mb-3 select-all overflow-x-auto">flatpak override --user --filesystem=home io.github.hkdb.Aerion</code>
        <p class="mb-3 text-sm text-destructive">{$_('composer.flatpakDndSecurityWarning')}</p>
        <p class="text-sm text-muted-foreground">{$_('composer.flatpakDndAlternative')}</p>
      </AlertDialog.Description>
    </AlertDialog.Header>
    <AlertDialog.Footer>
      <AlertDialog.Action onclick={() => showFlatpakDndDialog = false}>{$_('common.ok')}</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>

<!-- Close Confirmation Dialog -->
<ThreeOptionDialog
  bind:open={showCloseConfirm}
  title={$_('composer.closeTitle')}
  description={$_('composer.closeDescription')}
  option1Label={$_('composer.discardDraft')}
  option2Label={$_('composer.saveAndClose')}
  option3Label={$_('composer.keepEditing')}
  option1Variant="destructive"
  option2Variant="default"
  loading={closeLoading === 'discard' ? 'option1' : closeLoading === 'save' ? 'option2' : null}
  onOption1={handleDiscardAndClose}
  onOption2={handleSaveAndClose}
  onOption3={handleKeepEditing}
/>

<style>
  /* Zero-margin paragraphs so Enter looks like a single line break */
  :global(.composer-editor p) {
    margin: 0;
    line-height: 1.25;
  }

  :global(.ProseMirror p.is-editor-empty:first-child::before) {
    color: #adb5bd;
    content: attr(data-placeholder);
    float: left;
    height: 0;
    pointer-events: none;
  }

  /* Table styling for composer */
  :global(.composer-editor table) {
    border-collapse: collapse;
    margin: 0;
    overflow: hidden;
    table-layout: fixed;
    width: 100%;
  }

  :global(.composer-editor td),
  :global(.composer-editor th) {
    border: 1px solid hsl(var(--border));
    box-sizing: border-box;
    min-width: 1em;
    padding: 6px 8px;
    position: relative;
    vertical-align: top;
  }

  :global(.composer-editor th) {
    background-color: hsl(var(--muted));
    font-weight: 600;
  }
</style>
