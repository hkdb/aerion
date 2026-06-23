<script lang="ts">
  // OAuthCredsSlotEditor — single-slot OAuth credential editor primitive.
  // Used by Aerion core's "OAuth Credentials (advanced)" section (Settings →
  // Accounts) AND by each extension's settings dialog.
  //
  // Props:
  //   configID            — the slot identifier (e.g., "google-mail",
  //                         "google-contacts")
  //   extensionID         — the manifest id of the consuming extension
  //                         (e.g., "contacts", "calendar"). Omit (or pass "")
  //                         for core/mail's settings UI — then the backend
  //                         skips the manifest lookup and the "Aerion mail
  //                         client" option never appears.
  //   label               — display name (e.g., "Google Mail")
  //   secretRequired      — whether the slot needs a client_secret (true for
  //                         Google; false for Microsoft / PKCE)
  //
  // UX:
  //   Dropdown enumerates the available credential sources for this slot.
  //   Choice IDs come from the backend (GetOAuthCredsChoices); persistence
  //   goes through SetOAuthCredsChoice. Possible IDs today:
  //     - "custom"          — user-pasted client_id/secret. Selecting reveals
  //                           the edit form.
  //     - "aerion-shipped"  — the slot's own shipped client (compiled in via
  //                           the extension's .env / Makefile ldflags). Labeled
  //                           by the backend per slot ("Aerion - Google",
  //                           "Aerion - Microsoft", "Aerion testing", etc.).
  //     - "aerion-mail"     — reuse the core mail OAuth slot for scopes the
  //                           extension manifest declares as core-routable
  //                           (first_party_uses_core_for_scopes).
  //
  //   Picking a non-custom option writes the choice via SetOAuthCredsChoice
  //   (which clears any user override and sets/clears the slot alias as
  //   appropriate). Picking Custom shows the empty form; user pastes + Saves
  //   → SetOAuthCreds writes the user override AND SetOAuthCredsChoice
  //   ensures any stale alias is cleared.

  import { onMount } from 'svelte'
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import * as Select from '$lib/components/ui/select'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { GetOAuthCredsChoices, SetOAuthCreds, SetOAuthCredsChoice, ClearOAuthCreds } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { app } from '$wailsjs/go/models'

  interface Props {
    configID: string
    label: string
    extensionID?: string
    secretRequired?: boolean
  }

  const { configID, label, extensionID = '', secretRequired = true }: Props = $props()

  let choices = $state<app.OAuthCredsChoices | null>(null)
  let loading = $state(true)
  let mode = $state<string>('custom')
  let clientID = $state('')
  let clientSecret = $state('')
  let saving = $state(false)

  function currentChoiceLabel(): string {
    const match = choices?.choices.find(c => c.id === mode)
    if (match) return match.label
    return 'Custom'
  }

  // Status badge driven by the currently-selected mode rather than two
  // independent has* booleans. Maps to the same three visual states the
  // previous version had: Custom / Aerion / Not configured.
  function statusBadgeKind(): 'custom' | 'aerion' | 'unset' {
    if (loading) return 'unset'
    if (mode === 'custom' && choices?.hasUserOverride) return 'custom'
    if (mode !== 'custom') return 'aerion'
    return 'unset'
  }

  async function refresh() {
    loading = true
    try {
      choices = await GetOAuthCredsChoices(configID, extensionID)
      mode = choices?.current || 'custom'
    } catch (err) {
      console.error('Failed to load OAuth creds choices:', err)
      choices = null
      mode = 'custom'
    } finally {
      loading = false
    }
  }

  onMount(refresh)

  // Dropdown change handler. Custom shows the blank edit form; any other
  // choice gets persisted server-side via SetOAuthCredsChoice (which clears
  // any conflicting custom override and aligns the alias state).
  async function setMode(value: string | undefined) {
    if (!value) return
    if (value === mode) return
    const next = value
    mode = next
    if (next === 'custom') {
      clientID = ''
      clientSecret = ''
      try {
        await SetOAuthCredsChoice(configID, 'custom')
      } catch (err) {
        console.warn('Failed to record Custom as active choice:', err)
      }
      // Refresh so the UI reflects the now-current state. Without this,
      // hasUserOverride / fingerprint / badge stay stale from the
      // previous mode and the editor reads as "Not configured" even
      // when the saved Custom row still exists in the DB.
      await refresh()
      return
    }
    try {
      await SetOAuthCredsChoice(configID, next)
      const labelText = choices?.choices.find(c => c.id === next)?.label ?? next
      toasts.success(`${label} is now using ${labelText}`)
      await refresh()
    } catch (err) {
      console.error('Failed to switch OAuth choice:', err)
      toasts.error(`Failed to switch credentials: ${(err as Error)?.message ?? err}`)
      await refresh()
    }
  }

  async function save() {
    const id = clientID.trim()
    const secret = clientSecret.trim()

    // No-op-on-empty-with-override: when the user has already saved
    // credentials and clicks Save with both fields blank, treat it as
    // "I'm not changing anything" and silently close. Matches the IMAP
    // password "Leave empty to keep current" pattern advertised by the
    // placeholder copy below.
    const requiredFieldsAllBlank = !id && (!secretRequired || !secret)
    if (choices?.hasUserOverride && requiredFieldsAllBlank) {
      return
    }

    if (!id) {
      toasts.error('Client ID is required')
      return
    }
    if (secretRequired && !secret) {
      toasts.error('Client Secret is required')
      return
    }
    saving = true
    try {
      await SetOAuthCreds(configID, id, secret)
      // SetOAuthCreds writes the user override; ensure the active-choice
      // marker reflects Custom so the resolver routes through the new
      // override on the next OAuth call.
      try {
        await SetOAuthCredsChoice(configID, 'custom')
      } catch (err) {
        console.warn('Failed to record active choice after Custom save:', err)
      }
      toasts.success(`${label} credentials saved`)
      clientID = ''
      clientSecret = ''
      await refresh()
    } catch (err) {
      console.error('Failed to save OAuth creds:', err)
      toasts.error('Failed to save credentials')
    } finally {
      saving = false
    }
  }

  // Explicit delete of the stored Custom credentials. The picker
  // dropdown no longer destroys stored values when switching between
  // options (preserving Custom across round trips); this button is the
  // only path that actually wipes the user_oauth_clients row.
  async function clearSavedCustom() {
    if (!choices?.hasUserOverride) return
    if (!confirm(`Clear your saved Custom credentials for ${label}? You'll need to re-paste them to use Custom again.`)) {
      return
    }
    try {
      await ClearOAuthCreds(configID)
      toasts.success(`${label} saved Custom credentials cleared`)
      clientID = ''
      clientSecret = ''
      await refresh()
    } catch (err) {
      console.error('Failed to clear saved Custom credentials:', err)
      toasts.error('Failed to clear saved credentials')
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
        {:else if statusBadgeKind() === 'custom'}
          <span class="text-xs px-2 py-0.5 rounded bg-primary/15 text-primary">Custom</span>
        {:else if statusBadgeKind() === 'aerion'}
          <span class="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">Aerion</span>
        {:else}
          <span class="text-xs px-2 py-0.5 rounded bg-destructive/15 text-destructive">Not configured</span>
        {/if}
        {#if choices?.clientIdFingerprint}
          <code class="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">{choices.clientIdFingerprint}</code>
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
          {currentChoiceLabel()}
        </Select.Value>
      </Select.Trigger>
      <Select.Content>
        {#each (choices?.choices ?? []) as choice (choice.id)}
          <Select.Item value={choice.id} label={choice.label} />
        {/each}
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
          placeholder={choices?.hasUserOverride ? 'Leave empty to keep current' : 'Paste Client ID'}
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
            placeholder={choices?.hasUserOverride ? 'Leave empty to keep current' : 'Paste Client Secret'}
            disabled={saving}
            autocomplete="new-password"
          />
        </div>
      {/if}
      <div class="flex items-center gap-2 pt-2">
        {#if choices?.hasUserOverride}
          <!-- Outlined (not ghost) so it reads as a clickable button, not
               an inline error message. Pushed to the left via mr-auto so
               it's visually separated from Save on the right. -->
          <Button size="sm" variant="outline" class="mr-auto border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive" onclick={clearSavedCustom} disabled={saving}>
            <Icon icon="mdi:delete-outline" class="w-4 h-4 mr-1" />
            Clear saved Custom credentials
          </Button>
        {:else}
          <!-- Keep Save right-aligned in the no-override case too. -->
          <div class="mr-auto"></div>
        {/if}
        <Button size="sm" onclick={save} disabled={saving}>
          {#if saving}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
          {/if}
          Save
        </Button>
      </div>
    </div>
  {:else if choices?.hasUserOverride}
    <!-- Mode is aerion-shipped or aerion-mail, but the user has Custom
         credentials saved underneath. Surface that explicitly so the
         user knows their data wasn't wiped by the switch and can route
         back to it cheaply. -->
    <p class="mt-3 text-xs text-muted-foreground">
      You also have a saved Custom override — switch to Custom to use it.
    </p>
  {/if}
</div>
