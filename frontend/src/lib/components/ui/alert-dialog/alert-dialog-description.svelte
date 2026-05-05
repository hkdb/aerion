<script lang="ts">
  import { cn } from "$lib/utils";
  import { AlertDialog as AlertDialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
  }

  let { class: className, children }: Props = $props();
</script>

<AlertDialogPrimitive.Description
  class={cn("text-sm text-muted-foreground", className)}
>
  {#if children}
    {@render children()}
  {/if}
</AlertDialogPrimitive.Description>
