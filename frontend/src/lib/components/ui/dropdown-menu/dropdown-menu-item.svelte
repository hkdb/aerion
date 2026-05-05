<script lang="ts">
  import { cn } from "$lib/utils";
  import { DropdownMenu as DropdownMenuPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    inset?: boolean;
    disabled?: boolean;
    children?: Snippet;
    onSelect?: () => void;
  }

  let {
    class: className,
    inset = false,
    disabled = false,
    children,
    onSelect
  }: Props = $props();
</script>

<DropdownMenuPrimitive.Item
  {disabled}
  {onSelect}
  class={cn(
    "rounded-sm px-2 py-1.5 text-sm relative flex cursor-default items-center outline-none select-none",
    "focus:bg-accent focus:text-accent-foreground",
    "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
    inset && "pl-8",
    className
  )}
>
  {#if children}
    {@render children()}
  {/if}
</DropdownMenuPrimitive.Item>
