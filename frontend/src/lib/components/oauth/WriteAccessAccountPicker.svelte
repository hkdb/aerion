<script lang="ts">
  // WriteAccessAccountPicker — shown when the user clicks "Enable write
  // access" on a Google or Microsoft contacts source. Lists existing
  // authenticated identities (mail accounts + standalone contact sources)
  // matching the source's provider; the user picks one and the contacts
  // source's write grant attaches to that identity.
  //
  // No "Add another account" option. Aerion's design forbids adding new
  // accounts from inside the contacts extension — all accounts come from
  // core setup paths (Mail account add OR contacts source add). If no
  // matching identity exists, the dialog shows an empty-state message
  // pointing the user to those paths.

  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { ListAuthContextsForProvider, Contacts_EnableWriteAccess } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { app } from '$wailsjs/go/models'

  interface Props {
    open: boolean
    provider: 'google' | 'microsoft'
    sourceID: string
    sourceName: string
    onCompleted?: () => void
  }

  let { open = $bindable(false), provider, sourceID, sourceName, onCompleted }: Props = $props()

  let contexts = $state<app.AuthContextInfo[]>([])
  let loading = $state(false)
  let selected = $state<string>('') // key: `${kind}:${identifier}`
  let granting = $state(false)

  function ctxKey(c: app.AuthContextInfo): string {
    return `${c.kind}:${c.identifier}`
  }

  // Re-fetch every time the dialog opens. Cheap (single Wails call returning
  // a small array). Keeps the list fresh if the user added/removed accounts
  // since the dialog was last opened.
  $effect(() => {
    if (!open) return
    void load()
  })

  async function load() {
    loading = true
    try {
      const result = await ListAuthContextsForProvider(provider)
      contexts = result || []
      selected = contexts.length > 0 ? ctxKey(contexts[0]) : ''
    } catch (err) {
      console.error('Failed to load auth contexts:', err)
      toasts.error(
        $_('oauth.writeAccessPicker.loadFailed', {
          values: { message: (err as Error)?.message ?? String(err) },
        }),
      )
      contexts = []
      selected = ''
    } finally {
      loading = false
    }
  }

  const providerLabel = $derived.by(() => {
    switch (provider) {
      case 'google':
        return $_('oauth.writeAccessPicker.providerGoogle')
      case 'microsoft':
        return $_('oauth.writeAccessPicker.providerMicrosoft')
    }
    return provider
  })

  function close() {
    if (granting) return
    open = false
  }

  async function confirm() {
    const picked = contexts.find((c) => ctxKey(c) === selected)
    if (!picked) return
    granting = true
    try {
      await Contacts_EnableWriteAccess(sourceID, picked.kind, picked.identifier, picked.email)
      toasts.success(
        $_('oauth.writeAccessPicker.successToast', { values: { name: sourceName } }),
      )
      open = false
      onCompleted?.()
    } catch (err) {
      console.error('Failed to enable write access:', err)
      toasts.error(
        $_('oauth.writeAccessPicker.failureToast', {
          values: { message: (err as Error)?.message ?? String(err) },
        }),
      )
    } finally {
      granting = false
    }
  }
</script>

<Dialog.Root bind:open onOpenChange={(v: boolean) => { if (!v) close() }}>
  <Dialog.Content class="max-w-md">
    <Dialog.Header>
      <Dialog.Title>{$_('oauth.writeAccessPicker.title')}</Dialog.Title>
      <Dialog.Description>
        {$_('oauth.writeAccessPicker.description', { values: { provider: providerLabel, sourceName } })}
      </Dialog.Description>
    </Dialog.Header>

    <div class="mt-2 space-y-2 max-h-[40vh] overflow-y-auto pr-1">
      {#if loading}
        <div class="flex items-center justify-center py-6">
          <Icon icon="mdi:loading" class="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
      {:else if contexts.length === 0}
        <div class="flex flex-col items-center gap-2 py-6 text-center">
          <Icon icon="mdi:account-alert-outline" class="w-8 h-8 text-muted-foreground" />
          <p class="text-sm text-foreground">
            {$_('oauth.writeAccessPicker.emptyHeading', { values: { provider: providerLabel } })}
          </p>
          <p class="text-xs text-muted-foreground">
            {$_('oauth.writeAccessPicker.emptyHint', { values: { provider: providerLabel } })}
          </p>
        </div>
      {:else}
        {#each contexts as ctx (ctxKey(ctx))}
          <label
            class="flex items-center gap-3 p-3 rounded-md border border-border cursor-pointer hover:bg-muted/40 transition-colors {selected === ctxKey(ctx) ? 'bg-primary/5 border-primary/40' : ''}"
          >
            <input
              type="radio"
              name="auth-context"
              value={ctxKey(ctx)}
              bind:group={selected}
              class="accent-primary"
              disabled={granting}
            />
            <div class="flex-1 min-w-0">
              <div class="text-sm font-medium text-foreground truncate">{ctx.email}</div>
              <div class="text-xs text-muted-foreground">{ctx.label}</div>
            </div>
          </label>
        {/each}
      {/if}
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={close} disabled={granting}>
        {$_('oauth.writeAccessPicker.cancel')}
      </Button>
      {#if contexts.length > 0}
        <Button onclick={confirm} disabled={granting || !selected}>
          {#if granting}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
          {/if}
          {$_('oauth.writeAccessPicker.continue')}
        </Button>
      {/if}
    </div>
  </Dialog.Content>
</Dialog.Root>
