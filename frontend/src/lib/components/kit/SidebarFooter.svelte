<script lang="ts">
  // SidebarFooter — canonical bottom-strip chrome shared by every sidebar
  // (mail, calendar, contacts, future extensions). Owns padding, border,
  // font, icon-grid, and crucially the min-height so the strip stays the
  // same rendered size whether the consumer fills 1 or 2 lines of content.
  //
  // Before this primitive existed each consumer rolled its own strip:
  // mail at p-3 + optional 2-line content (~52px), calendar at px-3 py-2
  // single-line (~32px). Switching rail tabs made the bottom band jump.
  // SidebarFooter is the single hook to keep all three (and future
  // extensions) on the same vertical rhythm.
  //
  // Layout: leading on the left (flex-1, min-w-0, items-center so 1-line
  // content centers vertically in the 52px box), trailing on the right
  // (shrink-0). An optional overlay snippet supports mail's absolute-
  // positioned sync-progress bar — anchored by the wrapper's `relative`.

  import { type Snippet } from 'svelte'

  interface Props {
    /** Left-side content: status icon + label, or any consumer-defined
     *  affordance. 1 or 2 lines — the wrapper's min-h handles either. */
    leading: Snippet
    /** Right-side content: cog button, sync action, etc. Wrapped in a
     *  shrink-0 row so it never gets crowded out by long leading labels. */
    trailing: Snippet
    /** Optional absolutely-positioned overlay (top-0, h-1 progress bar
     *  for mail's sync indicator). Renders before the row content so it
     *  sits visually behind. */
    overlay?: Snippet
  }

  let { leading, trailing, overlay }: Props = $props()
</script>

<div
  class="relative p-3 border-t border-border text-xs text-muted-foreground
         flex items-center justify-between gap-2 min-h-[52px]"
>
  {#if overlay}
    {@render overlay()}
  {/if}
  <div class="flex items-center gap-2 min-w-0 flex-1">
    {@render leading()}
  </div>
  <div class="flex items-center gap-1 shrink-0">
    {@render trailing()}
  </div>
</div>
