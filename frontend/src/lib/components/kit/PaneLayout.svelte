<script lang="ts">
  // PaneLayout — outer flex container for kit-based extension panes that
  // composes SourceSidebar + ListPane + DetailPane (or any subset). Provides:
  //
  //   1. A 'relative' positioning context so the kit primitives' absolute-
  //      positioned overlay states (responsive-sidebar-overlay,
  //      responsive-viewer-overlay) anchor to this layout, not the page.
  //   2. The responsive scrim that darkens behind the sidebar overlay on
  //      narrow viewports. Clicking the scrim dismisses the sidebar.
  //
  // Behavior is 1-for-1 with mail's App.svelte layout (lines 1407-1417):
  // scrim is always rendered in narrow mode; the `-visible` modifier turns
  // it on when the sidebar overlay is open.
  //
  // Extensions don't need to import the layout store, manage scrim state,
  // or wire pane-class merging — that all lives in the kit primitives
  // themselves. ContactsPane.svelte is the canonical consumer.

  import { type Snippet } from 'svelte'
  import { getLayoutMode, getResponsiveView, hideSidebar } from '$lib/stores/layout.svelte'

  interface Props {
    children: Snippet
  }

  const { children }: Props = $props()
</script>

<!--
  overflow-hidden is critical: it clips the absolute-positioned overlays
  (sidebar slid left via translateX(-100%), viewer slid right via
  translateX(100%)) so their hit-test regions don't escape PaneLayout into
  sibling layout (notably the ExtensionRail at the left). Without this, the
  hidden sidebar's leftward-translated hit area intercepts rail clicks. Mail
  gets the same clipping for free via App.svelte's outer container.
-->
<div class="flex flex-1 min-w-0 h-full relative overflow-hidden">
  {@render children()}

  {#if getLayoutMode() === 'narrow'}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="responsive-scrim {getResponsiveView() === 'sidebar' ? 'responsive-scrim-visible' : ''}"
      onclick={hideSidebar}
    ></div>
  {/if}
</div>
