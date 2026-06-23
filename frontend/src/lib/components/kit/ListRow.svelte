<script lang="ts">
  // ListRow — generic horizontal row with hover/selected/density styling.
  //
  // Rendered as a non-focusable <div role="option"> rather than a <button>
  // so DOM focus stays on the parent ListPane container during keyboard
  // navigation. If rows were buttons, clicking one would move DOM focus to
  // that button, then arrow keys would leave the previous button visually
  // focused (with a residual outline) even after selection moved.

  import type { Snippet } from 'svelte'

  type Density = 'micro' | 'compact' | 'standard' | 'large'

  interface Props {
    selected?: boolean
    density?: Density
    onclick?: (e: MouseEvent) => void
    children: Snippet
  }

  const { selected = false, density = 'standard', onclick, children }: Props = $props()

  // Padding + gap values come straight from mail's ConversationRow.densityClasses.row
  // (frontend/src/lib/components/list/ConversationRow.svelte:73–78). Keep them
  // 1-for-1 with mail so a future mail-adopts-kit refactor is invisible.
  const PADDING: Record<Density, string> = {
    micro:    'px-3 py-2 gap-2',
    compact:  'px-4 py-3 gap-3',
    standard: 'px-5 py-4 gap-4',
    large:    'px-6 py-5 gap-5',
  }

  function handleClick(e: MouseEvent) {
    onclick?.(e)
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_interactive_supports_focus -->
<div
  role="option"
  aria-selected={selected}
  class="flex items-center w-full {PADDING[density]} border-b border-border text-left transition-colors cursor-pointer select-none {selected
    ? 'bg-primary/20'
    : 'hover:bg-muted/50'}"
  onclick={handleClick}
>
  {@render children()}
</div>
