<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { _ } from "$lib/i18n";
  import { cn } from "$lib/utils";
  import Icon from "@iconify/svelte";
  import { Dialog as DialogPrimitive } from "bits-ui";

  // @ts-ignore - wailsjs path
  import type { certificate } from "../../../../wailsjs/go/models";

  interface Props {
    open: boolean;
    certificate: certificate.CertificateInfo | null;
    onAcceptOnce: () => void;
    onAcceptPermanently: () => void;
    onDecline: () => void;
  }

  let {
    open = $bindable(false),
    certificate: cert,
    onAcceptOnce,
    onAcceptPermanently,
    onDecline
  }: Props = $props();

  function preventClose(e: Event) {
    e.preventDefault();
  }

  function formatFingerprint(fp: string): string {
    if (!fp) return "";
    const parts: string[] = [];
    for (let i = 0; i < fp.length; i += 2) {
      parts.push(fp.substring(i, i + 2).toUpperCase());
    }
    return parts.join(":");
  }

  function formatDate(iso: string): string {
    if (!iso) return "N/A";
    try {
      return new Date(iso).toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric"
      });
    } catch {
      return iso;
    }
  }
</script>

<DialogPrimitive.Root bind:open>
  <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay
      class="inset-0 bg-black/50 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed z-50"
    />
    <DialogPrimitive.Content
      class="max-w-lg bg-background shadow-lg sm:rounded-lg p-0 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] fixed top-[50%] left-[50%] z-50 w-full translate-x-[-50%] translate-y-[-50%] border"
      onInteractOutside={preventClose}
    >
      {#if cert}
        <!-- Header -->
        <div class="gap-3 px-6 pt-6 pb-4 flex items-center">
          <div
            class="w-10 h-10 bg-yellow-500/10 flex items-center justify-center rounded-full"
          >
            <Icon
              icon="mdi:shield-alert-outline"
              class="w-6 h-6 text-yellow-500"
            />
          </div>
          <div>
            <h2 class="text-lg font-semibold">{$_("certificate.title")}</h2>
            <p class="text-sm text-muted-foreground">
              {$_("certificate.description")}
            </p>
          </div>
        </div>

        <!-- Certificate Details -->
        <div class="px-6 pb-4 space-y-3">
          <div class="rounded-lg bg-muted/30 p-4 space-y-2.5 text-sm border">
            <div class="gap-1 grid grid-cols-[100px_1fr]">
              <span class="text-muted-foreground"
                >{$_("certificate.subject")}</span
              >
              <span class="font-mono text-xs break-all">{cert.subject}</span>
            </div>
            <div class="gap-1 grid grid-cols-[100px_1fr]">
              <span class="text-muted-foreground"
                >{$_("certificate.issuer")}</span
              >
              <span class="font-mono text-xs break-all">{cert.issuer}</span>
            </div>
            <div class="gap-1 grid grid-cols-[100px_1fr]">
              <span class="text-muted-foreground"
                >{$_("certificate.fingerprint")}</span
              >
              <span class="font-mono text-xs break-all select-all"
                >{formatFingerprint(cert.fingerprint)}</span
              >
            </div>
            <div class="gap-1 grid grid-cols-[100px_1fr]">
              <span class="text-muted-foreground"
                >{$_("certificate.validPeriod")}</span
              >
              <span
                class={cn(
                  "text-xs",
                  cert.isExpired && "text-destructive font-medium"
                )}
              >
                {formatDate(cert.notBefore)}
                {$_("certificate.to")}
                {formatDate(cert.notAfter)}
                {#if cert.isExpired}
                  {$_("certificate.expired")}
                {/if}
              </span>
            </div>
            {#if cert.dnsNames && cert.dnsNames.length > 0}
              <div class="gap-1 grid grid-cols-[100px_1fr]">
                <span class="text-muted-foreground"
                  >{$_("certificate.dnsNames")}</span
                >
                <span class="font-mono text-xs break-all"
                  >{cert.dnsNames.join(", ")}</span
                >
              </div>
            {/if}
            <div class="gap-1 grid grid-cols-[100px_1fr]">
              <span class="text-muted-foreground"
                >{$_("certificate.reason")}</span
              >
              <span class="text-xs text-yellow-600 dark:text-yellow-400"
                >{cert.errorReason}</span
              >
            </div>
          </div>
        </div>

        <!-- Actions -->
        <div class="gap-2 px-6 pb-6 flex items-center justify-end">
          <Button variant="outline" onclick={onDecline}>
            {$_("certificate.decline")}
          </Button>
          <Button variant="outline" onclick={onAcceptOnce}>
            {$_("certificate.acceptOnce")}
          </Button>
          <Button onclick={onAcceptPermanently}>
            {$_("certificate.acceptAlways")}
          </Button>
        </div>
      {/if}
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
</DialogPrimitive.Root>
