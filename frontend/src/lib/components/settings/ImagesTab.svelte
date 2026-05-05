<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import ConfirmDialog from "$lib/components/ui/confirm-dialog/ConfirmDialog.svelte";
  import { Label } from "$lib/components/ui/label";
  import Switch from "$lib/components/ui/switch/Switch.svelte";
  import { _ } from "$lib/i18n";
  import { refreshImageAllowlist } from "$lib/stores/imageAllowlist.svelte";
  import { addToast } from "$lib/stores/toast";
  import Icon from "@iconify/svelte";
  import { onMount } from "svelte";

  // @ts-ignore - wailsjs path
  import {
    GetImageAllowlist,
    RemoveImageAllowlist
  } from "../../../../wailsjs/go/app/App";
  // @ts-ignore - wailsjs path
  import { settings } from "../../../../wailsjs/go/models";

  interface Props {
    alwaysLoadImages: boolean;
    onAlwaysLoadImagesChange: (value: boolean) => void;
  }

  let { alwaysLoadImages = $bindable(), onAlwaysLoadImagesChange }: Props =
    $props();

  // State
  let entries = $state<settings.AllowlistEntry[]>([]);
  let loading = $state(true);
  let addressesCollapsed = $state(false);
  let domainsCollapsed = $state(false);
  let showAlwaysLoadImagesConfirm = $state(false);

  // Derived
  let addresses = $derived(entries.filter((e) => e.type === "sender"));
  let domains = $derived(entries.filter((e) => e.type === "domain"));

  function handleAlwaysLoadImagesChange(value: boolean) {
    if (value) {
      showAlwaysLoadImagesConfirm = true;
      return;
    }
    alwaysLoadImages = false;
    onAlwaysLoadImagesChange?.(false);
  }

  async function loadData() {
    try {
      entries = (await GetImageAllowlist()) ?? [];
    } catch (err) {
      console.error("Failed to load image allowlist:", err);
    } finally {
      loading = false;
    }
  }

  async function handleRemove(id: number) {
    try {
      await RemoveImageAllowlist(id);
      await loadData();
      refreshImageAllowlist();
      addToast({
        type: "success",
        message: $_("images.removed")
      });
    } catch (err) {
      console.error("Failed to remove allowlist entry:", err);
    }
  }

  onMount(() => {
    loadData();
  });
</script>

{#if loading}
  <div class="py-8 flex items-center justify-center">
    <Icon
      icon="mdi:loading"
      class="w-6 h-6 animate-spin text-muted-foreground"
    />
  </div>
{:else}
  <div class="space-y-6">
    <!-- Always Load Remote Images Toggle -->
    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label for="always-load-images"
            >{$_("settingsGeneral.alwaysLoadImages")}</Label
          >
          <p class="text-xs text-muted-foreground">
            {$_("settingsGeneral.alwaysLoadImagesHelp")}
          </p>
        </div>
        <Switch
          id="always-load-images"
          bind:checked={alwaysLoadImages}
          onCheckedChange={handleAlwaysLoadImagesChange}
        />
      </div>
    </div>

    <!-- Divider -->
    <div class="border-border border-t"></div>

    <!-- Addresses Section -->
    <div class="space-y-3">
      <button
        class="gap-2 text-sm font-semibold text-foreground hover:text-primary flex w-full items-center text-left transition-colors"
        onclick={() => (addressesCollapsed = !addressesCollapsed)}
      >
        <Icon
          icon={addressesCollapsed ? "mdi:chevron-right" : "mdi:chevron-down"}
          class="w-4 h-4 flex-shrink-0"
        />
        <Icon icon="mdi:email-outline" class="w-4 h-4" />
        {$_("images.addresses")}
        <span
          class="px-1.5 py-0.5 rounded bg-muted text-muted-foreground font-medium text-[10px]"
          >{addresses.length}</span
        >
      </button>

      {#if !addressesCollapsed}
        {#if addresses.length === 0}
          <p class="text-sm text-muted-foreground ml-6">
            {$_("images.noAddresses")}
          </p>
        {:else}
          <div class="space-y-1.5 max-h-48 ml-6 overflow-y-auto">
            {#each addresses as entry (entry.id)}
              <div
                class="gap-3 p-2 rounded-md border-border flex items-center border"
              >
                <Icon
                  icon="mdi:email-outline"
                  class="w-4 h-4 text-muted-foreground flex-shrink-0"
                />
                <span class="text-sm flex-1 truncate">{entry.value}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onclick={() => handleRemove(entry.id)}
                  title={$_("images.removeButton")}
                >
                  <Icon icon="mdi:close" class="w-3.5 h-3.5" />
                </Button>
              </div>
            {/each}
          </div>
        {/if}
      {/if}
    </div>

    <!-- Domains Section -->
    <div class="space-y-3">
      <button
        class="gap-2 text-sm font-semibold text-foreground hover:text-primary flex w-full items-center text-left transition-colors"
        onclick={() => (domainsCollapsed = !domainsCollapsed)}
      >
        <Icon
          icon={domainsCollapsed ? "mdi:chevron-right" : "mdi:chevron-down"}
          class="w-4 h-4 flex-shrink-0"
        />
        <Icon icon="mdi:web" class="w-4 h-4" />
        {$_("images.domains")}
        <span
          class="px-1.5 py-0.5 rounded bg-muted text-muted-foreground font-medium text-[10px]"
          >{domains.length}</span
        >
      </button>

      {#if !domainsCollapsed}
        {#if domains.length === 0}
          <p class="text-sm text-muted-foreground ml-6">
            {$_("images.noDomains")}
          </p>
        {:else}
          <div class="space-y-1.5 max-h-48 ml-6 overflow-y-auto">
            {#each domains as entry (entry.id)}
              <div
                class="gap-3 p-2 rounded-md border-border flex items-center border"
              >
                <Icon
                  icon="mdi:web"
                  class="w-4 h-4 text-muted-foreground flex-shrink-0"
                />
                <span class="text-sm flex-1 truncate">{entry.value}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onclick={() => handleRemove(entry.id)}
                  title={$_("images.removeButton")}
                >
                  <Icon icon="mdi:close" class="w-3.5 h-3.5" />
                </Button>
              </div>
            {/each}
          </div>
        {/if}
      {/if}
    </div>
  </div>
{/if}

<ConfirmDialog
  bind:open={showAlwaysLoadImagesConfirm}
  title={$_("settingsGeneral.alwaysLoadImagesWarningTitle")}
  description={$_("settingsGeneral.alwaysLoadImagesWarningDescription")}
  confirmLabel={$_("settingsGeneral.disable")}
  variant="destructive"
  onConfirm={() => {
    onAlwaysLoadImagesChange?.(true);
  }}
  onCancel={() => {
    alwaysLoadImages = false;
  }}
/>
