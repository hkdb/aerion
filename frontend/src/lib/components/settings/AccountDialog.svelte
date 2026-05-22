<script lang="ts">
  import Icon from '@iconify/svelte'
  import * as Dialog from '$lib/components/ui/dialog'
  import * as Tabs from '$lib/components/ui/tabs'
  import { Button } from '$lib/components/ui/button'
  import AccountForm, { type OAuthCredentials } from './AccountForm.svelte'
  import AccountGeneralTab from './account/AccountGeneralTab.svelte'
  import AccountIdentityTab from './account/AccountIdentityTab.svelte'
  import AccountServerTab from './account/AccountServerTab.svelte'
  import AccountSecurityTab from './account/AccountSecurityTab.svelte'
  import { accountStore } from '$lib/stores/accounts.svelte'
  import { oauthStore } from '$lib/stores/oauth.svelte'
  import { addToast } from '$lib/stores/toast'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { _ } from '$lib/i18n'
  // @ts-ignore - wailsjs path
  import { account } from '../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { GetIdentities } from '../../../../wailsjs/go/app/App'

  interface Props {
    /** Whether the dialog is open */
    open?: boolean
    /** Account to edit (null for new account) */
    editAccount?: account.Account | null
    /** Callback when dialog should close */
    onClose?: () => void
    /** Callback when account is successfully created/updated */
    onSuccess?: (account: account.Account) => void
  }

  let {
    open = $bindable(false),
    editAccount = null,
    onClose,
    onSuccess,
  }: Props = $props()

  // Tab state (for edit mode)
  let activeTab = $state('general')

  // Form state (for edit mode)
  let name = $state('')
  let displayName = $state('')
  let color = $state('')
  let email = $state('')
  let username = $state('')
  let password = $state('')
  let imapHost = $state('')
  let imapPort = $state(993)
  let imapSecurity = $state('tls')
  let smtpHost = $state('')
  let smtpPort = $state(587)
  let smtpSecurity = $state('starttls')
  let syncPeriodDays = $state('180')
  let syncInterval = $state('30')
  let syncAllFolders = $state(false)
  let syncFoldersEnabled = $state(false)
  let readReceiptRequestPolicy = $state('never')
  let authType = $state('password')

  // Folder mappings
  let sentFolderPath = $state('')
  let draftsFolderPath = $state('')
  let trashFolderPath = $state('')
  let spamFolderPath = $state('')
  let archiveFolderPath = $state('')
  let allMailFolderPath = $state('')
  let starredFolderPath = $state('')

  let saving = $state(false)
  let reauthorizing = $state(false)
  let reauthorizeSuccess = $state(false)
  let errors = $state<Record<string, string>>({})
  let initialized = $state(false)

  // Initialize form when editing
  $effect(() => {
    if (open && editAccount && !initialized) {
      initialized = true
      activeTab = 'general'

      // Load account values
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
      syncInterval = String(editAccount.syncInterval ?? 30)
      syncAllFolders = editAccount.syncAllFolders || false
      syncFoldersEnabled = editAccount.syncFoldersEnabled || false
      readReceiptRequestPolicy = editAccount.readReceiptRequestPolicy || 'never'
      authType = editAccount.authType || 'password'
      color = editAccount.color || ''

      // Folder mappings
      sentFolderPath = editAccount.sentFolderPath || ''
      draftsFolderPath = editAccount.draftsFolderPath || ''
      trashFolderPath = editAccount.trashFolderPath || ''
      spamFolderPath = editAccount.spamFolderPath || ''
      archiveFolderPath = editAccount.archiveFolderPath || ''
      allMailFolderPath = editAccount.allMailFolderPath || ''
      starredFolderPath = editAccount.starredFolderPath || ''

      // Load display name from the default identity
      loadDisplayName(editAccount.id)
    }
  })

  // Reset when dialog closes
  $effect(() => {
    if (!open) {
      initialized = false
      errors = {}
      password = ''
    }
  })

  // Activate the dialog guard while open: suppresses background refreshes
  // and routes global keyboard shortcuts (e.g. Ctrl+A) to the dialog inputs
  // instead of the message list / viewer behind it.
  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

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

  function validate(): boolean {
    errors = {}

    if (!name.trim()) errors.name = $_('account.accountNameRequired')
    if (!displayName.trim()) errors.displayName = $_('account.displayNameRequired')
    if (!imapHost.trim()) errors.imapHost = $_('account.imapHostRequired')
    if (!smtpHost.trim()) errors.smtpHost = $_('account.smtpHostRequired')
    if (imapPort < 1 || imapPort > 65535) errors.imapPort = $_('account.invalidPort')
    if (smtpPort < 1 || smtpPort > 65535) errors.smtpPort = $_('account.invalidPort')

    return Object.keys(errors).length === 0
  }

  async function handleSaveEdit() {
    if (!validate() || !editAccount) return

    saving = true
    try {
      const config = new account.AccountConfig({
        name,
        displayName,
        color,
        email,
        username: username || email,
        password: password, // Empty = keep current
        imapHost,
        imapPort,
        imapSecurity,
        smtpHost,
        smtpPort,
        smtpSecurity,
        authType,
        syncPeriodDays: Number(syncPeriodDays),
        syncInterval: Number(syncInterval),
        syncAllFolders,
        syncFoldersEnabled,
        readReceiptRequestPolicy,
        sentFolderPath,
        draftsFolderPath,
        trashFolderPath,
        spamFolderPath,
        archiveFolderPath,
        allMailFolderPath,
        starredFolderPath,
      })

      const result = await accountStore.updateAccount(editAccount.id, config)

      addToast({
        type: 'success',
        message: $_('toast.accountSaved'),
      })

      onSuccess?.(result)
      open = false
      onClose?.()
    } catch (err) {
      console.error('Failed to save account:', err)
      addToast({
        type: 'error',
        message: $_('toast.failedToSaveAccount'),
      })
    } finally {
      saving = false
    }
  }

  // Handlers for new account wizard (delegated to AccountForm)
  async function handleSubmit(config: account.AccountConfig, oauthCredentials?: OAuthCredentials) {
    let result: account.Account

    if (config.authType === 'oauth2' && oauthCredentials) {
      result = await accountStore.addOAuthAccount(
        oauthCredentials.provider,
        config.email,
        config.name,
        config.displayName,
        config.color
      )
    } else {
      result = await accountStore.addAccount(config)
    }

    onSuccess?.(result)
    open = false
    onClose?.()
  }

  async function handleTestConnection(config: account.AccountConfig) {
    if (config.authType === 'oauth2') {
      return
    }
    await accountStore.testConnection(config)
  }

  function handleCancel() {
    open = false
    onClose?.()
    oauthStore.cancelFlow()
  }

  function handleOpenChange(isOpen: boolean) {
    open = isOpen
    if (!isOpen) {
      onClose?.()
      oauthStore.cancelFlow()
    }
  }

  async function handleReauthorize() {
    if (!editAccount) return

    // Capture account details before async operations (editAccount could become stale)
    const accountId = editAccount.id
    const accountName = editAccount.name

    reauthorizing = true
    reauthorizeSuccess = false
    try {
      await oauthStore.reauthorize(accountId)
      reauthorizeSuccess = true
      addToast({
        type: 'success',
        message: $_('toast.reauthorized', { values: { name: accountName } }),
        duration: 5000,
      })
      // Trigger a sync to verify the new token works
      await accountStore.syncAccount(accountId)
      addToast({
        type: 'success',
        message: $_('toast.syncCompleted', { values: { name: accountName } }),
        duration: 3000,
      })
    } catch (err) {
      console.error('Failed to re-authorize:', err)
      reauthorizeSuccess = false
      addToast({
        type: 'error',
        message: $_('toast.failedToReauthorize'),
        duration: 8000,
      })
    } finally {
      reauthorizing = false
    }
  }
</script>

<Dialog.Root bind:open onOpenChange={handleOpenChange}>
  <Dialog.Content class="max-w-xl max-h-[90vh] overflow-hidden flex flex-col" preventCloseAutoFocus onInteractOutside={(e) => e.preventDefault()}>
    <Dialog.Header>
      <Dialog.Title>
        {editAccount?.sharedMailboxParentId ? $_('account.editSharedMailboxTitle') : editAccount ? $_('account.editTitle') : $_('account.addTitle')}
      </Dialog.Title>
      <Dialog.Description>
        {editAccount
          ? $_('account.editDescription')
          : $_('account.addDescription')}
      </Dialog.Description>
    </Dialog.Header>

    {#if editAccount}
      <!-- Edit Mode: Tabbed Interface -->
      <Tabs.Root bind:value={activeTab} class="flex-1 flex flex-col overflow-hidden">
        <Tabs.List class="grid w-full grid-cols-4">
          <Tabs.Trigger value="general" class="flex items-center gap-2">
            <Icon icon="mdi:cog" class="w-4 h-4" />
            {$_('account.general')}
          </Tabs.Trigger>
          <Tabs.Trigger value="identity" class="flex items-center gap-2">
            <Icon icon="mdi:account-multiple" class="w-4 h-4" />
            {$_('account.identity')}
          </Tabs.Trigger>
          <Tabs.Trigger value="server" class="flex items-center gap-2">
            <Icon icon="mdi:server" class="w-4 h-4" />
            {$_('account.server')}
          </Tabs.Trigger>
          <Tabs.Trigger value="security" class="flex items-center gap-2">
            <Icon icon="mdi:shield-lock-outline" class="w-4 h-4" />
            {$_('account.security')}
          </Tabs.Trigger>
        </Tabs.List>

        <div class="flex-1 overflow-y-auto mt-4 pr-2" style="max-height: calc(90vh - 220px);">
          <Tabs.Content value="general" class="mt-0">
            <AccountGeneralTab
              {editAccount}
              bind:name
              bind:displayName
              bind:color
              bind:email
              bind:username
              bind:password
              bind:syncPeriodDays
              {authType}
              {errors}
              {reauthorizing}
              {reauthorizeSuccess}
              onNameChange={(v) => name = v}
              onDisplayNameChange={(v) => displayName = v}
              onColorChange={(v) => color = v}
              onUsernameChange={(v) => username = v}
              onPasswordChange={(v) => password = v}
              onSyncPeriodChange={(v) => syncPeriodDays = v}
              onReauthorize={handleReauthorize}
            />
          </Tabs.Content>

          <Tabs.Content value="identity" class="mt-0">
            <AccountIdentityTab accountId={editAccount.id} {editAccount} />
          </Tabs.Content>

          <Tabs.Content value="security" class="mt-0">
            <AccountSecurityTab accountId={editAccount.id} />
          </Tabs.Content>

          <Tabs.Content value="server" class="mt-0">
            <AccountServerTab
              {editAccount}
              bind:imapHost
              bind:imapPort
              bind:imapSecurity
              bind:smtpHost
              bind:smtpPort
              bind:smtpSecurity
              bind:syncInterval
              bind:readReceiptRequestPolicy
              bind:sentFolderPath
              bind:draftsFolderPath
              bind:trashFolderPath
              bind:spamFolderPath
              bind:archiveFolderPath
              bind:allMailFolderPath
              bind:starredFolderPath
              {errors}
              onImapHostChange={(v) => imapHost = v}
              onImapPortChange={(v) => imapPort = v}
              onImapSecurityChange={(v) => imapSecurity = v}
              onSmtpHostChange={(v) => smtpHost = v}
              onSmtpPortChange={(v) => smtpPort = v}
              onSmtpSecurityChange={(v) => smtpSecurity = v}
              onSyncIntervalChange={(v) => syncInterval = v}
              onReadReceiptPolicyChange={(v) => readReceiptRequestPolicy = v}
              bind:syncAllFolders
              onSyncAllFoldersChange={(v) => syncAllFolders = v}
              bind:syncFoldersEnabled
              onSyncFoldersEnabledChange={(v) => syncFoldersEnabled = v}
              onFolderMappingChange={(type, v) => {
                switch (type) {
                  case 'sent': sentFolderPath = v; break
                  case 'drafts': draftsFolderPath = v; break
                  case 'trash': trashFolderPath = v; break
                  case 'spam': spamFolderPath = v; break
                  case 'archive': archiveFolderPath = v; break
                  case 'all': allMailFolderPath = v; break
                  case 'starred': starredFolderPath = v; break
                }
              }}
            />
          </Tabs.Content>
        </div>

        <!-- Actions for General/Server tabs (not Identity - it has its own save) -->
        {#if activeTab === 'identity' || activeTab === 'security'}
          <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
            <Button variant="ghost" onclick={handleCancel}>
              {$_('common.close')}
            </Button>
          </div>
        {:else}
          <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
            <Button variant="ghost" onclick={handleCancel} disabled={saving}>
              {$_('common.cancel')}
            </Button>
            <Button onclick={handleSaveEdit} disabled={saving}>
              {#if saving}
                <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
              {/if}
              {$_('common.saveChanges')}
            </Button>
          </div>
        {/if}
      </Tabs.Root>
    {:else}
      <!-- New Account Mode: Wizard -->
      <div class="flex-1 overflow-y-auto pr-2 pb-4" style="max-height: calc(90vh - 140px);">
        <AccountForm
          {editAccount}
          onSubmit={handleSubmit}
          onTestConnection={handleTestConnection}
          onCancel={handleCancel}
        />
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
