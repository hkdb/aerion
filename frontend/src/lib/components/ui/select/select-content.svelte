<script lang="ts">
  import { Select as SelectPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import type { Snippet } from 'svelte'

  interface Props {
    class?: string;
    children?: Snippet;
    position?: 'popper' | 'item-aligned';
    side?: 'top' | 'bottom' | 'left' | 'right';
    sideOffset?: number;
    align?: 'start' | 'center' | 'end';
  }

  let {
    class: className,
    children,
    position = 'popper',
    side = 'bottom',
    sideOffset = 4,
    align = 'start',
  }: Props = $props()
</script>

<SelectPrimitive.Portal>
  <SelectPrimitive.Content
    {side}
    {sideOffset}
    {align}
    class={cn(
      'relative z-50 min-w-[8rem] max-h-96 overflow-y-auto scrollbar-thin rounded-md border bg-popover text-popover-foreground shadow-md',
      'data-[state=open]:animate-in data-[state=closed]:animate-out',
      'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
      'data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95',
      'data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2',
      'data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
      position === 'popper' && 'max-h-[min(24rem,var(--bits-select-content-available-height))]',
      className
    )}
  >
    <div class="p-1">
      {#if children}
        {@render children()}
      {/if}
    </div>
  </SelectPrimitive.Content>
</SelectPrimitive.Portal>
