<script lang="ts">
  import { Dialog as DialogPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import Icon from '@iconify/svelte'
  import type { Snippet } from 'svelte'
  import DialogOverlay from './dialog-overlay.svelte'

  interface Props {
    class?: string
    children?: Snippet
    /** Prevent focus from returning to trigger element on close */
    preventCloseAutoFocus?: boolean
    /** Handler for clicks/touches outside the dialog. Call e.preventDefault()
     *  to prevent the dialog from closing on outside interaction. */
    onInteractOutside?: (e: Event) => void
  }

  let { class: className, children, preventCloseAutoFocus = false, onInteractOutside }: Props = $props()

  function handleCloseAutoFocus(e: Event) {
    if (preventCloseAutoFocus) {
      e.preventDefault()
    }
  }
</script>

<DialogPrimitive.Portal>
  <DialogOverlay />
  <div class="fixed inset-0 z-50 flex items-center justify-center pointer-events-none">
    <DialogPrimitive.Content
      onCloseAutoFocus={handleCloseAutoFocus}
      onInteractOutside={onInteractOutside}
      class={cn(
        'pointer-events-auto grid w-full max-w-lg gap-4 border bg-background p-6 shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 sm:rounded-lg',
        className
      )}
    >
      {#if children}
        {@render children()}
      {/if}
      <DialogPrimitive.Close
        class="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none data-[state=open]:bg-accent data-[state=open]:text-muted-foreground"
      >
        <Icon icon="mdi:close" class="h-4 w-4" />
        <span class="sr-only">Close</span>
      </DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </div>
</DialogPrimitive.Portal>
