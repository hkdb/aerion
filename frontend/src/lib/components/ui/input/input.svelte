<script lang="ts">
  import { cn } from "$lib/utils";
  import type { HTMLInputAttributes } from "svelte/elements";

  interface Props extends Omit<HTMLInputAttributes, "value"> {
    class?: string;
    value?: string | number;
  }

  let {
    class: className,
    value = $bindable(""),
    ...restProps
  }: Props = $props();
</script>

<input
  class={cn(
    "h-10 rounded-md border-input bg-background px-3 py-2 text-sm ring-offset-background file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:ring-ring flex w-full border file:border-0 file:bg-transparent focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
    className
  )}
  bind:value
  {...restProps}
/>
