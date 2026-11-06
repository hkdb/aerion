<script module lang="ts">
  import { unsubscribe as unsubscribeModels } from '../../../../wailsjs/go/models'

  // Shared across instances so collapsing/expanding a message (which remounts
  // this component) neither re-queries the backend nor forgets a completed
  // unsubscribe during the session.
  const infoCache = new Map<string, unsubscribeModels.Info | null>()
  const unsubscribedIds = new Set<string>()
</script>

<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '@iconify/svelte'
  import { GetUnsubscribeInfo, Unsubscribe } from '../../../../wailsjs/go/app/App'
  import { ConfirmDialog } from '$lib/components/ui/confirm-dialog'
  import { toasts } from '$lib/stores/toast'
  import { _ } from '$lib/i18n'

  interface Props {
    messageId: string
    onArchive?: () => void   // offered as a toast action after a successful unsubscribe
  }

  let { messageId, onArchive }: Props = $props()

  let info = $state<unsubscribeModels.Info | null>(null)
  let busy = $state(false)
  let done = $state(false)
  let showConfirm = $state(false)

  const descriptionKeys: Record<string, string> = {
    'one-click': 'viewer.unsubscribeDescOneClick',
    mailto: 'viewer.unsubscribeDescMailto',
    browser: 'viewer.unsubscribeDescBrowser',
  }

  const doneKeys: Record<string, string> = {
    'one-click': 'viewer.unsubscribeOneClickDone',
    mailto: 'viewer.unsubscribeMailtoDone',
    browser: 'viewer.unsubscribeBrowserDone',
  }

  const description = $derived.by(() => {
    if (!info) return ''
    const key = info.fromBody ? 'viewer.unsubscribeDescBody' : descriptionKeys[info.method]
    return $_(key, { values: { target: info.target } })
  })

  onMount(async () => {
    done = unsubscribedIds.has(messageId)
    if (infoCache.has(messageId)) {
      info = infoCache.get(messageId) ?? null
      return
    }
    try {
      const result = await GetUnsubscribeInfo(messageId)
      info = result?.available ? result : null
      infoCache.set(messageId, info)
    } catch (err) {
      // Not cached: a transient failure (e.g. offline) is retried on next mount
      console.debug('GetUnsubscribeInfo failed:', err)
    }
  })

  async function handleConfirm() {
    showConfirm = false
    if (busy) return
    busy = true
    try {
      const result = await Unsubscribe(messageId)
      unsubscribedIds.add(messageId)
      done = true
      const actions = onArchive ? [{ label: $_('viewer.archive'), onClick: onArchive }] : []
      toasts.success($_(doneKeys[result.method] ?? doneKeys['one-click'], { values: { target: result.target } }), actions)
    } catch (err) {
      console.error('Unsubscribe failed:', err)
      toasts.error($_('viewer.unsubscribeFailed'))
    } finally {
      busy = false
    }
  }
</script>

{#if done}
  <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs text-muted-foreground">
    <Icon icon="mdi:check" class="w-3.5 h-3.5" />
    {$_('viewer.unsubscribed')}
  </span>
{:else if info}
  <button
    class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full border border-border text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-50"
    title={$_('viewer.unsubscribeTooltip')}
    disabled={busy}
    onclick={(e) => { e.stopPropagation(); showConfirm = true }}
    onkeydown={(e) => e.stopPropagation()}
  >
    <Icon icon={busy ? 'mdi:loading' : 'mdi:email-remove-outline'} class="w-3.5 h-3.5 {busy ? 'animate-spin' : ''}" />
    {$_('viewer.unsubscribe')}
  </button>
{/if}

<ConfirmDialog
  bind:open={showConfirm}
  title={$_('viewer.unsubscribeTitle')}
  {description}
  confirmLabel={$_('viewer.unsubscribe')}
  cancelLabel={$_('common.cancel')}
  onConfirm={handleConfirm}
  onCancel={() => showConfirm = false}
/>
