<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { addToast } from '$lib/stores/toast'
  import { refreshExtensionRegistry, openExtensionSettings } from '$lib/stores/extensionRegistry.svelte'
  import { _ } from '$lib/i18n'
  // @ts-ignore - wailsjs bindings
  import { ListExtensions, SetExtensionEnabled } from '../../../../wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { app } from '../../../../wailsjs/go/models'

  let extensions = $state<app.ExtensionInfo[]>([])
  let loading = $state(true)
  let togglingId = $state<string | null>(null)

  onMount(async () => {
    await load()
  })

  async function load() {
    loading = true
    try {
      extensions = await ListExtensions() || []
    } catch (err) {
      console.error('Failed to load extensions:', err)
      addToast({ type: 'error', message: 'Failed to load extensions' })
    } finally {
      loading = false
    }
  }

  async function toggle(ext: app.ExtensionInfo) {
    togglingId = ext.id
    const next = !ext.enabled
    try {
      await SetExtensionEnabled(ext.id, next)
      // Refresh the registry so the rail / hooks update immediately.
      await refreshExtensionRegistry()
      await load()
    } catch (err) {
      console.error('Failed to toggle extension:', err)
      addToast({ type: 'error', message: `Failed to ${next ? 'enable' : 'disable'} ${ext.name}` })
    } finally {
      togglingId = null
    }
  }
</script>

<div class="space-y-6">
  <header>
    <h2 class="text-lg font-semibold">{$_('settings.extensionsCoreHeading')}</h2>
    <p class="text-sm text-muted-foreground mt-1">{$_('settings.extensionsCoreDescription')}</p>
  </header>

  {#if loading}
    <div class="flex items-center justify-center py-6">
      <Icon icon="mdi:loading" class="w-5 h-5 animate-spin text-muted-foreground" />
    </div>
  {:else if extensions.length === 0}
    <p class="text-sm text-muted-foreground">{$_('settings.extensionsNone')}</p>
  {:else}
    <ul class="space-y-3">
      {#each extensions as ext (ext.id)}
        <li class="border border-border rounded-md p-4 bg-card">
          <div class="flex items-start justify-between gap-4">
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2">
                <h3 class="font-medium">{ext.name}</h3>
                <span class="text-xs text-muted-foreground">
                  {$_('settings.extensionsVersion')} {ext.version}
                </span>
                {#if ext.enabled}
                  <span class="text-xs px-2 py-0.5 rounded bg-primary/15 text-primary">
                    {$_('settings.extensionsEnabled')}
                  </span>
                {:else}
                  <span class="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                    {$_('settings.extensionsDisabled')}
                  </span>
                {/if}
              </div>
              <p class="text-sm text-muted-foreground mt-1">{ext.description}</p>
              {#if ext.capabilities && ext.capabilities.length > 0}
                <div class="mt-2 flex flex-wrap gap-1">
                  <span class="text-xs text-muted-foreground">{$_('settings.extensionsCapabilities')}:</span>
                  {#each ext.capabilities as cap (cap)}
                    <code class="text-xs px-1.5 py-0.5 rounded bg-muted">{cap}</code>
                  {/each}
                </div>
              {/if}
            </div>
            <div class="flex items-center gap-2 flex-shrink-0">
              {#if ext.enabled}
                <Button
                  variant="outline"
                  size="sm"
                  onclick={() => openExtensionSettings(ext.id)}
                  title="Edit extension settings"
                >
                  <Icon icon="mdi:cog" class="w-4 h-4 mr-1" />
                  Edit
                </Button>
              {/if}
              <Button
                variant={ext.enabled ? 'outline' : 'default'}
                size="sm"
                disabled={togglingId === ext.id}
                onclick={() => toggle(ext)}
              >
                {#if togglingId === ext.id}
                  <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
                {/if}
                {ext.enabled ? $_('settings.extensionsDisable') : $_('settings.extensionsEnable')}
              </Button>
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>
