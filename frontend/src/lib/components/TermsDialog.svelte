<script lang="ts">
  import { Dialog as DialogPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import { Button } from '$lib/components/ui/button'
  import Switch from '$lib/components/ui/switch/Switch.svelte'
  // @ts-ignore - wailsjs path
  import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
  import { _ } from '$lib/i18n'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'

  interface Props {
    open: boolean
    onAccept: () => void
  }

  let { open = $bindable(false), onAccept }: Props = $props()

  // Hold the dialog guard while open so global shortcuts (Ctrl+, etc.) don't
  // fire behind this modal. Effect cleanup releases it on close and on
  // unmount-while-open (same pattern as ConfirmDialog).
  $effect(() => {
    if (!open) return
    dialogGuardOpen()
    return () => dialogGuardClose()
  })

  let agreed = $state(false)

  const PRIVACY_URL = 'https://github.com/hkdb/aerion/blob/main/docs/PRIVACY.md'
  const TERMS_URL = 'https://github.com/hkdb/aerion/blob/main/docs/TERMS.md'

  function openPrivacyPolicy() {
    BrowserOpenURL(PRIVACY_URL)
  }

  function openTermsOfService() {
    BrowserOpenURL(TERMS_URL)
  }

  function handleAccept() {
    if (agreed) {
      onAccept()
    }
  }

  function preventClose(e: Event) {
    e.preventDefault()
  }
</script>

<DialogPrimitive.Root bind:open>
  <DialogPrimitive.Portal>
    <!-- Overlay - non-interactive (no close on click) -->
    <DialogPrimitive.Overlay
      class="fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
    />

    <!-- Content - no close button -->
    <DialogPrimitive.Content
      preventScroll={false}
      onInteractOutside={preventClose}
      class={cn(
        'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg translate-x-[-50%] translate-y-[-50%] gap-6 border bg-background p-8 shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg'
      )}
    >
      <!-- Header -->
      <div class="flex flex-col space-y-1.5 text-center sm:text-left">
        <h2 class="text-lg font-semibold leading-none tracking-tight">
          {$_('terms.title')}
        </h2>
        <p class="text-sm text-muted-foreground">
          {$_('terms.description')}
        </p>
      </div>

      <!-- Content -->
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground">
          {$_('terms.content')}
        </p>

        <div class="flex flex-col gap-2">
          <button
            type="button"
            onclick={openPrivacyPolicy}
            class="text-sm text-primary hover:underline text-left flex items-center gap-2"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
              <polyline points="15 3 21 3 21 9"/>
              <line x1="10" y1="14" x2="21" y2="3"/>
            </svg>
            {$_('terms.privacyPolicy')}
          </button>
          <button
            type="button"
            onclick={openTermsOfService}
            class="text-sm text-primary hover:underline text-left flex items-center gap-2"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
              <polyline points="15 3 21 3 21 9"/>
              <line x1="10" y1="14" x2="21" y2="3"/>
            </svg>
            {$_('terms.termsOfUse')}
          </button>
        </div>

        <!-- Toggle -->
        <div class="flex items-center gap-3">
          <Switch
            bind:checked={agreed}
            id="agree-terms"
          />
          <label for="agree-terms" class="text-sm cursor-pointer">
            {$_('terms.agreeLabel')}
          </label>
        </div>
      </div>

      <!-- Footer -->
      <div class="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
        <Button onclick={handleAccept} disabled={!agreed}>
          {$_('terms.accept')}
        </Button>
      </div>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
</DialogPrimitive.Root>
