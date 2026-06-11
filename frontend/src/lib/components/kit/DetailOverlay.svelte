<script lang="ts">
  // DetailOverlay — generic right-side detail overlay primitive for
  // extensions. Three positioning modes driven by the `focused` prop and
  // the layout store:
  //
  //   Regular desktop  : position: fixed; right:0; top:0; bottom:0; w-[340px]
  //   Focused (desktop): position: fixed; inset:0
  //   Responsive       : position: fixed; inset:0 + auto-back-button
  //
  // No scrim: the view underneath stays interactive (consumers can swap
  // the body content by changing the children snippet's data, without
  // dismissing the overlay).
  //
  // Esc handler: while open, traps Esc — first press exits focus mode
  // (calls onToggleFocus); subsequent press calls onClose. Prevents
  // propagation so other Esc listeners (mail's global handler, dialogs)
  // don't double-fire.
  //
  // CONTAINING-BLOCK CAVEAT: position: fixed is relative to the viewport
  // ONLY when no ancestor has transform/filter/perspective/contain:
  // layout|paint/will-change. The Aerion ancestry (App.svelte → rail/pane
  // tree → consumer pane) currently has none of those. If a future PR
  // introduces one, this overlay would mis-position. Test before adding
  // any of those properties to wrapping divs.

  import { type Snippet, onMount, onDestroy } from 'svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import Icon from '@iconify/svelte'
  import { isResponsive } from '$lib/stores/layout.svelte'

  interface Props {
    open: boolean
    focused?: boolean
    title?: string
    onClose?: () => void
    onToggleFocus?: () => void
    children?: Snippet
  }

  let {
    open = $bindable(false),
    focused = $bindable(false),
    title = '',
    onClose,
    onToggleFocus,
    children,
  }: Props = $props()

  // Layout state — auto-fill the window in responsive (mobile) mode.
  const responsive = $derived(isResponsive())

  // Effective fill: focused OR responsive → cover the viewport.
  const fillsWindow = $derived(focused || responsive)

  // Esc handling. Use a window listener so the overlay catches Esc no
  // matter which descendant has focus. Bound only while `open`.
  function handleKeyDown(e: KeyboardEvent) {
    if (!open) return
    if (e.key !== 'Escape') return
    if (focused) {
      e.preventDefault()
      e.stopPropagation()
      onToggleFocus?.()
      return
    }
    e.preventDefault()
    e.stopPropagation()
    onClose?.()
  }

  onMount(() => {
    window.addEventListener('keydown', handleKeyDown, true)
  })

  onDestroy(() => {
    window.removeEventListener('keydown', handleKeyDown, true)
  })
</script>

{#if open}
  <div
    class="fixed top-0 bottom-0 z-50 bg-background shadow-xl flex flex-col
           transition-[width,left,right] duration-200 ease-out
           {fillsWindow ? 'inset-0' : 'right-0 w-[340px] border-l border-border'}"
    role="dialog"
    aria-modal="false"
    transition:fly={{ x: 360, duration: 200, easing: cubicOut }}
  >
    <!-- Header bar: back (responsive only), title, focus toggle, close. -->
    <div class="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
      {#if responsive}
        <button
          type="button"
          class="p-1 rounded hover:bg-muted/40"
          onclick={() => onClose?.()}
          aria-label="Back"
          title="Back"
        >
          <Icon icon="mdi:arrow-left" class="w-5 h-5 text-muted-foreground" />
        </button>
      {/if}
      <span class="flex-1 min-w-0 truncate text-sm font-medium text-foreground">
        {title}
      </span>
      <button
        type="button"
        class="p-1 rounded hover:bg-muted/40"
        onclick={() => onToggleFocus?.()}
        aria-label={focused ? 'Exit focus' : 'Enter focus mode'}
        title={focused ? 'Exit focus' : 'Enter focus mode'}
      >
        <Icon
          icon={focused ? 'mdi:fullscreen-exit' : 'mdi:fullscreen'}
          class="w-5 h-5 text-muted-foreground"
        />
      </button>
      <button
        type="button"
        class="p-1 rounded hover:bg-muted/40"
        onclick={() => onClose?.()}
        aria-label="Close"
        title="Close"
      >
        <Icon icon="mdi:close" class="w-5 h-5 text-muted-foreground" />
      </button>
    </div>

    <!-- Body: scrollable area for arbitrary consumer content. -->
    <div class="flex-1 min-h-0 overflow-y-auto">
      {#if children}{@render children()}{/if}
    </div>
  </div>
{/if}
