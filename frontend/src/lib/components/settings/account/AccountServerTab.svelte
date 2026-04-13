<script lang="ts">
  import Icon from '@iconify/svelte'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import * as Select from '$lib/components/ui/select'
  import Switch from '$lib/components/ui/switch/Switch.svelte'
  import {
    securityOptions,
    syncIntervalOptions,
  } from '$lib/config/providers'
  // @ts-ignore - wailsjs path
  import { account, certificate } from '../../../../../wailsjs/go/models'
  // @ts-ignore - wailsjs path
  import { GetAccountFoldersForMapping, GetAutoDetectedFolders, GetTrustedCertificates, RemoveTrustedCertificate, GetFolders, SubscribeFolder, UnsubscribeFolder, SubscribeAllFolders } from '../../../../../wailsjs/go/app/App'
  // @ts-ignore - wailsjs path
  import { folder } from '../../../../../wailsjs/go/models'
  import { Button } from '$lib/components/ui/button'
  import { _ } from '$lib/i18n'
  import ConfirmDialog from '$lib/components/ui/confirm-dialog/ConfirmDialog.svelte'

  interface Props {
    /** The account being edited */
    editAccount: account.Account
    /** Bound form values */
    imapHost: string
    imapPort: number
    imapSecurity: string
    smtpHost: string
    smtpPort: number
    smtpSecurity: string
    syncInterval: string
    readReceiptRequestPolicy: string
    /** Folder mappings */
    sentFolderPath: string
    draftsFolderPath: string
    trashFolderPath: string
    spamFolderPath: string
    archiveFolderPath: string
    allMailFolderPath: string
    starredFolderPath: string
    /** Validation errors */
    errors: Record<string, string>
    /** Callbacks */
    onImapHostChange: (value: string) => void
    onImapPortChange: (value: number) => void
    onImapSecurityChange: (value: string) => void
    onSmtpHostChange: (value: string) => void
    onSmtpPortChange: (value: number) => void
    onSmtpSecurityChange: (value: string) => void
    onSyncIntervalChange: (value: string) => void
    onReadReceiptPolicyChange: (value: string) => void
    onFolderMappingChange: (type: string, value: string) => void
    syncAllFolders: boolean
    onSyncAllFoldersChange: (value: boolean) => void
    syncFoldersEnabled: boolean
    onSyncFoldersEnabledChange: (value: boolean) => void
  }

  let {
    editAccount,
    imapHost = $bindable(),
    imapPort = $bindable(),
    imapSecurity = $bindable(),
    smtpHost = $bindable(),
    smtpPort = $bindable(),
    smtpSecurity = $bindable(),
    syncInterval = $bindable(),
    readReceiptRequestPolicy = $bindable(),
    sentFolderPath = $bindable(),
    draftsFolderPath = $bindable(),
    trashFolderPath = $bindable(),
    spamFolderPath = $bindable(),
    archiveFolderPath = $bindable(),
    allMailFolderPath = $bindable(),
    starredFolderPath = $bindable(),
    errors,
    onImapHostChange,
    onImapPortChange,
    onImapSecurityChange,
    onSmtpHostChange,
    onSmtpPortChange,
    onSmtpSecurityChange,
    onSyncIntervalChange,
    onReadReceiptPolicyChange,
    onFolderMappingChange,
    syncAllFolders = $bindable(),
    onSyncAllFoldersChange,
    syncFoldersEnabled = $bindable(),
    onSyncFoldersEnabledChange,
  }: Props = $props()

  // Folder mapping state
  let showFolderMapping = $state(false)
  let loadingFolders = $state(false)
  let availableFolders = $state<any[]>([])
  let autoDetectedFolders = $state<Record<string, string>>({})

  // Folder sync subscription state
  let showFolderSync = $state(false)
  let loadingSyncFolders = $state(false)
  let syncFolders = $state<folder.Folder[]>([])

  const coreFolderTypes = ['inbox', 'drafts', 'sent']

  async function loadSyncFolders() {
    if (syncFolders.length > 0) return
    loadingSyncFolders = true
    try {
      syncFolders = await GetFolders(editAccount.id)
    } catch (err) {
      console.error('Failed to load folders for sync:', err)
    } finally {
      loadingSyncFolders = false
    }
  }

  function handleFolderSyncToggle() {
    showFolderSync = !showFolderSync
    if (showFolderSync) {
      loadSyncFolders()
    }
  }

  async function handleSyncAllToggle(checked: boolean) {
    syncAllFolders = checked
    onSyncAllFoldersChange(checked)
    if (!checked) return
    try {
      await SubscribeAllFolders(editAccount.id)
      // Refresh folder list to show updated subscription state
      syncFolders = await GetFolders(editAccount.id)
    } catch (err) {
      console.error('Failed to subscribe to all folders:', err)
    }
  }

  async function handleFolderSubscriptionToggle(f: folder.Folder, subscribed: boolean) {
    const action = subscribed ? SubscribeFolder : UnsubscribeFolder
    try {
      await action(editAccount.id, f.id)
      // Update local state
      syncFolders = syncFolders.map(sf =>
        sf.id === f.id ? { ...sf, subscribed } as folder.Folder : sf
      )
    } catch (err) {
      console.error('Failed to update folder subscription:', err)
    }
  }

  // Trusted certificates state
  let showTrustedCerts = $state(false)
  let loadingCerts = $state(false)
  let trustedCerts = $state<certificate.CertificateInfo[]>([])
  let confirmRemoveFingerprint = $state<string | null>(null)
  let showRemoveConfirm = $state(false)

  // Read receipt request policy options
  const readReceiptRequestOptions = [
    { value: 'never', labelKey: 'account.neverRequest' },
    { value: 'ask', labelKey: 'account.askEachTime' },
    { value: 'always', labelKey: 'account.alwaysRequest' },
  ]

  // Helper functions
  function getSecurityLabel(value: string): string {
    return securityOptions.find(opt => opt.value === value)?.label || value
  }

  function getSyncIntervalLabel(value: string): string {
    const numValue = Number(value)
    return syncIntervalOptions.find(opt => opt.value === numValue)?.label || `${value} min`
  }

  function getReadReceiptLabel(value: string): string {
    switch (value) {
      case 'never': return $_('account.neverRequest')
      case 'ask': return $_('account.askEachTime')
      case 'always': return $_('account.alwaysRequest')
      default: return value
    }
  }

  // Load folders for mapping UI
  async function loadFoldersForMapping() {
    if (availableFolders.length > 0) return

    loadingFolders = true
    try {
      availableFolders = await GetAccountFoldersForMapping(editAccount.id)
      autoDetectedFolders = await GetAutoDetectedFolders(editAccount.id)
    } catch (err) {
      console.error('Failed to load folders for mapping:', err)
    } finally {
      loadingFolders = false
    }
  }

  function handleFolderMappingToggle() {
    showFolderMapping = !showFolderMapping
    if (showFolderMapping) {
      loadFoldersForMapping()
    }
  }

  function handleTrustedCertsToggle() {
    showTrustedCerts = !showTrustedCerts
    if (showTrustedCerts) {
      loadTrustedCerts()
    }
  }

  async function loadTrustedCerts() {
    loadingCerts = true
    try {
      const hosts = [imapHost, smtpHost].filter(h => h)
      const result = await GetTrustedCertificates(hosts)
      trustedCerts = result || []
    } catch (err) {
      console.error('Failed to load trusted certificates:', err)
      trustedCerts = []
    } finally {
      loadingCerts = false
    }
  }

  function handleRemoveCert(fingerprint: string) {
    confirmRemoveFingerprint = fingerprint
    showRemoveConfirm = true
  }

  async function confirmRemoveCert() {
    if (!confirmRemoveFingerprint) return
    try {
      await RemoveTrustedCertificate(confirmRemoveFingerprint)
      trustedCerts = trustedCerts.filter(c => c.fingerprint !== confirmRemoveFingerprint)
    } catch (err) {
      console.error('Failed to remove certificate:', err)
    }
    showRemoveConfirm = false
    confirmRemoveFingerprint = null
  }

  function formatFingerprint(fp: string): string {
    if (!fp) return ''
    const parts: string[] = []
    for (let i = 0; i < fp.length && i < 16; i += 2) {
      parts.push(fp.substring(i, i + 2).toUpperCase())
    }
    return parts.join(':') + '...'
  }

  function formatCertDate(iso: string): string {
    if (!iso) return 'N/A'
    try {
      return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
    } catch {
      return iso
    }
  }

  // Folder mapping types configuration
  // get() returns saved mapping or falls back to auto-detected folder
  const folderMappingTypes = [
    { key: 'sent', labelKey: 'account.folderSent', get: () => sentFolderPath || autoDetectedFolders['sent'] || '', set: (v: string) => { sentFolderPath = v; onFolderMappingChange('sent', v) }},
    { key: 'drafts', labelKey: 'account.folderDrafts', get: () => draftsFolderPath || autoDetectedFolders['drafts'] || '', set: (v: string) => { draftsFolderPath = v; onFolderMappingChange('drafts', v) }},
    { key: 'trash', labelKey: 'account.folderTrash', get: () => trashFolderPath || autoDetectedFolders['trash'] || '', set: (v: string) => { trashFolderPath = v; onFolderMappingChange('trash', v) }},
    { key: 'spam', labelKey: 'account.folderSpam', get: () => spamFolderPath || autoDetectedFolders['spam'] || '', set: (v: string) => { spamFolderPath = v; onFolderMappingChange('spam', v) }},
    { key: 'archive', labelKey: 'account.folderArchive', get: () => archiveFolderPath || autoDetectedFolders['archive'] || '', set: (v: string) => { archiveFolderPath = v; onFolderMappingChange('archive', v) }},
    { key: 'all', labelKey: 'account.folderAllMail', get: () => allMailFolderPath || autoDetectedFolders['all'] || '', set: (v: string) => { allMailFolderPath = v; onFolderMappingChange('all', v) }},
    { key: 'starred', labelKey: 'account.folderStarred', get: () => starredFolderPath || autoDetectedFolders['starred'] || '', set: (v: string) => { starredFolderPath = v; onFolderMappingChange('starred', v) }},
  ]
</script>

<div class="space-y-6">
  <!-- Incoming Mail (IMAP) -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:email-receive-outline" class="w-4 h-4" />
      {$_('account.incomingMail')}
    </h3>

    <div class="grid grid-cols-2 gap-3">
      <div class="space-y-2">
        <Label for="imapHost">{$_('account.server')}</Label>
        <Input
          id="imapHost"
          type="text"
          placeholder="imap.example.com"
          bind:value={imapHost}
          oninput={(e) => onImapHostChange((e.target as HTMLInputElement).value)}
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
            oninput={(e) => onImapPortChange(Number((e.target as HTMLInputElement).value))}
            class={errors.imapPort ? 'border-destructive' : ''}
          />
        </div>
        <div class="space-y-2">
          <Label>{$_('account.security')}</Label>
          <Select.Root
            value={imapSecurity}
            onValueChange={(v) => { imapSecurity = v; onImapSecurityChange(v) }}
          >
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

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Outgoing Mail (SMTP) -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:email-send-outline" class="w-4 h-4" />
      {$_('account.outgoingMail')}
    </h3>

    <div class="grid grid-cols-2 gap-3">
      <div class="space-y-2">
        <Label for="smtpHost">{$_('account.server')}</Label>
        <Input
          id="smtpHost"
          type="text"
          placeholder="smtp.example.com"
          bind:value={smtpHost}
          oninput={(e) => onSmtpHostChange((e.target as HTMLInputElement).value)}
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
            oninput={(e) => onSmtpPortChange(Number((e.target as HTMLInputElement).value))}
            class={errors.smtpPort ? 'border-destructive' : ''}
          />
        </div>
        <div class="space-y-2">
          <Label>{$_('account.security')}</Label>
          <Select.Root
            value={smtpSecurity}
            onValueChange={(v) => { smtpSecurity = v; onSmtpSecurityChange(v) }}
          >
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

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Check for New Mail -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:refresh" class="w-4 h-4" />
      {$_('account.syncOptions')}
    </h3>

    <div class="space-y-2">
      <Label>{$_('account.checkNewMail')}</Label>
      <Select.Root 
        value={syncInterval} 
        onValueChange={(v) => { syncInterval = v; onSyncIntervalChange(v) }}
      >
        <Select.Trigger>
          <Select.Value placeholder="Select">
            {getSyncIntervalLabel(syncInterval)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each syncIntervalOptions as opt (opt.value)}
            <Select.Item value={String(opt.value)} label={opt.label} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_('account.checkNewMailHelp')}
      </p>
    </div>

    <div class="space-y-2">
      <Label>{$_('account.requestReadReceipts')}</Label>
      <Select.Root 
        value={readReceiptRequestPolicy} 
        onValueChange={(v) => { readReceiptRequestPolicy = v; onReadReceiptPolicyChange(v) }}
      >
        <Select.Trigger>
          <Select.Value placeholder="Select">
            {getReadReceiptLabel(readReceiptRequestPolicy)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each readReceiptRequestOptions as opt (opt.value)}
            <Select.Item value={opt.value} label={$_(opt.labelKey)} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_('account.requestReadReceiptsHelp')}
      </p>
    </div>
  </div>

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Folder Mapping -->
  <div class="space-y-2">
    <button
      type="button"
      class="flex items-center gap-2 text-sm font-medium hover:text-primary transition-colors"
      onclick={handleFolderMappingToggle}
    >
      <Icon
        icon={showFolderMapping ? 'mdi:chevron-down' : 'mdi:chevron-right'}
        class="w-4 h-4"
      />
      <Icon icon="mdi:folder-cog-outline" class="w-4 h-4" />
      {$_('account.folderMapping')}
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
            {#each folderMappingTypes as mapping (mapping.key)}
              <div class="grid grid-cols-[100px_1fr] items-center gap-2">
                <Label class="text-sm">{$_(mapping.labelKey)}:</Label>
                <Select.Root value={mapping.get()} onValueChange={mapping.set}>
                  <Select.Trigger class="h-9">
                    <Select.Value placeholder={$_('account.none')}>
                      {(mapping.get() || $_('account.none')) + (autoDetectedFolders[mapping.key] === mapping.get() ? ' ' + $_('account.detected') : '')}
                    </Select.Value>
                  </Select.Trigger>
                  <Select.Content>
                    <Select.Item value="" label={$_('account.none')} />
                    {#each availableFolders as f (f.path)}
                      <Select.Item
                        value={f.path}
                        label={f.path + (autoDetectedFolders[mapping.key] === f.path ? ' ' + $_('account.detected') : '')}
                      />
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <!-- Folder Sync Subscriptions -->
  <div class="space-y-2">
    <button
      type="button"
      class="flex items-center gap-2 text-sm font-medium hover:text-primary transition-colors"
      onclick={handleFolderSyncToggle}
    >
      <Icon
        icon={showFolderSync ? 'mdi:chevron-down' : 'mdi:chevron-right'}
        class="w-4 h-4"
      />
      <Icon icon="mdi:folder-sync-outline" class="w-4 h-4" />
      {$_('account.folderSync')}
    </button>

    {#if showFolderSync}
      <div class="space-y-4 pl-6 pt-2 border-l border-border ml-2">
        <!-- Manage folder sync toggle (opt-in) -->
        <div class="space-y-1">
          <div class="flex items-center gap-3">
            <Label>{$_('account.manageFolderSync')}</Label>
            <Switch
              bind:checked={syncFoldersEnabled}
              onCheckedChange={(v) => { syncFoldersEnabled = v; onSyncFoldersEnabledChange(v); if (v) loadSyncFolders() }}
            />
          </div>
          <p class="text-xs text-muted-foreground">
            {$_('account.manageFolderSyncHelp')}
          </p>
        </div>

        {#if syncFoldersEnabled}
          <!-- Sync All Folders toggle -->
          <div class="flex items-center gap-3">
            <Label>{$_('account.syncAllFolders')}</Label>
            <Switch
              bind:checked={syncAllFolders}
              onCheckedChange={handleSyncAllToggle}
            />
          </div>

          <!-- Folder checklist (when sync all is off) -->
          {#if !syncAllFolders}
            {#if loadingSyncFolders}
              <div class="flex items-center gap-2 text-sm text-muted-foreground">
                <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
                {$_('account.loadingFolders')}
              </div>
            {:else}
              <div class="space-y-1 max-h-48 overflow-y-auto">
                {#each syncFolders as f (f.id)}
                  {@const isCore = coreFolderTypes.includes(f.type)}
                  <label class="flex items-center gap-2 cursor-pointer py-0.5 {isCore ? 'opacity-60' : ''}">
                    <input
                      type="checkbox"
                      checked={isCore || f.subscribed}
                      disabled={isCore}
                      onchange={(e) => handleFolderSubscriptionToggle(f, (e.target as HTMLInputElement).checked)}
                      class="rounded border-border"
                    />
                    <span class="text-sm truncate">{f.path}</span>
                    {#if isCore}
                      <span class="text-xs text-muted-foreground">({$_('account.alwaysSynced')})</span>
                    {/if}
                  </label>
                {/each}
              </div>
            {/if}
          {/if}
        {/if}
      </div>
    {/if}
  </div>

  <!-- Trusted Certificates -->
  <div class="space-y-2">
    <button
      type="button"
      class="flex items-center gap-2 text-sm font-medium hover:text-primary transition-colors"
      onclick={handleTrustedCertsToggle}
    >
      <Icon
        icon={showTrustedCerts ? 'mdi:chevron-down' : 'mdi:chevron-right'}
        class="w-4 h-4"
      />
      <Icon icon="mdi:shield-lock-outline" class="w-4 h-4" />
      {$_('account.trustedCertificates')}
    </button>

    {#if showTrustedCerts}
      <div class="space-y-3 pl-6 pt-2 border-l border-border ml-2">
        <p class="text-xs text-muted-foreground">
          {$_('account.trustedCertsHelp')}
        </p>

        {#if loadingCerts}
          <div class="flex items-center gap-2 text-sm text-muted-foreground">
            <Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
            {$_('account.loadingCerts')}
          </div>
        {:else if trustedCerts.length === 0}
          <p class="text-sm text-muted-foreground">
            {$_('account.noTrustedCerts')}
          </p>
        {:else}
          <div class="space-y-3">
            {#each trustedCerts as cert (cert.fingerprint)}
              <div class="flex items-start justify-between gap-3 rounded-lg border bg-muted/30 p-3">
                <div class="space-y-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <Icon icon="mdi:shield-check-outline" class="w-4 h-4 text-muted-foreground shrink-0" />
                    <span class="text-sm font-medium truncate">{cert.subject}</span>
                  </div>
                  <div class="text-xs text-muted-foreground space-y-0.5 pl-6">
                    <p>{$_('account.certFingerprint')} <span class="font-mono">{formatFingerprint(cert.fingerprint)}</span></p>
                    <p>{$_('account.certExpires')} {formatCertDate(cert.notAfter)}</p>
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  class="shrink-0 text-destructive hover:text-destructive hover:bg-destructive/10"
                  onclick={() => handleRemoveCert(cert.fingerprint)}
                >
                  {$_('common.remove')}
                </Button>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>

<ConfirmDialog
  bind:open={showRemoveConfirm}
  title={$_('account.removeTrustedCert')}
  description={$_('account.removeTrustedCertDescription')}
  confirmLabel={$_('common.remove')}
  onConfirm={confirmRemoveCert}
/>
