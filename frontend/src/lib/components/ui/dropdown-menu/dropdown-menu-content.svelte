<script lang="ts">
  import { cn } from "$lib/utils";
  import { DropdownMenu as DropdownMenuPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
    side?: "top" | "bottom" | "left" | "right";
    align?: "start" | "center" | "end";
    sideOffset?: number;
  }

  let {
    class: className,
    children,
    side = "bottom",
    align = "start",
    sideOffset = 4
  }: Props = $props();
</script>

<DropdownMenuPrimitive.Portal>
  <DropdownMenuPrimitive.Content
    {side}
    {align}
    {sideOffset}
    class={cn(
      "rounded-md bg-popover p-1 text-popover-foreground shadow-md z-50 min-w-[160px] border",
      "data-[state=open]:animate-in data-[state=closed]:animate-out",
      "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
      "data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
      "data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2",
      "data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
      className
    )}
  >
    {#if children}
      {@render children()}
    {/if}
  </DropdownMenuPrimitive.Content>
</DropdownMenuPrimitive.Portal>
