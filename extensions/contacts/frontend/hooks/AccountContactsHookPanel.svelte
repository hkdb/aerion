<script lang="ts">
  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { addToast } from '$lib/stores/toast'
  import { refreshExtensionRegistry } from '$lib/stores/extensionRegistry.svelte'
  // @ts-ignore - wailsjs bindings
  import { SetExtensionEnabled } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { v1 } from '$wailsjs/go/models'

  interface Props {
    hook: v1.AccountSetupHookRequest
    accountId: string
    accountName: string
    /** Called when user has settled the panel (set up or skipped). */
    onResolved: () => void
  }

  const { hook, accountId, accountName, onResolved }: Props = $props()

  let busy = $state(false)
  let done = $state(false)
  let error = $state<string | null>(null)

  async function setUp() {
    busy = true
    error = null
    try {
      // Link the account as a read-only CardDAV contact source. 60-minute
      // sync interval matches the default for new sources; a future settings
      // affordance may let the user override it.
      await contactSourcesStore.linkAccount(accountId, accountName, 60)

      // Enable the Contacts extension so the rail surfaces it.
      await SetExtensionEnabled('contacts', true)
      await refreshExtensionRegistry()

      done = true
      addToast({
        type: 'success',
        message: $_('contacts.hook.successMessage', { values: { name: accountName } }),
      })
    } catch (err) {
      console.error('Failed to set up contacts for account:', err)
      error = (err as Error)?.message || String(err)
    } finally {
      busy = false
    }
  }

  function skip() {
    onResolved()
  }

  function close() {
    onResolved()
  }
</script>

<section class="p-4 border border-border rounded-md mb-3 bg-card text-card-foreground">
  <header class="flex items-start gap-3 mb-3">
    <Icon icon="mdi:account-multiple" width="24" height="24" />
    <div>
      <h3 class="m-0 mb-1 text-[15px] font-semibold text-foreground">{hook.buttonLabel}</h3>
      {#if hook.description}
        <p class="m-0 text-sm text-muted-foreground">{hook.description}</p>
      {/if}
    </div>
  </header>

  {#if error}
    <p class="text-sm text-destructive m-0 mb-2" role="alert">{error}</p>
  {/if}

  {#if !done}
    <div class="flex justify-end gap-2">
      <Button variant="ghost" onclick={skip} disabled={busy}>{$_('contacts.hook.skip')}</Button>
      <Button onclick={setUp} disabled={busy}>
        {#if busy}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
        {/if}
        {$_('contacts.hook.setUp')}
      </Button>
    </div>
  {:else}
    <div class="flex items-center gap-2 text-primary">
      <Icon icon="mdi:check-circle" width="20" height="20" />
      <span class="flex-1">{$_('contacts.hook.completeMessage')}</span>
      <Button variant="ghost" onclick={close}>{$_('contacts.hook.done')}</Button>
    </div>
  {/if}
</section>
