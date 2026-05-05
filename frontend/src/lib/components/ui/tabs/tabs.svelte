<script lang="ts">
  import { cn } from "$lib/utils";
  import { Tabs as TabsPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    value?: string;
    onValueChange?: (value: string) => void;
    class?: string;
    children?: Snippet;
  }

  let {
    value = $bindable(),
    onValueChange,
    class: className,
    children,
    ...restProps
  }: Props = $props();
</script>

<TabsPrimitive.Root
  bind:value
  {onValueChange}
  class={cn("w-full", className)}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.Root>
