<script lang="ts">
  // ResponsiveSidebarToggle — drop-in hamburger button for kit-based 3-column
  // extension panes. Renders nothing when not narrow; renders an mdi:dock-left
  // icon button when narrow that fires showSidebar() on click.
  //
  // Mirrors mail's pattern at App.svelte:1452-1453 (showFolderToggle +
  // onToggleSidebar) but keeps the gating, icon choice, label, and store
  // access inside the kit so every extension consumer just composes:
  //
  //   <ResponsiveSidebarToggle />
  //
  // at the leading edge of their list-pane toolbar. No props, no plumbing.
  // The kit owns the canonical narrow-mode UX.

  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import { getLayoutMode, showSidebar } from '$lib/stores/layout.svelte'
</script>

{#if getLayoutMode() === 'narrow'}
  <button
    type="button"
    class="p-1.5 -ml-1 rounded-md hover:bg-muted transition-colors"
    title={$_('aria.toggleSidebar')}
    aria-label={$_('aria.toggleSidebar')}
    onclick={showSidebar}
  >
    <Icon icon="mdi:dock-left" class="w-5 h-5 text-muted-foreground" />
  </button>
{/if}
