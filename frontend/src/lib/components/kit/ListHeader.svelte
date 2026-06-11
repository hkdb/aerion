<script lang="ts">
  // ListHeader — canonical toolbar bar for kit-based 3-column extension panes.
  // Lives at the top of the list column (above ListPane) and owns:
  //
  //   1. The toolbar wrapper styling (border, padding, flex layout) so every
  //      extension's list column renders with the same visual rhythm as mail's
  //      MessageList toolbar.
  //   2. The leading <ResponsiveSidebarToggle /> — auto-included so extensions
  //      don't have to remember to place a hamburger toggle for narrow mode.
  //   3. The title <h2> + optional count badge layout matching mail.
  //   4. A search-mode swap: when `searchMode` is true, the title area is
  //      replaced by the consumer's `search` snippet (so the consumer can
  //      provide its own search input with bindings, refs, and clear-button
  //      handling that the kit can't generically own).
  //   5. A trailing `actions` snippet for per-extension toolbar buttons (sort,
  //      add, etc.) sitting at the right edge.
  //
  // What the kit does NOT own:
  //   - The label VALUE (extension knows about sources/folders/categories;
  //     kit just renders whatever string the consumer passes).
  //   - The search input markup, state, and keyboard handling (per-extension
  //     concerns — clear button, debounce, focus management).
  //   - The action buttons themselves (sort orders, add behavior, etc. are
  //     extension-specific).
  //
  // Mail-side `MessageList.svelte` doesn't (yet) use this primitive — it
  // predates the kit. If mail ever adopts the kit, this primitive is shaped
  // to be a 1-for-1 drop-in. That's the 1-for-1 kit rule applied to this
  // pattern.

  import { type Snippet } from 'svelte'
  import ResponsiveSidebarToggle from './ResponsiveSidebarToggle.svelte'

  interface Props {
    /** Title text rendered as <h2> when not in search mode. */
    label: string
    /** Optional count badge rendered after the label. Null/undefined hides it. */
    count?: number | null
    /** When true, the title + count area is replaced by the search snippet. */
    searchMode?: boolean
    /** Rendered in place of the title when searchMode is true. */
    search?: Snippet
    /** Trailing toolbar buttons (sort, add, etc.). Rendered right-aligned. */
    actions?: Snippet
  }

  const {
    label,
    count = null,
    searchMode = false,
    search,
    actions,
  }: Props = $props()
</script>

<div class="flex items-center justify-between px-4 py-3 border-b border-border">
  <div class="flex items-center gap-2 flex-1 min-w-0">
    <ResponsiveSidebarToggle />
    {#if searchMode && search}
      {@render search()}
    {:else}
      <h2 class="font-semibold text-foreground truncate">{label}</h2>
      {#if count != null}
        <span class="text-sm text-muted-foreground flex-shrink-0">{count}</span>
      {/if}
    {/if}
  </div>
  {#if actions}
    <div class="flex items-center gap-1 flex-shrink-0">
      {@render actions()}
    </div>
  {/if}
</div>
