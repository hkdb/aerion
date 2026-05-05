<script lang="ts">
  import { cn } from "$lib/utils";
  import { Tabs as TabsPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    value: string;
    class?: string;
    children?: Snippet;
  }

  let { value, class: className, children, ...restProps }: Props = $props();
</script>

<TabsPrimitive.Content
  {value}
  class={cn(
    "mt-2 ring-offset-background focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none",
    className
  )}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.Content>
