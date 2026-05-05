<script lang="ts">
  import { cn } from "$lib/utils";
  import Icon from "@iconify/svelte";
  import { Dialog as DialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  import DialogOverlay from "./dialog-overlay.svelte";

  interface Props {
    class?: string;
    children?: Snippet;
    /** Prevent focus from returning to trigger element on close */
    preventCloseAutoFocus?: boolean;
  }

  let {
    class: className,
    children,
    preventCloseAutoFocus = false
  }: Props = $props();

  function handleCloseAutoFocus(e: Event) {
    if (preventCloseAutoFocus) {
      e.preventDefault();
    }
  }
</script>

<DialogPrimitive.Portal>
  <DialogOverlay />
  <div
    class="inset-0 pointer-events-none fixed z-50 flex items-center justify-center"
  >
    <DialogPrimitive.Content
      onCloseAutoFocus={handleCloseAutoFocus}
      class={cn(
        "max-w-lg gap-4 bg-background p-6 shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 sm:rounded-lg pointer-events-auto grid w-full border duration-200",
        className
      )}
    >
      {#if children}
        {@render children()}
      {/if}
      <DialogPrimitive.Close
        class="right-4 top-4 rounded-sm ring-offset-background focus:ring-ring data-[state=open]:bg-accent data-[state=open]:text-muted-foreground absolute opacity-70 transition-opacity hover:opacity-100 focus:ring-2 focus:ring-offset-2 focus:outline-none disabled:pointer-events-none"
      >
        <Icon icon="mdi:close" class="h-4 w-4" />
        <span class="sr-only">Close</span>
      </DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </div>
</DialogPrimitive.Portal>
