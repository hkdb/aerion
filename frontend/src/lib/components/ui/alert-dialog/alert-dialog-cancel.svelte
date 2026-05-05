<script lang="ts">
  import { cn } from "$lib/utils";
  import { AlertDialog as AlertDialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
    onclick?: (e: MouseEvent) => void;
    disabled?: boolean;
  }

  let { class: className, children, onclick, disabled }: Props = $props();
</script>

<AlertDialogPrimitive.Cancel
  class={cn(
    "mt-2 h-10 rounded-md border-input bg-background px-4 py-2 text-sm font-medium ring-offset-background hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring sm:mt-0 inline-flex items-center justify-center border transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50",
    className
  )}
  {onclick}
  {disabled}
>
  {#if children}
    {@render children()}
  {/if}
</AlertDialogPrimitive.Cancel>
