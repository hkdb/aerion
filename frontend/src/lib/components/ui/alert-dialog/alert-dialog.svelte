<script lang="ts">
  import { AlertDialog as AlertDialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    children?: Snippet;
  }

  let { open = $bindable(false), onOpenChange, children }: Props = $props();
</script>

<AlertDialogPrimitive.Root bind:open {onOpenChange}>
  {#if children}
    {@render children()}
  {/if}
</AlertDialogPrimitive.Root>
