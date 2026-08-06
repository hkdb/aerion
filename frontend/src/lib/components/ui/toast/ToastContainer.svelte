<script lang="ts">
  import Icon from '@iconify/svelte'
  import { toasts } from '$lib/stores/toast'
  import { _ } from '$lib/i18n'
  import Toast from './Toast.svelte'

  // Cap the stack at MAX_VISIBLE cards TOTAL, newest on top. With more toasts
  // than that, the bottom card becomes the pile: it shows the next toast in
  // line with peeking edges and a +N badge for the ones stacked behind it.
  // Clicking the badge (or the peeking edges) expands the full list; a
  // chevron pill collapses it again. Purely presentational — every toast
  // keeps its own expiry timer in the store, so the pile drains itself.
  const MAX_VISIBLE = 3

  let expanded = $state(false)
  let newestFirst = $derived([...$toasts].reverse())
  let stacked = $derived(!expanded && $toasts.length > MAX_VISIBLE)
  let fullCards = $derived(stacked ? newestFirst.slice(0, MAX_VISIBLE - 1) : newestFirst)
  let hiddenBehind = $derived(Math.max(0, $toasts.length - MAX_VISIBLE))

  // Once the backlog drains below the cap there is nothing to expand — snap
  // back so the next burst starts collapsed again.
  $effect(() => {
    if ($toasts.length <= MAX_VISIBLE) {
      expanded = false
    }
  })
</script>

<div
  class="fixed bottom-4 inset-x-4 z-50 flex flex-col gap-2 max-w-sm ml-auto pointer-events-none {expanded
    ? 'max-h-[70vh] overflow-y-auto pointer-events-auto'
    : ''}"
>
  {#each fullCards as toast (toast.id)}
    <div class="pointer-events-auto">
      <Toast {toast} onClose={() => toasts.remove(toast.id)} />
    </div>
  {/each}

  {#if stacked}
    {@const pile = newestFirst[MAX_VISIBLE - 1]}
    <div class="pointer-events-auto relative">
      {#if hiddenBehind > 1}
        <button
          class="absolute inset-x-3 -bottom-3 top-3 rounded-lg border border-muted-foreground/25 bg-muted shadow-lg"
          onclick={() => (expanded = true)}
          aria-label={$_('aria.showAllToasts')}
        ></button>
      {/if}
      <button
        class="absolute inset-x-1.5 -bottom-1.5 top-1.5 rounded-lg border border-muted-foreground/40 bg-muted shadow-lg"
        onclick={() => (expanded = true)}
        aria-label={$_('aria.showAllToasts')}
      ></button>
      <div class="relative">
        <Toast toast={pile} onClose={() => toasts.remove(pile.id)} />
        <button
          class="absolute -top-2 -right-2 text-xs font-medium px-1.5 py-0.5 rounded-full bg-muted border border-border text-muted-foreground shadow hover:bg-accent transition-colors"
          onclick={() => (expanded = true)}
          aria-label={$_('aria.showAllToasts')}
        >
          +{hiddenBehind}
        </button>
      </div>
    </div>
  {/if}

  {#if expanded}
    <div class="pointer-events-auto flex justify-end">
      <button
        class="px-2 py-1 rounded-full bg-muted border border-border text-muted-foreground shadow hover:bg-accent transition-colors"
        onclick={() => (expanded = false)}
        aria-label={$_('aria.collapseToasts')}
      >
        <Icon icon="mdi:chevron-up" class="w-4 h-4" />
      </button>
    </div>
  {/if}
</div>
