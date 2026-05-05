<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import * as Dialog from "$lib/components/ui/dialog";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import { account } from "../../../../../wailsjs/go/models";
  import SignatureEditor from "./SignatureEditor.svelte";

  interface Props {
    /** Whether the dialog is open */
    open?: boolean;
    /** Identity to edit (null for new identity) */
    identity?: account.Identity | null;
    /** Account ID (required for creating new identity) */
    accountId: string;
    /** Callback when dialog should close */
    onClose?: () => void;
    /** Callback when identity is saved */
    onSave?: (config: account.IdentityConfig) => Promise<void>;
  }

  let {
    open = $bindable(false),
    identity = null,
    accountId,
    onClose,
    onSave
  }: Props = $props();

  // Form state
  let email = $state("");
  let name = $state("");
  let signatureHtml = $state("");
  let signatureText = $state("");
  let signatureEnabled = $state(true);
  let signatureForNew = $state(true);
  let signatureForReply = $state(true);
  let signatureForForward = $state(true);
  let signaturePlacement = $state<"above" | "below">("above");
  let signatureSeparator = $state(false);

  let saving = $state(false);
  let errors = $state<Record<string, string>>({});

  // Initialize form when identity changes
  $effect(() => {
    if (open) {
      if (identity) {
        // Editing existing identity
        email = identity.email || "";
        name = identity.name || "";
        signatureHtml = identity.signatureHtml || "";
        signatureText = identity.signatureText || "";
        signatureEnabled = identity.signatureEnabled ?? true;
        signatureForNew = identity.signatureForNew ?? true;
        signatureForReply = identity.signatureForReply ?? true;
        signatureForForward = identity.signatureForForward ?? true;
        signaturePlacement =
          (identity.signaturePlacement as "above" | "below") || "above";
        signatureSeparator = identity.signatureSeparator ?? false;
      } else {
        // New identity - reset to defaults
        email = "";
        name = "";
        signatureHtml = "";
        signatureText = "";
        signatureEnabled = true;
        signatureForNew = true;
        signatureForReply = true;
        signatureForForward = true;
        signaturePlacement = "above";
        signatureSeparator = false;
      }
      errors = {};
    }
  });

  function validate(): boolean {
    errors = {};

    if (!email.trim()) {
      errors.email = $_("identity.emailRequired");
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      errors.email = $_("identity.invalidEmailFormat");
    }

    if (!name.trim()) {
      errors.name = $_("identity.displayNameRequired");
    }

    return Object.keys(errors).length === 0;
  }

  async function handleSave() {
    if (!validate()) return;

    saving = true;
    try {
      const config = new account.IdentityConfig({
        email: email.trim(),
        name: name.trim(),
        signatureHtml,
        signatureText,
        signatureEnabled,
        signatureForNew,
        signatureForReply,
        signatureForForward,
        signaturePlacement,
        signatureSeparator
      });

      await onSave?.(config);
      open = false;
      onClose?.();
    } catch (err) {
      console.error("Failed to save identity:", err);
      errors.general = $_("identity.saveFailed");
    } finally {
      saving = false;
    }
  }

  function handleCancel() {
    open = false;
    onClose?.();
  }

  function handleOpenChange(isOpen: boolean) {
    open = isOpen;
    if (!isOpen) {
      onClose?.();
    }
  }

  // Convert HTML to plain text for the "Generate from HTML" button
  function generatePlainTextFromHtml() {
    if (!signatureHtml) return;

    const temp = document.createElement("div");
    temp.innerHTML = signatureHtml;

    // Replace <br> and block elements with newlines
    const blockElements = temp.querySelectorAll("p, div, br, li");
    blockElements.forEach((el) => {
      if (el.tagName === "BR") {
        el.replaceWith("\n");
      } else if (el.tagName === "LI") {
        el.prepend(document.createTextNode("- "));
        el.append(document.createTextNode("\n"));
      } else {
        el.append(document.createTextNode("\n"));
      }
    });

    let text = temp.textContent || "";
    text = text.replace(/\n{3,}/g, "\n\n");
    signatureText = text.trim();
  }
</script>

<Dialog.Root bind:open onOpenChange={handleOpenChange}>
  <Dialog.Content class="max-w-2xl max-h-[90vh] overflow-y-auto">
    <Dialog.Header>
      <Dialog.Title>
        {identity
          ? $_("identity.editEmailTitle")
          : $_("identity.addEmailTitle")}
      </Dialog.Title>
      <Dialog.Description>
        {identity
          ? $_("identity.editEmailDescription")
          : $_("identity.addEmailDescription")}
      </Dialog.Description>
    </Dialog.Header>

    <form
      onsubmit={(e) => {
        e.preventDefault();
        handleSave();
      }}
      class="space-y-6"
    >
      <!-- Email & Name -->
      <div class="space-y-4">
        <div class="space-y-2">
          <Label for="email">{$_("identity.emailAddressLabel")}</Label>
          <Input
            id="email"
            type="email"
            placeholder="you@example.com"
            bind:value={email}
            class={errors.email ? "border-destructive" : ""}
          />
          {#if errors.email}
            <p class="text-sm text-destructive">{errors.email}</p>
          {/if}
        </div>

        <div class="space-y-2">
          <Label for="name">{$_("identity.displayNameLabel")}</Label>
          <Input
            id="name"
            type="text"
            placeholder="John Smith"
            bind:value={name}
            class={errors.name ? "border-destructive" : ""}
          />
          <p class="text-xs text-muted-foreground">
            {$_("identity.displayNameHelp")}
          </p>
          {#if errors.name}
            <p class="text-sm text-destructive">{errors.name}</p>
          {/if}
        </div>
      </div>

      <!-- Divider -->
      <div class="border-border border-t"></div>

      <!-- Signature Section -->
      <div class="space-y-4">
        <div class="gap-2 flex items-center">
          <input
            type="checkbox"
            id="signatureEnabled"
            bind:checked={signatureEnabled}
            class="w-4 h-4 rounded border-input accent-primary"
          />
          <Label for="signatureEnabled" class="font-medium cursor-pointer">
            {$_("identity.useSignature")}
          </Label>
        </div>

        {#if signatureEnabled}
          <!-- HTML Signature Editor -->
          <div class="space-y-2">
            <Label>{$_("identity.htmlSignature")}</Label>
            <SignatureEditor
              value={signatureHtml}
              placeholder="Enter your signature..."
              onchange={(html) => (signatureHtml = html)}
            />
            <p class="text-xs text-muted-foreground">
              {$_("identity.signatureHelp")}
            </p>
          </div>

          <!-- Plain Text Signature -->
          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <Label for="signatureText"
                >{$_("identity.plainTextSignature")}</Label
              >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onclick={generatePlainTextFromHtml}
                disabled={!signatureHtml}
                class="text-xs h-7"
              >
                {$_("identity.generateFromHtml")}
              </Button>
            </div>
            <textarea
              id="signatureText"
              bind:value={signatureText}
              placeholder="Plain text version for text-only emails..."
              class="p-3 text-sm bg-background border-input rounded-md focus:ring-ring font-mono min-h-[80px] w-full resize-y border focus:ring-2 focus:outline-none"
            ></textarea>
            <p class="text-xs text-muted-foreground">
              {$_("identity.plainTextHelp")}
            </p>
          </div>

          <!-- Divider -->
          <div class="border-border border-t"></div>

          <!-- Signature Behavior -->
          <div class="space-y-4">
            <Label class="font-medium">{$_("identity.appendSignatureTo")}</Label
            >
            <div class="gap-4 flex flex-wrap">
              <label class="gap-2 flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  bind:checked={signatureForNew}
                  class="w-4 h-4 rounded border-input accent-primary"
                />
                <span class="text-sm">{$_("identity.newMessages")}</span>
              </label>
              <label class="gap-2 flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  bind:checked={signatureForReply}
                  class="w-4 h-4 rounded border-input accent-primary"
                />
                <span class="text-sm">{$_("identity.replies")}</span>
              </label>
              <label class="gap-2 flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  bind:checked={signatureForForward}
                  class="w-4 h-4 rounded border-input accent-primary"
                />
                <span class="text-sm">{$_("identity.forwards")}</span>
              </label>
            </div>
          </div>

          <!-- Signature Placement -->
          <div class="space-y-3">
            <Label class="font-medium"
              >{$_("identity.signaturePlacementLabel")}</Label
            >
            <div class="gap-4 flex">
              <label class="gap-2 flex cursor-pointer items-center">
                <input
                  type="radio"
                  name="placement"
                  value="above"
                  bind:group={signaturePlacement}
                  class="w-4 h-4 accent-primary"
                />
                <span class="text-sm">{$_("identity.aboveQuotedText")}</span>
              </label>
              <label class="gap-2 flex cursor-pointer items-center">
                <input
                  type="radio"
                  name="placement"
                  value="below"
                  bind:group={signaturePlacement}
                  class="w-4 h-4 accent-primary"
                />
                <span class="text-sm">{$_("identity.belowQuotedText")}</span>
              </label>
            </div>
          </div>

          <!-- Separator Option -->
          <div class="gap-2 flex items-center">
            <input
              type="checkbox"
              id="signatureSeparator"
              bind:checked={signatureSeparator}
              class="w-4 h-4 rounded border-input accent-primary"
            />
            <Label for="signatureSeparator" class="text-sm cursor-pointer">
              {$_("identity.addSeparator")} (<code
                class="text-xs bg-muted px-1 rounded"
                >--
              </code>)
            </Label>
          </div>
        {/if}
      </div>

      <!-- Error message -->
      {#if errors.general}
        <div
          class="gap-2 p-3 rounded-lg bg-destructive/10 border-destructive/20 flex items-start border"
        >
          <Icon
            icon="mdi:alert-circle"
            class="w-5 h-5 text-destructive mt-0.5 flex-shrink-0"
          />
          <p class="text-sm text-destructive">{errors.general}</p>
        </div>
      {/if}

      <!-- Actions -->
      <div
        class="gap-2 pt-4 border-border flex items-center justify-end border-t"
      >
        <Button
          type="button"
          variant="ghost"
          onclick={handleCancel}
          disabled={saving}
        >
          {$_("common.cancel")}
        </Button>
        <Button type="submit" disabled={saving}>
          {#if saving}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
          {/if}
          {identity
            ? $_("identity.saveIdentityChanges")
            : $_("identity.addEmailAddressButton")}
        </Button>
      </div>
    </form>
  </Dialog.Content>
</Dialog.Root>
