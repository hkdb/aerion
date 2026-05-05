<script lang="ts">
  import { cn } from "$lib/utils";
  import { Dialog as DialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
  }

  let { class: className, children }: Props = $props();
</script>

<DialogPrimitive.Description
  class={cn("text-sm text-muted-foreground", className)}
>
  {#if children}
    {@render children()}
  {/if}
</DialogPrimitive.Description>
