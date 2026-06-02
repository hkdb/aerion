<script lang="ts">
  // OAuthCredsSlotEditor — single-slot OAuth credential editor primitive.
  // Used by Aerion core's "OAuth Credentials (advanced)" section (Settings →
  // Accounts) AND by each extension's settings dialog. Composed from existing
  // ui/input (type="password" for secret), ui/button, and ui/select.
  //
  // Props:
  //   configID            — the slot identifier (e.g., "google-mail",
  //                         "google-contacts")
  //   label               — display name (e.g., "Google Mail")
  //   secretRequired      — whether the slot needs a client_secret (true for
  //                         Google; false for Microsoft / PKCE)
  //   onChanged           — fired after a successful save/clear so the parent
  //                         can refresh its own state if needed.
  //
  // UX:
  //   Single dropdown picks the source of credentials for this slot:
  //     - "Custom"          — user-pasted client_id/secret. Default. Selecting
  //                           reveals the edit form.
  //     - "Aerion - <prov>" — Aerion-shipped build-time client_id/secret.
  //                           Only appears when shipped creds for this slot's
  //                           provider exist in the binary. Selecting hides
  //                           the edit form.
  //
  //   Switching Custom → Aerion-X calls ClearOAuthCreds, so the resolver
  //   falls through to the shipped provider chain naturally.
  //   Switching Aerion-X → Custom shows an empty form; user pastes + Saves
  //   → SetOAuthCreds writes the user override.

  import { onMount } from 'svelte'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import * as Select from '$lib/components/ui/select'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { GetOAuthCredsStatus, SetOAuthCreds, ClearOAuthCreds } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { app } from '$wailsjs/go/models'

  interface Props {
    configID: string
    label: string
    secretRequired?: boolean
    onChanged?: () => void
  }

  const { configID, label, secretRequired = true, onChanged }: Props = $props()

  type SourceMode = 'custom' | 'aerion-shipped'

  let status = $state<app.OAuthCredsStatus | null>(null)
  let loading = $state(true)
  let mode = $state<SourceMode>('custom')
  let clientID = $state('')
  let clientSecret = $state('')
  let saving = $state(false)

  // Derive provider label from the slot's configID prefix. The shipped option
  // shows as "Aerion - Google" / "Aerion - Microsoft" so the user can tell
  // exactly which client identity they're picking, independent of the slot
  // they're configuring (which might be e.g. "google-contacts").
  const shippedOptionLabel = $derived.by(() => {
    if (configID.startsWith('google-')) return 'Aerion - Google'
    if (configID.startsWith('microsoft-')) return 'Aerion - Microsoft'
    return 'Aerion - Default'
  })

  // The Aerion-shipped option appears only when the binary actually carries
  // shipped creds for this slot. Same detection as before — hasShipped is
  // computed by GetOAuthCredsStatus by probing the provider chain with the
  // UserOverrideLookup hook disabled.
  const shippedOptionAvailable = $derived(status?.hasShipped === true)

  // Map the slot's current backend state to one of the two SourceMode values.
  // Guard-clause style to comply with the no-else rule.
  function deriveMode(s: app.OAuthCredsStatus | null): SourceMode {
    if (s?.hasUserOverride) return 'custom'
    if (s?.hasShipped) return 'aerion-shipped'
    return 'custom'
  }

  async function refresh() {
    loading = true
    try {
      status = await GetOAuthCredsStatus(configID)
      mode = deriveMode(status)
    } catch (err) {
      console.error('Failed to load OAuth creds status:', err)
      status = null
      mode = 'custom'
    } finally {
      loading = false
    }
  }

  onMount(refresh)

  // Dropdown change handler. Switching to Aerion-shipped clears the user
  // override so the resolver falls through to the provider chain. Switching
  // to Custom shows the blank edit form; the slot stays on whatever it was
  // (shipped or empty) until the user saves explicit creds.
  async function setMode(value: string | undefined) {
    if (!value) return
    const next = value as SourceMode
    if (next === mode) return
    mode = next
    if (next === 'custom') {
      clientID = ''
      clientSecret = ''
      return
    }
    // Switching to Aerion-shipped — clear any user override.
    try {
      await ClearOAuthCreds(configID)
      toasts.success(`${label} is now using Aerion's credentials`)
      await refresh()
      onChanged?.()
    } catch (err) {
      console.error('Failed to switch to Aerion-shipped creds:', err)
      toasts.error(`Failed to switch credentials: ${(err as Error)?.message ?? err}`)
      await refresh()
    }
  }

  async function save() {
    if (!clientID.trim()) {
      toasts.error('Client ID is required')
      return
    }
    if (secretRequired && !clientSecret.trim()) {
      toasts.error('Client Secret is required')
      return
    }
    saving = true
    try {
      await SetOAuthCreds(configID, clientID.trim(), clientSecret.trim())
      toasts.success(`${label} credentials saved`)
      clientID = ''
      clientSecret = ''
      await refresh()
      onChanged?.()
    } catch (err) {
      console.error('Failed to save OAuth creds:', err)
      toasts.error('Failed to save credentials')
    } finally {
      saving = false
    }
  }
</script>

<div class="border border-border rounded-md p-4 bg-card">
  <div class="flex items-start justify-between gap-3">
    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-2 flex-wrap">
        <h4 class="font-medium text-foreground">{label}</h4>
        {#if loading}
          <Icon icon="mdi:loading" class="w-3.5 h-3.5 animate-spin text-muted-foreground" />
        {:else if status?.hasUserOverride}
          <span class="text-xs px-2 py-0.5 rounded bg-primary/15 text-primary">Custom</span>
        {:else if status?.hasShipped}
          <span class="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">Aerion</span>
        {:else}
          <span class="text-xs px-2 py-0.5 rounded bg-destructive/15 text-destructive">Not configured</span>
        {/if}
        {#if status?.clientIdFingerprint}
          <code class="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">{status.clientIdFingerprint}</code>
        {/if}
      </div>
      <p class="text-xs text-muted-foreground mt-1 font-mono">{configID}</p>
    </div>
  </div>

  <div class="mt-3 flex items-center gap-2">
    <Label class="text-xs text-muted-foreground">Client ID/Secret:</Label>
    <Select.Root value={mode} onValueChange={setMode}>
      <Select.Trigger class="h-8 w-[220px] text-sm">
        <Select.Value placeholder="Custom">
          {mode === 'aerion-shipped' ? shippedOptionLabel : 'Custom'}
        </Select.Value>
      </Select.Trigger>
      <Select.Content>
        <Select.Item value="custom" label="Custom" />
        {#if shippedOptionAvailable}
          <Select.Item value="aerion-shipped" label={shippedOptionLabel} />
        {/if}
      </Select.Content>
    </Select.Root>
  </div>

  {#if mode === 'custom'}
    <div class="mt-4 space-y-3">
      <div>
        <Label for={`${configID}-client-id`}>Client ID</Label>
        <Input
          id={`${configID}-client-id`}
          type="text"
          bind:value={clientID}
          placeholder={status?.hasUserOverride ? 'paste a new Client ID to replace' : 'paste Client ID'}
          disabled={saving}
          autocomplete="off"
        />
      </div>
      {#if secretRequired}
        <div>
          <Label for={`${configID}-client-secret`}>Client Secret</Label>
          <Input
            id={`${configID}-client-secret`}
            type="password"
            bind:value={clientSecret}
            placeholder={status?.hasUserOverride ? 'paste a new Client Secret to replace' : 'paste Client Secret'}
            disabled={saving}
            autocomplete="new-password"
          />
        </div>
      {/if}
      <div class="flex items-center justify-end gap-2 pt-2">
        <Button size="sm" onclick={save} disabled={saving}>
          {#if saving}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
          {/if}
          Save
        </Button>
      </div>
    </div>
  {/if}
</div>
