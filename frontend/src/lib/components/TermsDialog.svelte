<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import Switch from "$lib/components/ui/switch/Switch.svelte";
  import { _ } from "$lib/i18n";
  import { cn } from "$lib/utils";
  import { Dialog as DialogPrimitive } from "bits-ui";

  // @ts-ignore - wailsjs path
  import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";

  interface Props {
    open: boolean;
    onAccept: () => void;
  }

  let { open = $bindable(false), onAccept }: Props = $props();

  let agreed = $state(false);

  const PRIVACY_URL =
    "https://github.com/hkdb/aerion/blob/main/docs/PRIVACY.md";
  const TERMS_URL = "https://github.com/hkdb/aerion/blob/main/docs/TERMS.md";

  function openPrivacyPolicy() {
    BrowserOpenURL(PRIVACY_URL);
  }

  function openTermsOfService() {
    BrowserOpenURL(TERMS_URL);
  }

  function handleAccept() {
    if (agreed) {
      onAccept();
    }
  }

  function preventClose(e: Event) {
    e.preventDefault();
  }
</script>

<DialogPrimitive.Root bind:open>
  <DialogPrimitive.Portal>
    <!-- Overlay - non-interactive (no close on click) -->
    <DialogPrimitive.Overlay
      class="inset-0 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed z-50"
    />

    <!-- Content - no close button -->
    <DialogPrimitive.Content
      onInteractOutside={preventClose}
      class={cn(
        "max-w-lg gap-6 bg-background p-8 shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg fixed top-[50%] left-[50%] z-50 grid w-full translate-x-[-50%] translate-y-[-50%] border duration-200"
      )}
    >
      <!-- Header -->
      <div class="space-y-1.5 sm:text-left flex flex-col text-center">
        <h2 class="text-lg font-semibold tracking-tight leading-none">
          {$_("terms.title")}
        </h2>
        <p class="text-sm text-muted-foreground">
          {$_("terms.description")}
        </p>
      </div>

      <!-- Content -->
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground">
          {$_("terms.content")}
        </p>

        <div class="gap-2 flex flex-col">
          <button
            type="button"
            onclick={openPrivacyPolicy}
            class="text-sm text-primary gap-2 flex items-center text-left hover:underline"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path
                d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"
              />
              <polyline points="15 3 21 3 21 9" />
              <line x1="10" y1="14" x2="21" y2="3" />
            </svg>
            {$_("terms.privacyPolicy")}
          </button>
          <button
            type="button"
            onclick={openTermsOfService}
            class="text-sm text-primary gap-2 flex items-center text-left hover:underline"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path
                d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"
              />
              <polyline points="15 3 21 3 21 9" />
              <line x1="10" y1="14" x2="21" y2="3" />
            </svg>
            {$_("terms.termsOfUse")}
          </button>
        </div>

        <!-- Toggle -->
        <div class="gap-3 flex items-center">
          <Switch bind:checked={agreed} id="agree-terms" />
          <label for="agree-terms" class="text-sm cursor-pointer">
            {$_("terms.agreeLabel")}
          </label>
        </div>
      </div>

      <!-- Footer -->
      <div
        class="sm:flex-row sm:justify-end sm:space-x-2 flex flex-col-reverse"
      >
        <Button onclick={handleAccept} disabled={!agreed}>
          {$_("terms.accept")}
        </Button>
      </div>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
</DialogPrimitive.Root>
