<script lang="ts">
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import * as Select from '$lib/components/ui/select'
  import { ColorPicker } from '$lib/components/ui/color-picker'
  import {
    providers,
    detectProvider,
    getCustomProvider,
    securityOptions,
    syncPeriodOptions,
    syncIntervalOptions,
    isOAuthProvider,
    allowsPasswordFallback,
    getOAuthProviderType,
    type EmailProvider,
    type OAuthProvider,
  } from '$lib/config/providers'
  import { oauthStore } from '$lib/stores/oauth.svelte'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs path
  import { account, certificate } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { GetAccountFoldersForMapping, GetAutoDetectedFolders, GetIdentities, AcceptCertificate } from '../../../../wailsjs/go/app/App'
  import CertificateDialog from './CertificateDialog.svelte'
  import { accountStore } from '$lib/stores/accounts.svelte'
  import { _ } from '$lib/i18n'

  // OAuth credentials to pass to parent
  export interface OAuthCredentials {
    provider: string
    accessToken: string
    refreshToken: string
    expiresIn: number
  }

  interface Props {
    /** Account to edit (null for new account) */
    editAccount?: account.Account | null
    /** Callback when form is submitted successfully */
    onSubmit?: (config: account.AccountConfig, oauthCredentials?: OAuthCredentials) => Promise<void>
    /** Callback when form is cancelled */
    onCancel?: () => void
    /** Callback for testing connection */
    onTestConnection?: (config: account.AccountConfig) => Promise<void>
  }

  let {
    editAccount = null,
    onSubmit,
    onCancel,
    onTestConnection: _onTestConnection,
  }: Props = $props()

  // Form state
  let step = $state<'provider' | 'details'>('provider')
  let selectedProvider = $state<EmailProvider | null>(null)
  let showAdvanced = $state(false)

  // OAuth state
  let authMethod = $state<'password' | 'oauth2'>('password')
  let oauthConfigured = $state<Record<OAuthProvider, boolean>>({
    google: false,
    microsoft: false,
  })
  let oauthInitialized = $state(false)

  // Form fields
  let name = $state('')
  let displayName = $state('')
  let color = $state('')
  let email = $state('')
  let prevEmail = $state('')
  let username = $state('')
  let password = $state('')
  let imapHost = $state('')
  let imapPort = $state(993)
  let imapSecurity = $state<string>('tls')
  let smtpHost = $state('')
  let smtpPort = $state(587)
  let smtpSecurity = $state<string>('starttls')
  let syncPeriodDays = $state<string>('180')
  let syncInterval = $state<string>('30') // Default: 30 minutes
  let readReceiptRequestPolicy = $state<string>('never')

  // Read receipt request policy options
  const readReceiptRequestOptions = $derived([
    { value: 'never', label: $_('account.neverRequest') },
    { value: 'ask', label: $_('account.askEachTime') },
    { value: 'always', label: $_('account.alwaysRequest') },
  ])

  // Helper functions to get labels
  function getSecurityLabel(value: string): string {
    return securityOptions.find(opt => opt.value === value)?.label || value
  }

  function getSyncPeriodLabel(value: string): string {
    const numValue = Number(value)
    const option = syncPeriodOptions.find(opt => opt.value === numValue)
    return option ? $_(option.labelKey) : `${value} days`
  }

  function getSyncIntervalLabel(value: string): string {
    const numValue = Number(value)
    const option = syncIntervalOptions.find(opt => opt.value === numValue)
    return option ? $_(option.labelKey) : `${value} min`
  }

  function getReadReceiptLabel(value: string): string {
    return readReceiptRequestOptions.find(opt => opt.value === value)?.label || value
  }

  // UI state
  let testing = $state(false)
  let testResult = $state<{ success: boolean; message: string } | null>(null)
  let submitting = $state(false)
  let errors = $state<Record<string, string>>({})
  let initialized = $state(false)

  // Certificate TOFU state
  let showCertDialog = $state(false)
  let pendingCertificate = $state<certificate.CertificateInfo | null>(null)

  // Folder mapping state
  let showFolderMapping = $state(false)
  let loadingFolders = $state(false)
  let availableFolders = $state<any[]>([])
  let autoDetectedFolders = $state<Record<string, string>>({})

  // Folder mapping values
  let sentFolderPath = $state('')
  let draftsFolderPath = $state('')
  let trashFolderPath = $state('')
  let spamFolderPath = $state('')
  let archiveFolderPath = $state('')
  let allMailFolderPath = $state('')
  let starredFolderPath = $state('')

  // Load folders for mapping UI
  async function loadFoldersForMapping() {
    if (!editAccount || availableFolders.length > 0) return

    loadingFolders = true
    try {
      availableFolders = await GetAccountFoldersForMapping(editAccount.id)
      autoDetectedFolders = await GetAutoDetectedFolders(editAccount.id)

      // Pre-select: use saved value if exists, otherwise auto-detected
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      sentFolderPath = editAccount.sentFolderPath || autoDetectedFolders.sent || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      draftsFolderPath = editAccount.draftsFolderPath || autoDetectedFolders.drafts || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      trashFolderPath = editAccount.trashFolderPath || autoDetectedFolders.trash || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      spamFolderPath = editAccount.spamFolderPath || autoDetectedFolders.spam || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      archiveFolderPath = editAccount.archiveFolderPath || autoDetectedFolders.archive || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      allMailFolderPath = editAccount.allMailFolderPath || autoDetectedFolders.all || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      starredFolderPath = editAccount.starredFolderPath || autoDetectedFolders.starred || ''
    } catch (err) {
      console.error('Failed to load folders for mapping:', err)
    } finally {
      loadingFolders = false
    }
  }

  // Initialize form when editing (only once)
  $effect(() => {
    if (editAccount && !initialized) {
      initialized = true
      step = 'details'
      name = editAccount.name
      email = editAccount.email
      username = editAccount.username
      imapHost = editAccount.imapHost
      imapPort = editAccount.imapPort
      imapSecurity = editAccount.imapSecurity
      smtpHost = editAccount.smtpHost
      smtpPort = editAccount.smtpPort
      smtpSecurity = editAccount.smtpSecurity
      syncPeriodDays = String(editAccount.syncPeriodDays)
      // @ts-ignore - syncInterval from backend
      syncInterval = String(editAccount.syncInterval ?? 30)
      readReceiptRequestPolicy = editAccount.readReceiptRequestPolicy || 'never'
      // @ts-ignore - authType from backend
      authMethod = editAccount.authType === 'oauth2' ? 'oauth2' : 'password'
      // @ts-ignore - color from backend
      color = editAccount.color || ''

      // Initialize folder mappings (will be populated when section is expanded)
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      sentFolderPath = editAccount.sentFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      draftsFolderPath = editAccount.draftsFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      trashFolderPath = editAccount.trashFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      spamFolderPath = editAccount.spamFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      archiveFolderPath = editAccount.archiveFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      allMailFolderPath = editAccount.allMailFolderPath || ''
      // @ts-ignore - wailsjs binding will have these fields after regeneration
      starredFolderPath = editAccount.starredFolderPath || ''

      // Try to detect provider
      selectedProvider = detectProvider(email) ?? getCustomProvider()
      showAdvanced = selectedProvider.id === 'custom'

      // Load display name from the default identity
      loadDisplayName(editAccount.id)
    }
  })

  // Load display name from the account's default identity
  async function loadDisplayName(accountId: string) {
    try {
      const identities = await GetIdentities(accountId)
      const defaultIdentity = identities?.find((id: any) => id.isDefault) || identities?.[0]
      if (defaultIdentity) {
        displayName = defaultIdentity.name || ''
      }
    } catch (err) {
      console.error('Failed to load display name:', err)
    }
  }

  // Initialize OAuth configuration check
  $effect(() => {
    if (!oauthInitialized) {
      oauthInitialized = true
      checkOAuthConfiguration()
      // Initialize OAuth event listeners
      oauthStore.initEvents()
    }
  })

  // Update authMethod when OAuth configuration finishes loading.
  // If a user selects a provider before the async OAuth check completes,
  // this corrects the auth method once we know OAuth is available.
  // Does NOT depend on authMethod — otherwise clicking "App Password"
  // gets immediately reverted back to oauth2.
  $effect(() => {
    const _ = oauthConfigured
    if (!editAccount && selectedProvider && canUseOAuth(selectedProvider)) {
      authMethod = 'oauth2'
    }
  })

  // Check which OAuth providers are configured
  async function checkOAuthConfiguration() {
    try {
      const [googleConfigured, microsoftConfigured] = await Promise.all([
        oauthStore.isProviderConfigured('google'),
        oauthStore.isProviderConfigured('microsoft'),
      ])
      oauthConfigured = {
        google: googleConfigured,
        microsoft: microsoftConfigured,
      }
    } catch (err) {
      console.error('Failed to check OAuth configuration:', err)
    }
  }

  // Check if the selected provider supports OAuth and it's configured
  function canUseOAuth(provider: EmailProvider | null): boolean {
    if (!provider) return false
    if (!isOAuthProvider(provider)) return false
    const oauthType = getOAuthProviderType(provider)
    if (!oauthType) return false
    return oauthConfigured[oauthType] ?? false
  }

  // Start OAuth flow for the selected provider
  async function startOAuthFlow() {
    if (!selectedProvider) return
    const oauthType = getOAuthProviderType(selectedProvider)
    if (!oauthType) return

    try {
      await oauthStore.startFlow(oauthType)
    } catch (err) {
      console.error('Failed to start OAuth flow:', err)
    }
  }

  // Cancel OAuth flow
  function cancelOAuthFlow() {
    oauthStore.cancelFlow()
  }

  // Copy-link fallback for OAuth waiting state — used when the browser fails
  // to open and the user needs to paste the URL manually.
  let oauthLinkCopied = $state(false)
  let oauthCopiedResetTimer: ReturnType<typeof setTimeout> | null = null
  async function handleCopyOAuthLink() {
    if (!oauthStore.authURL) return
    try {
      await navigator.clipboard.writeText(oauthStore.authURL)
      oauthLinkCopied = true
      if (oauthCopiedResetTimer) clearTimeout(oauthCopiedResetTimer)
      oauthCopiedResetTimer = setTimeout(() => { oauthLinkCopied = false }, 1500)
    } catch {
      toasts.error($_('viewer.failedToCopy'))
    }
  }

  // Get OAuth button text based on provider
  function getOAuthButtonText(provider: EmailProvider | null): string {
    if (!provider) return $_('account.signIn')
    const oauthType = getOAuthProviderType(provider)
    if (oauthType === 'google') return $_('account.signInWith', { values: { provider: 'Google' } })
    if (oauthType === 'microsoft') return $_('account.signInWith', { values: { provider: 'Microsoft' } })
    return $_('account.signIn')
  }

  // Get OAuth button icon based on provider
  function getOAuthButtonIcon(provider: EmailProvider | null): string {
    if (!provider) return 'mdi:login'
    const oauthType = getOAuthProviderType(provider)
    if (oauthType === 'google') return 'logos:google-icon'
    if (oauthType === 'microsoft') return 'logos:microsoft-icon'
    return 'mdi:login'
  }

  // Auto-fill settings when provider is selected
  function selectProvider(provider: EmailProvider) {
    selectedProvider = provider
    imapHost = provider.imap.host
    imapPort = provider.imap.port
    imapSecurity = provider.imap.security
    smtpHost = provider.smtp.host
    smtpPort = provider.smtp.port
    smtpSecurity = provider.smtp.security

    // Set auth method based on provider and configuration
    if (canUseOAuth(provider)) {
      authMethod = 'oauth2'
    } else {
      authMethod = 'password'
    }

    // Show advanced for custom provider
    showAdvanced = provider.id === 'custom'
    step = 'details'
  }

  // Auto-detect provider and auto-fill fields when email changes
  $effect(() => {
    if (!email) return

    // Auto-fill username with full email
    if (!username || username === prevEmail) {
      username = email
    }
    prevEmail = email

    // Try to detect provider
    const detected = detectProvider(email)
    if (detected && detected.id !== selectedProvider?.id) {
      selectProvider(detected)
    }

    // Auto-fill name from email if empty
    if (!name) {
      const localPart = email.split('@')[0]
      if (localPart) {
        name = localPart.charAt(0).toUpperCase() + localPart.slice(1)
      }
    }
  })

  // Build config from form fields
  function buildConfig(): account.AccountConfig {
    return new account.AccountConfig({
      name,
      displayName,
      color,
      email,
      username: username || email,
      password: authMethod === 'oauth2' ? '' : password,
      imapHost,
      imapPort,
      imapSecurity,
      smtpHost,
      smtpPort,
      smtpSecurity,
      authType: authMethod,
      syncPeriodDays: Number(syncPeriodDays),
      syncInterval: Number(syncInterval),
      readReceiptRequestPolicy,
      // Folder mappings
      sentFolderPath,
      draftsFolderPath,
      trashFolderPath,
      spamFolderPath,
      archiveFolderPath,
      allMailFolderPath,
      starredFolderPath,
    })
  }

  // Validate form
  function validate(): boolean {
    errors = {}

    if (!name.trim()) errors.name = $_('account.accountNameRequired')
    if (!displayName.trim()) errors.displayName = $_('account.displayNameRequired')
    if (!email.trim()) errors.email = $_('account.emailRequired')
    else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) errors.email = $_('account.invalidEmail')

    // Password is only required for password auth on new accounts
    if (authMethod === 'password' && !password && !editAccount) {
      errors.password = $_('account.passwordRequired')
    }

    // For OAuth, check that the flow completed successfully
    if (authMethod === 'oauth2' && !editAccount && !oauthStore.isFlowSuccess) {
      errors.oauth = $_('account.pleaseCompleteSignIn')
    }

    if (!imapHost.trim()) errors.imapHost = $_('account.imapHostRequired')
    if (!smtpHost.trim()) errors.smtpHost = $_('account.smtpHostRequired')
    if (imapPort < 1 || imapPort > 65535) errors.imapPort = $_('account.invalidPort')
    if (smtpPort < 1 || smtpPort > 65535) errors.smtpPort = $_('account.invalidPort')

    return Object.keys(errors).length === 0
  }

  // Test connection
  async function handleTestConnection() {
    if (!validate()) return

    testing = true
    testResult = null

    try {
      const result = await accountStore.testConnection(buildConfig())
      if (result.success) {
        testResult = { success: true, message: $_('account.connectionSuccessful') }
      } else if (result.certificateRequired && result.certificate) {
        pendingCertificate = result.certificate
        showCertDialog = true
      } else {
        testResult = { success: false, message: result.error || $_('account.connectionFailed') }
      }
    } catch (err) {
      console.error('Connection test failed:', err)
      testResult = {
        success: false,
        message: $_('account.connectionTestFailed'),
      }
    } finally {
      testing = false
    }
  }

  async function handleCertAcceptOnce() {
    if (!pendingCertificate) return
    await AcceptCertificate(imapHost, pendingCertificate, false)
    showCertDialog = false
    pendingCertificate = null
    handleTestConnection()
  }

  async function handleCertAcceptPermanently() {
    if (!pendingCertificate) return
    await AcceptCertificate(imapHost, pendingCertificate, true)
    showCertDialog = false
    pendingCertificate = null
    handleTestConnection()
  }

  function handleCertDecline() {
    showCertDialog = false
    pendingCertificate = null
    testResult = { success: false, message: $_('account.certificateDeclined') }
  }

  // Submit form
  async function handleSubmit(e: Event) {
    e.preventDefault()
    if (!validate()) return

    submitting = true
    testResult = null

    try {
      // Build OAuth credentials if using OAuth
      let oauthCredentials: OAuthCredentials | undefined
      if (authMethod === 'oauth2' && oauthStore.isFlowSuccess && oauthStore.flowResult) {
        // Note: The actual tokens are stored in the backend during the OAuth flow
        // We just pass the metadata so the parent can complete the account setup
        oauthCredentials = {
          provider: oauthStore.flowResult.provider,
          accessToken: '', // Tokens are handled by backend
          refreshToken: '', // Tokens are handled by backend
          expiresIn: oauthStore.flowResult.expiresIn,
        }
      }

      await onSubmit?.(buildConfig(), oauthCredentials)

      // Reset OAuth state on success
      if (authMethod === 'oauth2') {
        oauthStore.reset()
      }
    } catch (err) {
      console.error('Account save failed:', err)
      testResult = {
        success: false,
        message: $_('account.saveFailed'),
      }
    } finally {
      submitting = false
    }
  }

  // Go back to provider selection
  function goBackToProviders() {
    step = 'provider'
    testResult = null
  }
</script>

<form onsubmit={handleSubmit} class="space-y-6">
  {#if step === 'provider' && !editAccount}
    <!-- Step 1: Provider Selection -->
    <div class="space-y-4">
      <div class="text-center">
        <h3 class="text-lg font-medium">{$_('account.chooseProvider')}</h3>
        <p class="text-sm text-muted-foreground mt-1">
          {$_('account.chooseProviderHelp')}
        </p>
      </div>

      <div class="grid grid-cols-3 gap-3">
        {#each providers as provider (provider.id)}
          <button
            type="button"
            class="flex flex-col items-center gap-2 p-4 rounded-lg border border-input bg-background hover:bg-accent hover:text-accent-foreground transition-colors"
            onclick={() => selectProvider(provider)}
          >
            {#if provider.iconSrc}
              <img src={provider.iconSrc} alt={provider.name} class="w-8 h-8" />
            {:else}
              <Icon icon={provider.icon} class="w-8 h-8" />
            {/if}
            <span class="text-sm font-medium text-center">{provider.name}</span>
          </button>
        {/each}
      </div>

      <div class="flex justify-end pt-2">
        <Button variant="ghost" onclick={() => onCancel?.()}>
          {$_('common.cancel')}
        </Button>
      </div>
    </div>
  {:else}
    <!-- Step 2: Account Details -->
    <div class="space-y-4">
      {#if !editAccount}
        <button
          type="button"
          class="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
          onclick={goBackToProviders}
        >
          <Icon icon="mdi:arrow-left" class="w-4 h-4" />
          {$_('account.changeProvider')}
        </button>
      {/if}

      {#if selectedProvider?.notes}
        <div class="flex items-start gap-2 p-3 rounded-lg bg-amber-500/10 border border-amber-500/20">
          <Icon icon="mdi:information-outline" class="w-5 h-5 text-amber-500 flex-shrink-0 mt-0.5" />
          <p class="text-sm text-amber-600 dark:text-amber-400">
            {selectedProvider.notesKey ? $_(selectedProvider.notesKey) : selectedProvider.notes}
          </p>
        </div>
      {/if}

      <!-- Basic Fields -->
      <div class="grid gap-4">
        <div class="space-y-2">
          <Label for="name">{$_('account.accountName')}</Label>
          <div class="flex items-center gap-3">
            <ColorPicker value={color} onchange={(c) => color = c} />
            <Input
              id="name"
              type="text"
              placeholder={$_('account.accountNamePlaceholder')}
              bind:value={name}
              class={errors.name ? 'border-destructive' : ''}
            />
          </div>
          <p class="text-xs text-muted-foreground">
            {$_('account.colorHelp')}
          </p>
          {#if errors.name}
            <p class="text-sm text-destructive">{errors.name}</p>
          {/if}
        </div>

        <div class="space-y-2">
          <Label for="displayName">{$_('account.displayName')}</Label>
          <Input
            id="displayName"
            type="text"
            placeholder={$_('account.displayNamePlaceholder')}
            bind:value={displayName}
            class={errors.displayName ? 'border-destructive' : ''}
          />
          <p class="text-xs text-muted-foreground">
            {$_('account.displayNameHelp')}
          </p>
          {#if errors.displayName}
            <p class="text-sm text-destructive">{errors.displayName}</p>
          {/if}
        </div>

        <div class="space-y-2">
          <Label for="email">{$_('account.emailAddress')}</Label>
          <Input
            id="email"
            type="email"
            placeholder="you@example.com"
            bind:value={email}
            class={errors.email ? 'border-destructive' : ''}
          />
          {#if errors.email}
            <p class="text-sm text-destructive">{errors.email}</p>
          {/if}
        </div>

        <div class="space-y-2">
          <Label for="username">{$_('account.username')}</Label>
          <Input
            id="username"
            type="text"
            placeholder={$_('account.usernamePlaceholder')}
            bind:value={username}
          />
          <p class="text-xs text-muted-foreground">
            {$_('account.usernameHelp')}
          </p>
        </div>

        <!-- Authentication Section -->
        <div class="space-y-3">
          {#if canUseOAuth(selectedProvider) && !editAccount}
            <!-- OAuth Provider - Show Sign In Button -->
            <div class="space-y-3">
              <Label>{$_('account.authentication')}</Label>

              {#if allowsPasswordFallback(selectedProvider!)}
                <!-- Provider allows both OAuth and password -->
                <div class="flex gap-2">
                  <Button
                    type="button"
                    variant={authMethod === 'oauth2' ? 'default' : 'outline'}
                    size="sm"
                    onclick={() => authMethod = 'oauth2'}
                    class="flex-1"
                  >
                    <Icon icon={getOAuthButtonIcon(selectedProvider)} class="w-4 h-4 mr-2" />
                    OAuth
                  </Button>
                  <Button
                    type="button"
                    variant={authMethod === 'password' ? 'default' : 'outline'}
                    size="sm"
                    onclick={() => authMethod = 'password'}
                    class="flex-1"
                  >
                    <Icon icon="mdi:key" class="w-4 h-4 mr-2" />
                    {$_('account.appPassword')}
                  </Button>
                </div>
              {/if}

              {#if authMethod === 'oauth2'}
                <!-- OAuth Flow UI -->
                <div class="rounded-lg border border-border p-4 space-y-3">
                  {#if oauthStore.flowState === 'idle' || oauthStore.flowState === 'cancelled'}
                    <!-- Initial state - show sign in button -->
                    <Button
                      type="button"
                      variant="outline"
                      class="w-full h-12"
                      onclick={startOAuthFlow}
                    >
                      <Icon icon={getOAuthButtonIcon(selectedProvider)} class="w-5 h-5 mr-3" />
                      {getOAuthButtonText(selectedProvider)}
                    </Button>
                    <p class="text-xs text-muted-foreground text-center">
                      {$_('account.redirectToSignIn')}
                    </p>
                  {:else if oauthStore.flowState === 'pending'}
                    <!-- Waiting for OAuth callback -->
                    <div class="flex flex-col items-center gap-3 py-2">
                      <Icon icon="mdi:loading" class="w-8 h-8 animate-spin text-primary" />
                      <div class="text-center">
                        <p class="text-sm font-medium">{$_('account.waitingForAuth')}</p>
                        <p class="text-xs text-muted-foreground mt-1">
                          {$_('account.completeSignIn')}
                        </p>
                      </div>
                      {#if oauthStore.authURL}
                        <button
                          type="button"
                          class="text-xs text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 transition-colors"
                          onclick={handleCopyOAuthLink}
                        >
                          {oauthLinkCopied ? $_('account.linkCopied') : $_('viewer.copyLink')}
                          <Icon icon={oauthLinkCopied ? 'mdi:check' : 'mdi:content-copy'} class="w-3.5 h-3.5" />
                        </button>
                      {/if}
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onclick={cancelOAuthFlow}
                      >
                        {$_('common.cancel')}
                      </Button>
                    </div>
                  {:else if oauthStore.flowState === 'success'}
                    <!-- OAuth completed successfully -->
                    <div class="flex items-center gap-3 py-2">
                      <div class="flex-shrink-0 w-10 h-10 rounded-full bg-green-500/10 flex items-center justify-center">
                        <Icon icon="mdi:check" class="w-5 h-5 text-green-500" />
                      </div>
                      <div class="flex-1 min-w-0">
                        <p class="text-sm font-medium text-green-600 dark:text-green-400">
                          {$_('account.connectedSuccessfully')}
                        </p>
                        <p class="text-xs text-muted-foreground truncate">
                          {oauthStore.flowResult?.email}
                        </p>
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onclick={() => {
                          oauthStore.reset()
                        }}
                      >
                        <Icon icon="mdi:refresh" class="w-4 h-4" />
                      </Button>
                    </div>
                  {:else if oauthStore.flowState === 'error'}
                    <!-- OAuth failed -->
                    <div class="space-y-3">
                      <div class="flex items-start gap-3">
                        <div class="flex-shrink-0 w-10 h-10 rounded-full bg-destructive/10 flex items-center justify-center">
                          <Icon icon="mdi:alert" class="w-5 h-5 text-destructive" />
                        </div>
                        <div class="flex-1">
                          <p class="text-sm font-medium text-destructive">
                            {$_('account.authFailed')}
                          </p>
                          <p class="text-xs text-muted-foreground mt-1">
                            {$_('account.authFailed')}
                          </p>
                        </div>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        class="w-full"
                        onclick={startOAuthFlow}
                      >
                        {$_('account.tryAgain')}
                      </Button>
                    </div>
                  {/if}
                </div>
                {#if errors.oauth}
                  <p class="text-sm text-destructive">{errors.oauth}</p>
                {/if}
              {:else}
                <!-- Password field for app password -->
                <div class="space-y-2">
                  <Label for="password">{$_('account.appPassword')}</Label>
                  <Input
                    id="password"
                    type="password"
                    placeholder={$_('account.enterAppPassword')}
                    bind:value={password}
                    class={errors.password ? 'border-destructive' : ''}
                  />
                  {#if errors.password}
                    <p class="text-sm text-destructive">{errors.password}</p>
                  {/if}
                </div>
              {/if}
            </div>
          {:else if editAccount && editAccount.authType === 'oauth2'}
            <!-- Editing an OAuth account -->
            <div class="space-y-2">
              <Label>{$_('account.authentication')}</Label>
              <div class="rounded-lg border border-border p-4">
                <div class="flex items-center gap-3">
                  <div class="flex-shrink-0 w-10 h-10 rounded-full bg-primary/10 flex items-center justify-center">
                    <Icon icon={getOAuthButtonIcon(selectedProvider)} class="w-5 h-5" />
                  </div>
                  <div class="flex-1">
                    <p class="text-sm font-medium">{$_('account.oauthConnected')}</p>
                    <p class="text-xs text-muted-foreground">
                      {$_('account.signInAgainHelp')}
                    </p>
                  </div>
                </div>
              </div>
            </div>
          {:else}
            <!-- Standard password field -->
            <div class="space-y-2">
              <Label for="password">
                {selectedProvider?.notes?.includes('App Password') ? $_('account.appPassword') : $_('account.password')}
              </Label>
              <Input
                id="password"
                type="password"
                placeholder={editAccount ? $_('account.leaveEmptyToKeep') : $_('account.password')}
                bind:value={password}
                class={errors.password ? 'border-destructive' : ''}
              />
              {#if errors.password}
                <p class="text-sm text-destructive">{errors.password}</p>
              {/if}
            </div>
          {/if}
        </div>
      </div>

      <!-- Advanced Settings Toggle -->
      <button
        type="button"
        class="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
        onclick={() => (showAdvanced = !showAdvanced)}
      >
        <Icon
          icon={showAdvanced ? 'mdi:chevron-down' : 'mdi:chevron-right'}
          class="w-4 h-4"
        />
        {$_('account.advancedSettings')}
      </button>

      {#if showAdvanced}
        <div class="space-y-4 pt-2 border-t border-border">
          <!-- IMAP Settings -->
          <div class="space-y-3">
            <h4 class="text-sm font-medium">{$_('account.incomingMail')}</h4>
            <div class="grid grid-cols-2 gap-3">
              <div class="space-y-2">
                <Label for="imapHost">{$_('account.server')}</Label>
                <Input
                  id="imapHost"
                  type="text"
                  placeholder="imap.example.com"
                  bind:value={imapHost}
                  class={errors.imapHost ? 'border-destructive' : ''}
                />
                {#if errors.imapHost}
                  <p class="text-sm text-destructive">{errors.imapHost}</p>
                {/if}
              </div>
              <div class="grid grid-cols-2 gap-2">
                <div class="space-y-2">
                  <Label for="imapPort">{$_('account.port')}</Label>
                  <Input
                    id="imapPort"
                    type="number"
                    bind:value={imapPort}
                    class={errors.imapPort ? 'border-destructive' : ''}
                  />
                </div>
                <div class="space-y-2">
                  <Label>{$_('account.security')}</Label>
                  <Select.Root bind:value={imapSecurity}>
                    <Select.Trigger class="h-10">
                      <Select.Value placeholder="Select">
                        {getSecurityLabel(imapSecurity)}
                      </Select.Value>
                    </Select.Trigger>
                    <Select.Content>
                      {#each securityOptions as opt (opt.value)}
                        <Select.Item value={opt.value} label={opt.label} />
                      {/each}
                    </Select.Content>
                  </Select.Root>
                </div>
              </div>
            </div>
          </div>

          <!-- SMTP Settings -->
          <div class="space-y-3">
            <h4 class="text-sm font-medium">{$_('account.outgoingMail')}</h4>
            <div class="grid grid-cols-2 gap-3">
              <div class="space-y-2">
                <Label for="smtpHost">{$_('account.server')}</Label>
                <Input
                  id="smtpHost"
                  type="text"
                  placeholder="smtp.example.com"
                  bind:value={smtpHost}
                  class={errors.smtpHost ? 'border-destructive' : ''}
                />
                {#if errors.smtpHost}
                  <p class="text-sm text-destructive">{errors.smtpHost}</p>
                {/if}
              </div>
              <div class="grid grid-cols-2 gap-2">
                <div class="space-y-2">
                  <Label for="smtpPort">{$_('account.port')}</Label>
                  <Input
                    id="smtpPort"
                    type="number"
                    bind:value={smtpPort}
                    class={errors.smtpPort ? 'border-destructive' : ''}
                  />
                </div>
                <div class="space-y-2">
                  <Label>{$_('account.security')}</Label>
                  <Select.Root bind:value={smtpSecurity}>
                    <Select.Trigger class="h-10">
                      <Select.Value placeholder="Select">
                        {getSecurityLabel(smtpSecurity)}
                      </Select.Value>
                    </Select.Trigger>
                    <Select.Content>
                      {#each securityOptions as opt (opt.value)}
                        <Select.Item value={opt.value} label={opt.label} />
                      {/each}
                    </Select.Content>
                  </Select.Root>
                </div>
              </div>
            </div>
          </div>

          <!-- Sync Settings -->
          <div class="space-y-2">
            <Label>{$_('account.syncPeriod')}</Label>
            <Select.Root bind:value={syncPeriodDays}>
              <Select.Trigger>
                <Select.Value placeholder="Select">
                  {getSyncPeriodLabel(syncPeriodDays)}
                </Select.Value>
              </Select.Trigger>
              <Select.Content>
                {#each syncPeriodOptions as opt (opt.value)}
                  <Select.Item value={String(opt.value)} label={$_(opt.labelKey)} />
                {/each}
              </Select.Content>
            </Select.Root>
            <p class="text-xs text-muted-foreground">
              {$_('account.syncPeriodHelp')}
            </p>
          </div>

          <!-- Check Interval Settings -->
          <div class="space-y-2">
            <Label>{$_('account.checkNewMail')}</Label>
            <Select.Root bind:value={syncInterval}>
              <Select.Trigger>
                <Select.Value placeholder="Select">
                  {getSyncIntervalLabel(syncInterval)}
                </Select.Value>
              </Select.Trigger>
              <Select.Content>
                {#each syncIntervalOptions as opt (opt.value)}
                  <Select.Item value={String(opt.value)} label={$_(opt.labelKey)} />
                {/each}
              </Select.Content>
            </Select.Root>
            <p class="text-xs text-muted-foreground">
              {$_('account.checkNewMailHelp')}
            </p>
          </div>

          <!-- Read Receipt Settings -->
          <div class="space-y-2">
            <Label>{$_('account.requestReadReceipts')}</Label>
            <Select.Root bind:value={readReceiptRequestPolicy}>
              <Select.Trigger>
                <Select.Value placeholder="Select">
                  {getReadReceiptLabel(readReceiptRequestPolicy)}
                </Select.Value>
              </Select.Trigger>
              <Select.Content>
                {#each readReceiptRequestOptions as opt (opt.value)}
                  <Select.Item value={opt.value} label={opt.label} />
                {/each}
              </Select.Content>
            </Select.Root>
            <p class="text-xs text-muted-foreground">
              {$_('account.requestReadReceiptsHelp')}
            </p>
          </div>

          <!-- Folder Mapping -->
          <div class="space-y-2">
            <button
              type="button"
              class="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
              onclick={() => {
                showFolderMapping = !showFolderMapping
                if (showFolderMapping) loadFoldersForMapping()
              }}
              disabled={!editAccount}
            >
              <Icon
                icon={showFolderMapping ? 'mdi:chevron-down' : 'mdi:chevron-right'}
                class="w-4 h-4"
              />
              {$_('account.folderMapping')}
              {#if !editAccount}
                <span class="text-xs text-muted-foreground">{$_('account.saveAccountFirst')}</span>
              {/if}
            </button>

            {#if showFolderMapping}
              <div class="space-y-3 pl-6 pt-2 border-l border-border ml-2">
                <p class="text-xs text-muted-foreground">
                  {$_('account.folderMappingHelp2')}
                </p>

                {#if loadingFolders}
                  <div class="flex items-center gap-2 text-sm text-muted-foreground">
                    <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
                    {$_('account.loadingFolders')}
                  </div>
                {:else if availableFolders.length === 0}
                  <p class="text-sm text-muted-foreground">{$_('account.noFoldersAvailable')}</p>
                {:else}
                  <div class="grid gap-3">
                    <!-- Sent -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderSent')}:</Label>
                      <Select.Root bind:value={sentFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {sentFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.sent === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- Drafts -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderDrafts')}:</Label>
                      <Select.Root bind:value={draftsFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {draftsFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.drafts === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- Trash -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderTrash')}:</Label>
                      <Select.Root bind:value={trashFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {trashFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.trash === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- Spam/Junk -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderSpam')}:</Label>
                      <Select.Root bind:value={spamFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {spamFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.spam === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- Archive -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderArchive')}:</Label>
                      <Select.Root bind:value={archiveFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {archiveFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.archive === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- All Mail -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderAllMail')}:</Label>
                      <Select.Root bind:value={allMailFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {allMailFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.all === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <!-- Starred -->
                    <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                      <Label class="text-sm">{$_('account.folderStarred')}:</Label>
                      <Select.Root bind:value={starredFolderPath}>
                        <Select.Trigger class="h-9">
                          <Select.Value placeholder={$_('account.none')}>
                            {starredFolderPath || $_('account.none')}
                          </Select.Value>
                        </Select.Trigger>
                        <Select.Content>
                          <Select.Item value="" label={$_('account.none')} />
                          {#each availableFolders as f (f.path)}
                            <Select.Item value={f.path} label={f.path + (autoDetectedFolders.starred === f.path ? ' ' + $_('account.detected') : '')} />
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Test Result -->
      {#if testResult}
        <div
          class="flex items-start gap-2 p-3 rounded-lg {testResult.success
            ? 'bg-green-500/10 border border-green-500/20'
            : 'bg-destructive/10 border border-destructive/20'}"
        >
          <Icon
            icon={testResult.success ? 'mdi:check-circle' : 'mdi:alert-circle'}
            class="w-5 h-5 flex-shrink-0 mt-0.5 {testResult.success
              ? 'text-green-500'
              : 'text-destructive'}"
          />
          <p
            class="text-sm {testResult.success
              ? 'text-green-600 dark:text-green-400'
              : 'text-destructive'}"
          >
            {testResult.message}
          </p>
        </div>
      {/if}

      <!-- Actions -->
      <div class="flex items-center justify-between pt-4 border-t border-border">
        <Button
          type="button"
          variant="outline"
          onclick={handleTestConnection}
          disabled={testing || submitting}
        >
          {#if testing}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
          {:else}
            <Icon icon="mdi:connection" class="w-4 h-4 mr-2" />
          {/if}
          {$_('account.testConnection')}
        </Button>

        <div class="flex gap-2">
          <Button type="button" variant="ghost" onclick={onCancel} disabled={submitting}>
            {$_('common.cancel')}
          </Button>
          <Button type="submit" disabled={submitting || testing}>
            {#if submitting}
              <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
            {/if}
            {editAccount ? $_('common.saveChanges') : $_('account.addAccount')}
          </Button>
        </div>
      </div>
    </div>
  {/if}
</form>

<CertificateDialog
  bind:open={showCertDialog}
  certificate={pendingCertificate}
  onAcceptOnce={handleCertAcceptOnce}
  onAcceptPermanently={handleCertAcceptPermanently}
  onDecline={handleCertDecline}
/>
