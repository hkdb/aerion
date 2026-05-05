<script lang="ts">
  import { cn } from "$lib/utils";
  import Icon from "@iconify/svelte";
  import { Select as SelectPrimitive } from "bits-ui";

  interface Props {
    value: string;
    label?: string;
    disabled?: boolean;
    class?: string;
  }

  let { value, label, disabled = false, class: className }: Props = $props();
</script>

<SelectPrimitive.Item
  {value}
  {label}
  {disabled}
  class={cn(
    "rounded-sm py-1.5 pl-8 pr-2 text-sm relative flex w-full cursor-default items-center outline-none select-none",
    "text-popover-foreground",
    "focus:bg-accent focus:text-accent-foreground",
    "data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground",
    "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
    className
  )}
>
  {#snippet children({ selected })}
    <span class="left-2 h-3.5 w-3.5 absolute flex items-center justify-center">
      {#if selected}
        <Icon icon="mdi:check" class="h-4 w-4" />
      {/if}
    </span>
    {label || value}
  {/snippet}
</SelectPrimitive.Item>
