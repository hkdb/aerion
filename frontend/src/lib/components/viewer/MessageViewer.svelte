<script lang="ts">
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";
  import { onDestroy, tick } from "svelte";
  import { fade } from "svelte/transition";

  // @ts-ignore - wailsjs path
  import { FetchMessageBody, GetMessage } from "../../../../wailsjs/go/app/App";
  // @ts-ignore - wailsjs path
  import { message as messageModels } from "../../../../wailsjs/go/models";
  // @ts-ignore - wailsjs runtime
  import { EventsOff, EventsOn } from "../../../../wailsjs/runtime/runtime";
  import AttachmentList from "./AttachmentList.svelte";

  interface Props {
    messageId?: string | null;
  }

  let { messageId = null }: Props = $props();

  // State
  let message = $state<messageModels.Message | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let fetchingBody = $state(false);
  let readyToShow = $state(false);

  // Listen for body fetched events (from background sync)
  let cleanupBodyFetched: (() => void) | null = null;

  $effect(() => {
    // Set up event listener when component mounts
    cleanupBodyFetched = EventsOn(
      "message:bodyFetched",
      (data: { messageId: string }) => {
        // If this is the message we're viewing, reload it
        if (message && data.messageId === message.id) {
          loadMessage(data.messageId);
        }
      }
    );

    return () => {
      if (cleanupBodyFetched) cleanupBodyFetched();
    };
  });

  // Load message when ID changes
  $effect(() => {
    if (messageId) {
      loadMessage(messageId);
    } else {
      message = null;
    }
  });

  async function loadMessage(id: string) {
    loading = true;
    error = null;
    fetchingBody = false;
    readyToShow = false; // Hide content initially to prevent flash

    try {
      const loadedMessage = await GetMessage(id);
      message = loadedMessage;

      // Wait for DOM to settle, then show with transition
      await tick();
      await new Promise((resolve) => requestAnimationFrame(resolve));
      readyToShow = true;

      // If body not fetched, request it on-demand
      // @ts-ignore - bodyFetched exists in generated models
      if (loadedMessage && loadedMessage.bodyFetched === false) {
        fetchBodyOnDemand(id);
      }
    } catch (err) {
      console.error("Failed to load message:", err);
      error = $_("viewer.failedToLoadMessage");
    } finally {
      loading = false;
    }
  }

  // Fetch body on-demand when user views a message without body
  async function fetchBodyOnDemand(id: string) {
    if (fetchingBody) return;

    fetchingBody = true;
    try {
      const updatedMessage = await FetchMessageBody(id);
      // Only update if still viewing same message
      if (message && message.id === id) {
        message = updatedMessage;
      }
    } catch (err) {
      console.error("Failed to fetch message body:", err);
      // Message may have been deleted from server — clear it so UI doesn't stay stuck on loading
      if (message && message.id === id) {
        message = null;
      }
    } finally {
      fetchingBody = false;
    }
  }

  function formatBytes(bytes: number): string {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
  }

  function getFileIcon(type: string): string {
    if (type.includes("pdf")) return "mdi:file-pdf-box";
    if (type.includes("word") || type.includes("document"))
      return "mdi:file-word-box";
    if (type.includes("excel") || type.includes("spreadsheet"))
      return "mdi:file-excel-box";
    if (type.includes("image")) return "mdi:file-image";
    if (type.includes("video")) return "mdi:file-video";
    if (type.includes("audio")) return "mdi:file-music";
    if (type.includes("zip") || type.includes("archive"))
      return "mdi:folder-zip";
    return "mdi:file-outline";
  }

  function getInitials(name: string): string {
    return name
      .split(" ")
      .map((n) => n[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  }

  function formatDate(dateStr: any): string {
    const date = new Date(dateStr);
    return `${date.toLocaleDateString()} at ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  }

  // Parse recipient list (JSON array format from backend)
  function parseRecipients(
    recipientStr: string | undefined
  ): Array<{ name: string; email: string }> {
    if (!recipientStr) return [];
    try {
      // Try parsing as JSON array first (backend format)
      const parsed = JSON.parse(recipientStr);
      if (Array.isArray(parsed)) {
        return parsed.map((r: any) => ({
          name: r.name || "",
          email: r.email || ""
        }));
      }
      return [];
    } catch {
      // Fallback: try "Name <email>, Name2 <email2>" format
      return recipientStr.split(",").map((r) => {
        const match = r.trim().match(/^(.+?)\s*<(.+?)>$/);
        if (match) {
          return { name: match[1].trim(), email: match[2].trim() };
        }
        return { name: "", email: r.trim() };
      });
    }
  }

  // Check if HTML content has a background color set
  function hasBackgroundSet(html: string): boolean {
    if (!html) return false;
    const lowerHtml = html.toLowerCase();
    // Check for background-color or background in style attributes or CSS
    return (
      lowerHtml.includes("background-color") ||
      lowerHtml.includes("background:") ||
      // Check for bgcolor attribute (older HTML emails)
      lowerHtml.includes("bgcolor")
    );
  }

  // Wrap HTML email with consistent styling (rounded corners) and fallback background if needed
  function prepareHtmlEmail(html: string): string {
    if (!html) return "";

    // Always wrap for consistent styling (rounded corners, overflow hidden)
    // Use font-family: inherit to ensure system fonts are used for CJK support
    const baseStyles =
      "border-radius: 0.375rem; overflow: hidden; font-family: inherit;";

    if (hasBackgroundSet(html)) {
      // Email sets its own background, just add rounded corners
      return `<div style="${baseStyles}">${html}</div>`;
    }
    // Add white background fallback for readability in dark mode
    return `<div style="${baseStyles} background-color: white; color: #1a1a1a; padding: 1rem;">${html}</div>`;
  }
</script>

<div class="flex h-full flex-col">
  {#if !messageId}
    <!-- No message selected -->
    <div
      class="text-muted-foreground flex h-full flex-col items-center justify-center"
    >
      <Icon icon="mdi:email-open-outline" class="w-16 h-16 mb-4" />
      <p class="text-lg">{$_("viewer.selectConversation")}</p>
    </div>
  {:else if loading}
    <!-- Loading -->
    <div class="flex h-full items-center justify-center">
      <Icon
        icon="mdi:loading"
        class="w-8 h-8 animate-spin text-muted-foreground"
      />
    </div>
  {:else if error}
    <!-- Error -->
    <div
      class="px-4 flex h-full flex-col items-center justify-center text-center"
    >
      <Icon
        icon="mdi:alert-circle-outline"
        class="w-12 h-12 text-destructive mb-3"
      />
      <p class="text-destructive mb-2">{$_("viewer.failedToLoadMessage")}</p>
      <p class="text-sm text-muted-foreground">{error}</p>
      <button
        class="mt-4 text-sm text-primary hover:underline"
        onclick={() => loadMessage(messageId!)}
      >
        {$_("viewer.tryAgain")}
      </button>
    </div>
  {:else if message && readyToShow}
    {#key message.id}
      <div class="flex h-full flex-col" transition:fade={{ duration: 400 }}>
        <!-- Header with Actions -->
        <div
          class="px-4 py-3 border-border flex items-center justify-between border-b"
        >
          <div class="gap-2 flex items-center">
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.reply")}
            >
              <Icon icon="mdi:reply" class="w-5 h-5 text-muted-foreground" />
            </button>
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.replyAll")}
            >
              <Icon
                icon="mdi:reply-all"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.forward")}
            >
              <Icon icon="mdi:share" class="w-5 h-5 text-muted-foreground" />
            </button>

            <div class="h-5 bg-border mx-1 w-px"></div>

            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.archive")}
            >
              <Icon
                icon="mdi:archive-outline"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.delete")}
            >
              <Icon
                icon="mdi:delete-outline"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.markAsSpam")}
            >
              <Icon
                icon="mdi:alert-octagon-outline"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
          </div>

          <div class="gap-2 flex items-center">
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.markAsUnread")}
            >
              <Icon
                icon="mdi:email-outline"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
            <button
              class="p-2 rounded-md hover:bg-muted transition-colors"
              title={$_("viewer.more")}
            >
              <Icon
                icon="mdi:dots-vertical"
                class="w-5 h-5 text-muted-foreground"
              />
            </button>
          </div>
        </div>

        <!-- Message Content -->
        <div class="scrollbar-thin flex-1 overflow-y-auto">
          <div class="p-6">
            <!-- Subject -->
            <h1 class="text-xl font-semibold text-foreground mb-4">
              {message.subject}
            </h1>

            <!-- Sender Info -->
            <div class="gap-3 mb-6 flex items-start">
              <div
                class="w-10 h-10 bg-primary text-primary-foreground font-medium flex items-center justify-center rounded-full"
              >
                {getInitials(message.fromName || message.fromEmail)}
              </div>
              <div class="flex-1">
                <div class="gap-2 flex items-center">
                  <span class="font-medium text-foreground"
                    >{message.fromName || $_("viewer.unknown")}</span
                  >
                  <span class="text-sm text-muted-foreground"
                    >&lt;{message.fromEmail}&gt;</span
                  >
                </div>
                {#if message.toList}
                  <div class="text-sm text-muted-foreground">
                    {$_("viewer.to")}
                    {parseRecipients(message.toList)
                      .map((t) => t.name || t.email)
                      .join(", ")}
                  </div>
                {/if}
              </div>
              <div class="text-sm text-muted-foreground">
                {formatDate(message.date)}
              </div>
            </div>

            <!-- Body -->
            <div class="prose prose-sm dark:prose-invert mb-6 max-w-none">
              {#if fetchingBody || ((message as any).bodyFetched === false && !message.bodyHtml && !message.bodyText)}
                <!-- Body not yet fetched, show loading state -->
                <div class="py-8 flex flex-col items-center justify-center">
                  <Icon
                    icon="mdi:loading"
                    class="w-6 h-6 animate-spin text-muted-foreground"
                  />
                  <span class="text-sm text-muted-foreground mt-2">
                    {$_("common.loading")}
                  </span>
                </div>
              {:else if message.bodyHtml}
                {@html prepareHtmlEmail(message.bodyHtml)}
              {:else if message.bodyText}
                <pre
                  class="font-sans whitespace-pre-wrap">{message.bodyText}</pre>
              {:else}
                <p class="text-muted-foreground italic">
                  {$_("viewer.noContent")}
                </p>
              {/if}
            </div>

            <!-- Attachments -->
            {#if message.hasAttachments}
              <div class="border-border pt-4 border-t">
                <h3
                  class="text-sm font-medium text-foreground mb-3 gap-2 flex items-center"
                >
                  <Icon icon="mdi:paperclip" class="w-4 h-4" />
                  {$_("viewer.attachments")}
                </h3>
                <AttachmentList messageId={message.id} />
              </div>
            {/if}
          </div>
        </div>
      </div>
    {/key}
  {/if}
</div>
