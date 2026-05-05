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

<AlertDialogPrimitive.Title class={cn("text-lg font-semibold", className)}>
  {#if children}
    {@render children()}
  {/if}
</AlertDialogPrimitive.Title>
