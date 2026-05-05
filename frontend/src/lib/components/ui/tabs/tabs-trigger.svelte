<script lang="ts">
  import { cn } from "$lib/utils";
  import { Tabs as TabsPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    value: string;
    disabled?: boolean;
    class?: string;
    children?: Snippet;
  }

  let {
    value,
    disabled = false,
    class: className,
    children,
    ...restProps
  }: Props = $props();
</script>

<TabsPrimitive.Trigger
  {value}
  {disabled}
  class={cn(
    "rounded-sm px-3 py-1.5 text-sm font-medium ring-offset-background focus-visible:ring-ring data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm inline-flex items-center justify-center whitespace-nowrap transition-all focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50",
    className
  )}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.Trigger>
