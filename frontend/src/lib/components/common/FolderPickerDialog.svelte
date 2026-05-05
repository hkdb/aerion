<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import * as Dialog from "$lib/components/ui/dialog";
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import { folder } from "../../../../wailsjs/go/models";

  interface Props {
    open: boolean;
    title: string;
    foldersLoading: boolean;
    specialFolders: folder.Folder[];
    customFolders: folder.Folder[];
    onSelect: (folderId: string, folderName: string) => void;
  }

  let {
    open = $bindable(false),
    title,
    foldersLoading,
    specialFolders,
    customFolders,
    onSelect
  }: Props = $props();

  let focusedIndex = $state(-1);
  let active = $state(false);
  let listEl: HTMLDivElement | undefined = $state();
  let searchQuery = $state("");
  let searchInput: HTMLInputElement | null = $state(null);

  // Combine and sort all folders by path for hierarchy display
  const allFolders = $derived(
    [...specialFolders, ...customFolders].sort((a, b) =>
      a.path.localeCompare(b.path)
    )
  );

  // Detect the IMAP delimiter from folder paths
  const delimiter = $derived(() => {
    for (const f of allFolders) {
      if (f.path.includes("/")) return "/";
      if (f.path.includes(".")) return ".";
    }
    return "/";
  });

  // Calculate depth for each folder based on path separators
  function getDepth(path: string): number {
    const d = delimiter();
    // Don't count depth for paths like [Gmail]/Sent — treat [Gmail] prefix as depth 0
    const normalized = path.replace(/^\[.*?\]\//, "");
    if (!normalized.includes(d)) return 0;
    return normalized.split(d).length - 1;
  }

  // Format path for search results (readable breadcrumb)
  function formatPath(path: string): string {
    const d = delimiter();
    return path
      .replace(/\[.*?\]\//g, "")
      .split(d)
      .join(" / ");
  }

  // Filtered folders when searching
  const isSearching = $derived(searchQuery.trim().length > 0);
  const displayFolders = $derived(() => {
    if (!isSearching) return allFolders;
    const query = searchQuery.trim().toLowerCase();
    return allFolders.filter(
      (f) =>
        f.name.toLowerCase().includes(query) ||
        f.path.toLowerCase().includes(query)
    );
  });

  // Reset state when dialog opens/closes
  $effect(() => {
    if (!open) {
      active = false;
      searchQuery = "";
      return;
    }
    const folders = displayFolders();
    focusedIndex = folders.length > 0 ? 0 : -1;
    active = false;
    const timer = setTimeout(() => {
      active = true;
      searchInput?.focus();
    }, 0);
    return () => clearTimeout(timer);
  });

  // Reset focus when search changes
  $effect(() => {
    // Track searchQuery to re-run
    searchQuery;
    const folders = displayFolders();
    focusedIndex = folders.length > 0 ? 0 : -1;
  });

  // Scroll focused item into view
  $effect(() => {
    if (focusedIndex < 0 || !listEl) return;
    const buttons = listEl.querySelectorAll("button");
    buttons[focusedIndex]?.scrollIntoView({ block: "nearest" });
  });

  const folderIcons: Record<string, string> = {
    inbox: "mdi:inbox",
    sent: "mdi:send",
    drafts: "mdi:file-document-edit-outline",
    trash: "mdi:delete-outline",
    archive: "mdi:archive-outline",
    spam: "mdi:alert-octagon-outline",
    all: "mdi:email-multiple-outline",
    starred: "mdi:star-outline",
    folder: "mdi:folder-outline"
  };

  function getFolderIcon(type: string): string {
    return folderIcons[type] || folderIcons.folder;
  }

  function handleKeydown(e: KeyboardEvent) {
    if (!active) return;
    const folders = displayFolders();
    if (folders.length === 0) return;

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        e.stopPropagation();
        focusedIndex = (focusedIndex + 1) % folders.length;
        break;
      case "ArrowUp":
        e.preventDefault();
        e.stopPropagation();
        focusedIndex = (focusedIndex - 1 + folders.length) % folders.length;
        break;
      case "Enter":
        e.preventDefault();
        e.stopPropagation();
        if (focusedIndex >= 0 && focusedIndex < folders.length) {
          const f = folders[focusedIndex];
          onSelect(f.id, f.name);
        }
        break;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>{title}</Dialog.Title>
    </Dialog.Header>

    <!-- Search input -->
    <div class="relative">
      <Icon
        icon="mdi:magnify"
        class="left-3 h-4 w-4 text-muted-foreground absolute top-1/2 -translate-y-1/2"
      />
      <input
        bind:this={searchInput}
        bind:value={searchQuery}
        placeholder={$_("contextMenu.searchFolders")}
        class="h-10 rounded-md border-input bg-background pl-9 pr-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex w-full border focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none"
      />
    </div>

    <div
      class="border-border rounded-md max-h-64 overflow-y-auto border"
      bind:this={listEl}
      role="listbox"
    >
      {#if foldersLoading}
        <div class="gap-2 p-3 text-muted-foreground text-sm flex items-center">
          <Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />
          {$_("common.loading")}
        </div>
      {:else if displayFolders().length === 0}
        <div class="p-3 text-sm text-muted-foreground">
          {$_("contextMenu.noFoldersAvailable")}
        </div>
      {:else}
        {#each displayFolders() as f, i (f.id)}
          {@const depth = isSearching ? 0 : getDepth(f.path)}
          <button
            type="button"
            role="option"
            aria-selected={i === focusedIndex}
            class="gap-2 py-2 pr-3 text-sm hover:bg-muted/50 flex w-full items-center text-left transition-colors {i ===
            focusedIndex
              ? 'bg-muted/50'
              : ''}"
            style="padding-left: {12 + depth * 16}px"
            onclick={() => onSelect(f.id, f.name)}
          >
            <Icon icon={getFolderIcon(f.type)} class="h-4 w-4 shrink-0" />
            {#if isSearching}
              <span class="truncate">{formatPath(f.path)}</span>
            {:else}
              <span class="truncate">{f.name}</span>
            {/if}
          </button>
        {/each}
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="destructive" onclick={() => (open = false)}>
        {$_("common.cancel")}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
