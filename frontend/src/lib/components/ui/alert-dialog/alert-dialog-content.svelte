<script lang="ts">
  import { cn } from "$lib/utils";
  import { AlertDialog as AlertDialogPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
    onOpenAutoFocus?: (e: Event) => void;
    /** Prevent focus from returning to trigger element on close */
    preventCloseAutoFocus?: boolean;
  }

  let {
    class: className,
    children,
    onOpenAutoFocus,
    preventCloseAutoFocus = false
  }: Props = $props();

  function handleCloseAutoFocus(e: Event) {
    if (preventCloseAutoFocus) {
      e.preventDefault();
    }
  }
</script>

<AlertDialogPrimitive.Portal>
  <AlertDialogPrimitive.Overlay
    class="inset-0 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed z-50"
  />
  <div
    class="inset-0 pointer-events-none fixed z-50 flex items-center justify-center"
  >
    <AlertDialogPrimitive.Content
      class={cn(
        "max-w-lg gap-4 bg-background p-6 shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 sm:rounded-lg [&>*]:min-w-0 pointer-events-auto grid w-full border duration-200",
        className
      )}
      {onOpenAutoFocus}
      onCloseAutoFocus={handleCloseAutoFocus}
    >
      {#if children}
        {@render children()}
      {/if}
    </AlertDialogPrimitive.Content>
  </div>
</AlertDialogPrimitive.Portal>
