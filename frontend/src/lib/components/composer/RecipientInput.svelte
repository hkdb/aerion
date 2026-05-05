<script lang="ts">
  import {
    COMPOSER_API_KEY,
    type ComposerApi,
    createMainWindowApi
  } from "$lib/composerApi";
  import Icon from "@iconify/svelte";
  import { getContext } from "svelte";

  // @ts-ignore - Wails generated imports
  import { contact, smtp } from "../../../../wailsjs/go/models";

  interface Props {
    recipients: smtp.Address[];
    placeholder?: string;
    /** Optional: search contacts function override */
    searchContactsFn?: (
      query: string,
      limit: number
    ) => Promise<contact.Contact[]>;
  }

  let {
    recipients = $bindable([]),
    placeholder = "Add recipients...",
    searchContactsFn
  }: Props = $props();

  // Get API from context or create default
  const contextApi = getContext<ComposerApi | undefined>(COMPOSER_API_KEY);
  const api: ComposerApi = contextApi || createMainWindowApi();

  // Use the prop function or fall back to API (evaluated each call to handle prop changes)
  function doSearchContacts(query: string, limit: number) {
    return searchContactsFn
      ? searchContactsFn(query, limit)
      : api.searchContacts(query, limit);
  }

  // State
  let inputValue = $state("");
  let suggestions = $state<contact.Contact[]>([]);
  let showSuggestions = $state(false);
  let selectedIndex = $state(-1);
  let inputElement: HTMLInputElement;
  let containerElement: HTMLDivElement;
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Search contacts as user types
  async function searchContacts(query: string) {
    if (query.length < 2) {
      suggestions = [];
      showSuggestions = false;
      return;
    }

    try {
      const results = await doSearchContacts(query, 10);
      suggestions = results || [];
      showSuggestions = suggestions.length > 0;
      selectedIndex = -1;
    } catch (err) {
      console.error("Failed to search contacts:", err);
      suggestions = [];
    }
  }

  function handleInput() {
    // Debounce the search
    if (debounceTimer) {
      clearTimeout(debounceTimer);
    }
    debounceTimer = setTimeout(() => {
      searchContacts(inputValue);
    }, 200);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (showSuggestions && selectedIndex < suggestions.length - 1) {
        selectedIndex++;
      }
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (showSuggestions && selectedIndex > 0) {
        selectedIndex--;
      }
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (showSuggestions && selectedIndex >= 0) {
        selectSuggestion(suggestions[selectedIndex]);
      } else if (inputValue.trim()) {
        addRecipient(inputValue.trim());
      }
    } else if (e.key === "Escape") {
      showSuggestions = false;
      selectedIndex = -1;
    } else if (
      e.key === "Backspace" &&
      inputValue === "" &&
      recipients.length > 0
    ) {
      // Remove last recipient
      removeRecipient(recipients.length - 1);
    } else if (e.key === "," || e.key === ";" || e.key === "Tab") {
      if (inputValue.trim()) {
        e.preventDefault();
        addRecipient(inputValue.trim());
      }
    }
  }

  function selectSuggestion(contact: contact.Contact) {
    const address = new smtp.Address({
      name: contact.display_name || "",
      address: contact.email
    });
    recipients = [...recipients, address];
    inputValue = "";
    suggestions = [];
    showSuggestions = false;
    selectedIndex = -1;
    inputElement?.focus();
  }

  function addRecipient(value: string) {
    // Parse email address (handle "Name <email@example.com>" format)
    const emailRegex = /^(?:(.+?)\s*<)?([^\s<>]+@[^\s<>]+)>?$/;
    const match = value.match(emailRegex);

    if (match) {
      const name = match[1]?.trim() || "";
      const email = match[2].toLowerCase();

      // Check if already added (handle both 'address' and 'email' field names)
      if (
        recipients.some(
          (r) => (r.address || (r as any).email || "").toLowerCase() === email
        )
      ) {
        inputValue = "";
        return;
      }

      const address = new smtp.Address({
        name: name,
        address: email
      });
      recipients = [...recipients, address];
      inputValue = "";
      suggestions = [];
      showSuggestions = false;
    }
  }

  function removeRecipient(index: number) {
    recipients = recipients.filter((_, i) => i !== index);
    inputElement?.focus();
  }

  function handleBlur() {
    // Delay hiding to allow click on suggestion
    setTimeout(() => {
      showSuggestions = false;
    }, 200);
  }

  // Allow parent to focus the input programmatically
  export function focus() {
    inputElement?.focus();
  }

  function handleFocus() {
    if (inputValue.length >= 2 && suggestions.length > 0) {
      showSuggestions = true;
    }
  }

  function handlePaste(e: ClipboardEvent) {
    const text = e.clipboardData?.getData("text");
    if (text) {
      // Handle pasted email addresses (comma or semicolon separated)
      const addresses = text
        .split(/[,;]/)
        .map((a) => a.trim())
        .filter(Boolean);
      if (addresses.length > 1) {
        e.preventDefault();
        addresses.forEach(addRecipient);
      }
    }
  }
</script>

<div bind:this={containerElement} class="relative">
  <div class="gap-1 flex flex-wrap items-center">
    <!-- Recipient chips -->
    {#each recipients as recipient, index}
      <div
        class="gap-1 px-2 py-0.5 bg-muted rounded-md text-sm flex items-center"
      >
        <span>
          {#if recipient.name}
            {recipient.name}
          {:else}
            {recipient.address || (recipient as any).email || ""}
          {/if}
        </span>
        <button
          onclick={() => removeRecipient(index)}
          class="text-muted-foreground hover:text-foreground"
          type="button"
        >
          <Icon icon="mdi:close" class="w-3.5 h-3.5" />
        </button>
      </div>
    {/each}

    <!-- Input -->
    <input
      bind:this={inputElement}
      bind:value={inputValue}
      oninput={handleInput}
      onkeydown={handleKeyDown}
      onblur={handleBlur}
      onfocus={handleFocus}
      onpaste={handlePaste}
      type="email"
      {placeholder}
      class="text-sm min-w-[150px] flex-1 bg-transparent focus:outline-none"
    />
  </div>

  <!-- Suggestions dropdown -->
  {#if showSuggestions}
    <div
      class="left-0 right-0 mt-1 bg-popover border-border rounded-md shadow-lg max-h-60 absolute top-full z-50 overflow-auto border"
    >
      {#each suggestions as suggestion, index}
        <button
          onmousedown={() => selectSuggestion(suggestion)}
          class="px-3 py-2 hover:bg-muted gap-3 flex w-full items-center text-left transition-colors {index ===
          selectedIndex
            ? 'bg-muted'
            : ''}"
          type="button"
        >
          <!-- Avatar placeholder -->
          <div
            class="w-8 h-8 bg-primary/10 text-xs font-medium text-primary flex items-center justify-center rounded-full"
          >
            {(suggestion.display_name || suggestion.email)[0].toUpperCase()}
          </div>
          <div class="min-w-0 flex-1">
            <div class="text-sm font-medium truncate">
              {suggestion.display_name || suggestion.email}
            </div>
            {#if suggestion.display_name}
              <div class="text-xs text-muted-foreground truncate">
                {suggestion.email}
              </div>
            {/if}
          </div>
          {#if suggestion.send_count > 0}
            <div class="text-xs text-muted-foreground">
              {suggestion.send_count}x
            </div>
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</div>
