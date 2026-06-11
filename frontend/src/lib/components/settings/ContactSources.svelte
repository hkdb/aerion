<script lang="ts">
  import Icon from '@iconify/svelte'
  import { onMount } from 'svelte'
  import { Button } from '$lib/components/ui/button'
  import { ConfirmDialog } from '$lib/components/ui/confirm-dialog'
  import { contactSourcesStore } from '$lib/stores/contactSources.svelte'
  import { addToast } from '$lib/stores/toast'
  import ContactSourceDialog from './ContactSourceDialog.svelte'
  import { formatDistanceToNow } from 'date-fns'
  import { _ } from '$lib/i18n'
  import { getCurrentDateFnsLocale } from '$lib/stores/settings.svelte'
  // @ts-ignore - wailsjs path
  import type { carddav } from '../../../../wailsjs/go/models'

  // Dialog state
  let showAddDialog = $state(false)
  let editingSource = $state<carddav.Source | null>(null)
  let syncingSourceId = $state<string | null>(null)
  let forceSyncingSourceId = $state<string | null>(null)

  // Delete confirmation state
  let showDeleteConfirm = $state(false)
  let deletingSource = $state<carddav.Source | null>(null)
  let isDeleting = $state(false)

  // Force re-sync confirmation state
  let showForceSyncConfirm = $state(false)
  let forceSyncTarget = $state<carddav.Source | null>(null)

  onMount(() => {
    contactSourcesStore.load()
  })

  function formatLastSync(source: carddav.Source): string {
    if (!source.last_synced_at) return $_('contactSource.neverSynced')
    try {
      return $_('contactSource.syncedAgo', { values: { time: formatDistanceToNow(new Date(source.last_synced_at), { addSuffix: true, locale: getCurrentDateFnsLocale() }) } })
    } catch {
      return $_('contactSource.neverSynced')
    }
  }

  async function handleSync(sourceId: string) {
    syncingSourceId = sourceId
    try {
      await contactSourcesStore.syncSource(sourceId)
      addToast({ type: 'success', message: $_('toast.contactSourceSynced') })
    } catch (err) {
      console.error('Contact source sync failed:', err)
      addToast({ type: 'error', message: $_('toast.syncFailed') })
    } finally {
      syncingSourceId = null
    }
  }

  function openForceSyncConfirm(source: carddav.Source) {
    forceSyncTarget = source
    showForceSyncConfirm = true
  }

  async function confirmForceSync() {
    if (!forceSyncTarget) return
    const id = forceSyncTarget.id
    showForceSyncConfirm = false
    forceSyncTarget = null
    forceSyncingSourceId = id
    try {
      await contactSourcesStore.forceSyncSource(id)
      addToast({ type: 'success', message: $_('toast.contactSourceForceSynced') })
    } catch (err) {
      console.error('Contact source force-sync failed:', err)
      addToast({ type: 'error', message: $_('toast.contactSourceForceSyncFailed') })
    } finally {
      forceSyncingSourceId = null
    }
  }

  function cancelForceSync() {
    showForceSyncConfirm = false
    forceSyncTarget = null
  }

  function handleDelete(source: carddav.Source) {
    deletingSource = source
    showDeleteConfirm = true
  }

  async function confirmDelete() {
    if (!deletingSource) return
    isDeleting = true
    try {
      await contactSourcesStore.deleteSource(deletingSource.id)
      addToast({ type: 'success', message: $_('toast.contactSourceDeleted') })
      showDeleteConfirm = false
      deletingSource = null
    } catch (err) {
      console.error('Failed to delete contact source:', err)
      addToast({ type: 'error', message: $_('toast.failedToDeleteContactSource') })
    } finally {
      isDeleting = false
    }
  }

  function cancelDelete() {
    showDeleteConfirm = false
    deletingSource = null
  }

  function openEdit(source: carddav.Source) {
    editingSource = source
    showAddDialog = true
  }

  function openAdd() {
    editingSource = null
    showAddDialog = true
  }

  function handleDialogClose() {
    showAddDialog = false
    editingSource = null
    contactSourcesStore.refresh()
  }
</script>

<div class="space-y-4">
  <h3 class="text-sm font-medium flex items-center gap-2">
    <Icon icon="mdi:contacts-outline" class="w-4 h-4" />
    {$_('contactSource.title')}
  </h3>

  {#if contactSourcesStore.loading}
    <div class="flex items-center justify-center py-4">
      <Icon icon="mdi:loading" class="w-5 h-5 animate-spin text-muted-foreground" />
    </div>
  {:else if contactSourcesStore.sources.length === 0}
    <div class="text-sm text-muted-foreground py-4 text-center">
      <p class="mb-3">{$_('contactSource.noSources')}</p>
      <Button size="sm" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_('contactSource.addSource')}
      </Button>
    </div>
  {:else}
    <div class="space-y-2">
      {#each contactSourcesStore.sources as source (source.id)}
        <div class="p-3 border border-border rounded-lg space-y-2 {source.last_error ? 'border-destructive/50 bg-destructive/5' : ''}">
          <!-- Source header -->
          <div class="flex items-start justify-between">
            <div class="flex items-center gap-2">
              <Icon
                icon={source.type === 'google' ? 'mdi:google' : source.type === 'microsoft' ? 'mdi:microsoft' : 'mdi:card-account-details'}
                class="w-5 h-5 {source.enabled ? 'text-primary' : 'text-muted-foreground'}"
              />
              <div>
                <div class="font-medium text-sm">{source.name}</div>
                <div class="text-xs text-muted-foreground flex items-center gap-1.5">
                  <span class="capitalize">{source.type}</span>
                  {#if source.account_id}
                    <span class="text-muted-foreground/50">·</span>
                    <span class="text-muted-foreground/80">{$_('contactSource.linked')}</span>
                  {/if}
                </div>
              </div>
            </div>
            <div class="text-xs text-muted-foreground">
              {formatLastSync(source)}
            </div>
          </div>

          <!-- Error display -->
          {#if source.last_error}
            <div class="flex items-start gap-2 p-2 bg-destructive/10 rounded text-sm min-w-0">
              <Icon icon="mdi:alert-circle" class="w-4 h-4 text-destructive shrink-0 mt-0.5" />
              <div class="flex-1 min-w-0">
                <div class="text-destructive font-medium">{$_('contactSource.syncFailed')}</div>
                <div class="text-xs text-muted-foreground break-all max-h-24 overflow-y-auto">{source.last_error}</div>
              </div>
            </div>
          {/if}

          <!-- Actions -->
          <div class="flex items-center gap-2 pt-1">
            <Button
              size="sm"
              variant="ghost"
              onclick={() => handleSync(source.id)}
              disabled={syncingSourceId === source.id || forceSyncingSourceId === source.id}
            >
              {#if syncingSourceId === source.id}
                <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
              {:else}
                <Icon icon="mdi:sync" class="w-4 h-4 mr-1" />
              {/if}
              {source.last_error ? $_('common.retry') : $_('common.sync')}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onclick={() => openForceSyncConfirm(source)}
              disabled={syncingSourceId === source.id || forceSyncingSourceId === source.id}
              title={$_('contactSource.forceSync')}
            >
              {#if forceSyncingSourceId === source.id}
                <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
              {:else}
                <Icon icon="mdi:refresh-auto" class="w-4 h-4 mr-1" />
              {/if}
              {$_('contactSource.forceSync')}
            </Button>
            <Button size="sm" variant="ghost" onclick={() => openEdit(source)}>
              <Icon icon="mdi:pencil" class="w-4 h-4 mr-1" />
              {$_('common.edit')}
            </Button>
            <Button size="sm" variant="ghost" class="text-destructive hover:text-destructive" onclick={() => handleDelete(source)}>
              <Icon icon="mdi:delete" class="w-4 h-4 mr-1" />
              {$_('common.delete')}
            </Button>
          </div>
        </div>
      {/each}

      <!-- Add button -->
      <Button size="sm" variant="outline" class="w-full" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_('contactSource.addSource')}
      </Button>
    </div>
  {/if}
</div>

<!-- Add/Edit Dialog -->
<ContactSourceDialog
  bind:open={showAddDialog}
  editSource={editingSource}
  onClose={handleDialogClose}
/>

<!-- Delete Confirmation Dialog -->
<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_('contactSource.deleteTitle')}
  description={$_('contactSource.deleteConfirmName', { values: { name: deletingSource?.name || '' } })}
  confirmLabel={$_('common.delete')}
  cancelLabel={$_('common.cancel')}
  variant="destructive"
  loading={isDeleting}
  onConfirm={confirmDelete}
  onCancel={cancelDelete}
/>

<!-- Force Re-sync Confirmation Dialog -->
<ConfirmDialog
  bind:open={showForceSyncConfirm}
  title={$_('contactSource.forceSyncConfirmTitle')}
  description={$_('contactSource.forceSyncConfirmDescription', { values: { name: forceSyncTarget?.name || '' } })}
  confirmLabel={$_('contactSource.forceSync')}
  cancelLabel={$_('common.cancel')}
  onConfirm={confirmForceSync}
  onCancel={cancelForceSync}
/>
