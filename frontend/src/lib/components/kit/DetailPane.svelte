<script lang="ts">
  // DetailPane — generic right-pane shell: optional header slot, scrollable
  // body slot, and an empty-state slot rendered when `empty` is true. No
  // keyboard ownership (read-only viewing area; keys pass through to global
  // handler).

  import { type Snippet } from 'svelte'
  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import { isPaneFlashing, type FocusablePane } from '$lib/stores/keyboard.svelte'
  // Self-managed responsive (mobile + tablet) behavior — read the layout
  // store directly so consumers (extension panes) never forward responsive
  // props. At medium (≤1024px) and narrow (≤767px) the detail pane renders
  // as an overlay AND a back-arrow button is injected at the start of the
  // header (calls hideViewer to return to the list). Matches mail's
  // ConversationViewer pattern at App.svelte:1488 1-for-1.
  import { isResponsive, getResponsiveView, hideViewer } from '$lib/stores/layout.svelte'

  interface Props {
    /** True when there's nothing to show. Empty snippet renders when true. */
    empty?: boolean
    /** Which pane-focus slot this detail pane occupies (default 'viewer'). */
    focusSlot?: FocusablePane
    /** Header snippet (typically: title + action buttons). */
    header?: Snippet
    /** Body snippet (typically scrollable content). */
    body?: Snippet
    /** Empty-state snippet. If absent, a default placeholder renders. */
    emptyState?: Snippet
    /** Iconify identifier used in the default empty-state placeholder. */
    emptyIcon?: string
    /** Text used in the default empty-state placeholder. */
    emptyText?: string
  }

  const {
    empty = false,
    focusSlot = 'viewer',
    header,
    body,
    emptyState,
    emptyIcon = 'mdi:tray-arrow-down',
    emptyText = 'Nothing selected.',
  }: Props = $props()

  const flashing = $derived(isPaneFlashing(focusSlot))
  const overlay = $derived(isResponsive())
  const visible = $derived(getResponsiveView() === 'viewer')
</script>

<section class="flex-1 min-w-0 flex flex-col bg-background {flashing ? 'pane-focus-flash' : ''} {overlay ? 'responsive-viewer-overlay' : ''} {overlay && visible ? 'responsive-viewer-visible' : ''}">
  <!--
    Header rendering rules — matched 1-for-1 with mail's ConversationViewer
    pattern at App.svelte:1488 (showBackButton={isResponsive()}):

      - Full (>1024px): render header bar ONLY when the consumer provided a
        `header` snippet AND we're not in the empty state. No back button.
      - Responsive (medium ≤1024px OR narrow ≤767px): render header bar
        UNCONDITIONALLY so the back button is always visible. Both medium and
        narrow render the detail pane as an overlay (per responsive-viewer-
        overlay CSS) — both need the back affordance. Using `narrow` only
        would strand users at medium breakpoints (laptop tablet windows) with
        a visible overlay and no back button.
  -->
  {#if overlay || (!empty && header)}
    <header class="flex items-center gap-3 px-4 py-3 border-b border-border">
      {#if overlay}
        <button
          type="button"
          class="p-2 rounded-md hover:bg-muted transition-colors flex-shrink-0"
          onclick={hideViewer}
          aria-label={$_('common.back')}
        >
          <Icon icon="mdi:arrow-left" class="w-5 h-5 text-muted-foreground" />
        </button>
      {/if}
      {#if !empty && header}
        {@render header()}
      {/if}
    </header>
  {/if}

  {#if empty}
    <div class="flex-1 flex flex-col items-center justify-center text-muted-foreground gap-3 p-6">
      {#if emptyState}
        {@render emptyState()}
      {:else}
        <Icon icon={emptyIcon} width="48" height="48" />
        <p class="text-lg">{emptyText}</p>
      {/if}
    </div>
  {:else}
    <div class="flex-1 min-h-0 overflow-y-auto p-6">
      {#if body}
        {@render body()}
      {/if}
    </div>
  {/if}
</section>
