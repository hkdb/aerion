<script lang="ts">
  import { cn } from "$lib/utils";
  import { Tabs as TabsPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
  }

  let { class: className, children, ...restProps }: Props = $props();
</script>

<TabsPrimitive.List
  class={cn(
    "h-10 rounded-md bg-muted p-1 text-muted-foreground inline-flex items-center justify-center",
    className
  )}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.List>
