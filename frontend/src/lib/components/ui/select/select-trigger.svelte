<script lang="ts">
  import { cn } from "$lib/utils";
  import Icon from "@iconify/svelte";
  import { Select as SelectPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
  }

  let { class: className, children }: Props = $props();
</script>

<SelectPrimitive.Trigger
  class={cn(
    "h-10 rounded-md border-input bg-background px-3 py-2 text-sm text-foreground ring-offset-background placeholder:text-muted-foreground focus:ring-ring flex w-full items-center justify-between border focus:ring-2 focus:ring-offset-2 focus:outline-none disabled:cursor-not-allowed disabled:opacity-50",
    className
  )}
>
  {#if children}
    {@render children()}
  {/if}
  <Icon icon="mdi:chevron-down" class="h-4 w-4 opacity-50" />
</SelectPrimitive.Trigger>
