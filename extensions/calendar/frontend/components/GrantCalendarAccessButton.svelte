<script lang="ts">
  // Shared button for the incremental-consent OAuth flow
  // (Calendar_GrantCalendarAccess). Used by the Add Google/Microsoft dialogs,
  // the account-setup hook panels, and the per-source Reauthorize button in
  // calendar settings. Owns the in-flight state + the bridge call + error-
  // message normalization; callers supply the labels and the success/error
  // handlers (which vary per site — re-fetch vs reload, toast vs inline error).
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  // @ts-ignore - wailsjs bindings
  import { Calendar_GrantCalendarAccess } from '$wailsjs/go/app/App.js'

  interface Props {
    provider: 'google' | 'microsoft'
    accountId: string
    email: string
    idleLabel: string
    busyLabel: string
    variant?: 'default' | 'outline' | 'ghost'
    size?: 'default' | 'sm'
    class?: string
    title?: string
    /** Show the provider logo (mdi:google / mdi:microsoft) before the label. */
    providerIcon?: boolean
    onSuccess?: () => void | Promise<void>
    onError?: (message: string) => void
  }

  let {
    provider,
    accountId,
    email,
    idleLabel,
    busyLabel,
    variant = 'outline',
    size = 'sm',
    class: className = '',
    title,
    providerIcon = false,
    onSuccess,
    onError,
  }: Props = $props()

  const iconName = $derived(provider === 'google' ? 'mdi:google' : 'mdi:microsoft')

  let granting = $state(false)

  async function run() {
    if (granting || !accountId) return
    granting = true
    try {
      await Calendar_GrantCalendarAccess(provider, accountId, email)
      await onSuccess?.()
    } catch (err) {
      onError?.((err as Error)?.message ?? String(err))
    } finally {
      granting = false
    }
  }
</script>

<Button {variant} {size} class={className} {title} onclick={run} disabled={granting}>
  {#if providerIcon}
    <Icon icon={iconName} class="w-4 h-4 mr-2" />
  {/if}
  {granting ? busyLabel : idleLabel}
</Button>
