<script lang="ts">
  // SidebarFrame — kit primitive owning the chrome shared between all
  // extension sidebars (kit `SourceSidebar` and any custom-row extension
  // sidebars like Calendar's). Captures container styling, responsive
  // overlay behavior (slide-in + back button in narrow mode), optional
  // title, scrollable body slot, and optional sticky footer slot.
  //
  // Why this primitive exists: it's the single hook for upcoming
  // cross-extension sidebar settings (density, font-size variants) — when
  // those settings ship, the density-aware classes live here and all
  // consumers (calendar directly, contacts via SourceSidebar composition,
  // future extensions) inherit automatically.
  //
  // Layout model: title stays non-scrolling at the top; body fills the
  // remaining vertical space with its own overflow-y-auto; optional footer
  // stays pinned at the bottom. This split is intrinsic — it's what lets
  // consumers like Calendar pin a sync/settings strip at the bottom while
  // the calendar list scrolls in the middle.

  import { type Snippet } from 'svelte'
  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import { getLayoutMode, getResponsiveView, hideSidebar } from '$lib/stores/layout.svelte'

  interface Props {
    /** Optional title rendered as <h2>. Omit for sidebars with no title. */
    title?: string
    /** ARIA label for the <aside>. Defaults to `title`. */
    label?: string
    /** The scrollable body content. Required. */
    body: Snippet
    /** Optional sticky bottom strip. Consumer owns the strip's chrome
     *  (border-t, padding, content); SidebarFrame just pins it with shrink-0. */
    footer?: Snippet
    /** Bindable ref to the outer <aside>. SourceSidebar binds this for its
     *  tabindex-based keyboard nav + focus-slot integration. */
    containerRef?: HTMLElement | null
    /** When true, the <aside> gets tabindex="0" so it can take DOM focus.
     *  Default false. */
    focusable?: boolean
    /** Extra class string appended to the outer <aside>. Used by SourceSidebar
     *  for its pane-focus-flash indicator. */
    class?: string
    /** Optional DOM event handlers forwarded to the outer <aside>. */
    onkeydown?: (e: KeyboardEvent) => void
    onfocus?: () => void
    onmousedown?: (e: MouseEvent) => void
  }

  let {
    title,
    label,
    body,
    footer,
    containerRef = $bindable(null),
    focusable = false,
    class: extraClass = '',
    onkeydown,
    onfocus,
    onmousedown,
  }: Props = $props()

  const narrow = $derived(getLayoutMode() === 'narrow')
  const overlayVisible = $derived(narrow && getResponsiveView() === 'sidebar')
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<aside
  bind:this={containerRef}
  role="navigation"
  aria-label={label ?? title ?? 'Sidebar'}
  tabindex={focusable ? 0 : undefined}
  class="w-60 flex-shrink-0 flex flex-col pt-3 border-r border-border outline-none {narrow ? 'bg-background' : 'bg-muted/30'} {narrow ? 'responsive-sidebar-overlay' : ''} {overlayVisible ? 'responsive-sidebar-visible' : ''} {extraClass}"
  {onkeydown}
  {onfocus}
  {onmousedown}
>
  {#if narrow}
    <button
      type="button"
      class="flex items-center gap-2 px-4 py-2 mb-2 text-sm text-muted-foreground hover:text-foreground"
      onclick={hideSidebar}
      aria-label={$_('common.back')}
    >
      <Icon icon="mdi:arrow-left" class="w-4 h-4" />
      <span>{$_('common.back')}</span>
    </button>
  {/if}

  {#if title}
    <h2 class="px-4 mb-3 text-lg font-semibold text-foreground">{title}</h2>
  {/if}

  <div class="flex-1 min-h-0 overflow-y-auto">
    {@render body()}
  </div>

  {#if footer}
    <div class="shrink-0">
      {@render footer()}
    </div>
  {/if}
</aside>
