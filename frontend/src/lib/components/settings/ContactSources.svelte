<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import { _ } from "$lib/i18n";
  import { contactSourcesStore } from "$lib/stores/contactSources.svelte";
  import { getCurrentDateFnsLocale } from "$lib/stores/settings.svelte";
  import { addToast } from "$lib/stores/toast";
  import Icon from "@iconify/svelte";
  import { formatDistanceToNow } from "date-fns";
  import { onMount } from "svelte";

  // @ts-ignore - wailsjs path
  import type { carddav } from "../../../../wailsjs/go/models";
  import ContactSourceDialog from "./ContactSourceDialog.svelte";

  // Dialog state
  let showAddDialog = $state(false);
  let editingSource = $state<carddav.Source | null>(null);
  let syncingSourceId = $state<string | null>(null);

  // Delete confirmation state
  let showDeleteConfirm = $state(false);
  let deletingSource = $state<carddav.Source | null>(null);
  let isDeleting = $state(false);

  onMount(() => {
    contactSourcesStore.load();
  });

  function formatLastSync(source: carddav.Source): string {
    if (!source.last_synced_at) return $_("contactSource.neverSynced");
    try {
      return $_("contactSource.syncedAgo", {
        values: {
          time: formatDistanceToNow(new Date(source.last_synced_at), {
            addSuffix: true,
            locale: getCurrentDateFnsLocale()
          })
        }
      });
    } catch {
      return $_("contactSource.neverSynced");
    }
  }

  async function handleSync(sourceId: string) {
    syncingSourceId = sourceId;
    try {
      await contactSourcesStore.syncSource(sourceId);
      addToast({ type: "success", message: $_("toast.contactSourceSynced") });
    } catch (err) {
      console.error("Contact source sync failed:", err);
      addToast({ type: "error", message: $_("toast.syncFailed") });
    } finally {
      syncingSourceId = null;
    }
  }

  function handleDelete(source: carddav.Source) {
    deletingSource = source;
    showDeleteConfirm = true;
  }

  async function confirmDelete() {
    if (!deletingSource) return;
    isDeleting = true;
    try {
      await contactSourcesStore.deleteSource(deletingSource.id);
      addToast({ type: "success", message: $_("toast.contactSourceDeleted") });
      showDeleteConfirm = false;
      deletingSource = null;
    } catch (err) {
      console.error("Failed to delete contact source:", err);
      addToast({
        type: "error",
        message: $_("toast.failedToDeleteContactSource")
      });
    } finally {
      isDeleting = false;
    }
  }

  function cancelDelete() {
    showDeleteConfirm = false;
    deletingSource = null;
  }

  function openEdit(source: carddav.Source) {
    editingSource = source;
    showAddDialog = true;
  }

  function openAdd() {
    editingSource = null;
    showAddDialog = true;
  }

  function handleDialogClose() {
    showAddDialog = false;
    editingSource = null;
    contactSourcesStore.refresh();
  }
</script>

<div class="space-y-4">
  <h3 class="text-sm font-medium gap-2 flex items-center">
    <Icon icon="mdi:contacts-outline" class="w-4 h-4" />
    {$_("contactSource.title")}
  </h3>

  {#if contactSourcesStore.loading}
    <div class="py-4 flex items-center justify-center">
      <Icon
        icon="mdi:loading"
        class="w-5 h-5 animate-spin text-muted-foreground"
      />
    </div>
  {:else if contactSourcesStore.sources.length === 0}
    <div class="text-sm text-muted-foreground py-4 text-center">
      <p class="mb-3">{$_("contactSource.noSources")}</p>
      <Button size="sm" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_("contactSource.addSource")}
      </Button>
    </div>
  {:else}
    <div class="space-y-2">
      {#each contactSourcesStore.sources as source (source.id)}
        <div
          class="p-3 border-border rounded-lg space-y-2 border {source.last_error
            ? 'border-destructive/50 bg-destructive/5'
            : ''}"
        >
          <!-- Source header -->
          <div class="flex items-start justify-between">
            <div class="gap-2 flex items-center">
              <Icon
                icon={source.type === "google"
                  ? "mdi:google"
                  : source.type === "microsoft"
                    ? "mdi:microsoft"
                    : "mdi:card-account-details"}
                class="w-5 h-5 {source.enabled
                  ? 'text-primary'
                  : 'text-muted-foreground'}"
              />
              <div>
                <div class="font-medium text-sm">{source.name}</div>
                <div
                  class="text-xs text-muted-foreground gap-1.5 flex items-center"
                >
                  <span class="capitalize">{source.type}</span>
                  {#if source.account_id}
                    <span class="text-muted-foreground/50">·</span>
                    <span class="text-muted-foreground/80"
                      >{$_("contactSource.linked")}</span
                    >
                  {/if}
                </div>
              </div>
            </div>
            <div class="text-xs text-muted-foreground">
              {formatLastSync(source)}
            </div>
          </div>

          <!-- Error display -->
          {#if source.last_error}
            <div
              class="gap-2 p-2 bg-destructive/10 rounded text-sm flex items-start"
            >
              <Icon
                icon="mdi:alert-circle"
                class="w-4 h-4 text-destructive mt-0.5 shrink-0"
              />
              <div class="flex-1">
                <div class="text-destructive font-medium">
                  {$_("contactSource.syncFailed")}
                </div>
                <div class="text-xs text-muted-foreground">
                  {source.last_error}
                </div>
              </div>
            </div>
          {/if}

          <!-- Actions -->
          <div class="gap-2 pt-1 flex items-center">
            <Button
              size="sm"
              variant="ghost"
              onclick={() => handleSync(source.id)}
              disabled={syncingSourceId === source.id}
            >
              {#if syncingSourceId === source.id}
                <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
              {:else}
                <Icon icon="mdi:sync" class="w-4 h-4 mr-1" />
              {/if}
              {source.last_error ? $_("common.retry") : $_("common.sync")}
            </Button>
            <Button size="sm" variant="ghost" onclick={() => openEdit(source)}>
              <Icon icon="mdi:pencil" class="w-4 h-4 mr-1" />
              {$_("common.edit")}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              class="text-destructive hover:text-destructive"
              onclick={() => handleDelete(source)}
            >
              <Icon icon="mdi:delete" class="w-4 h-4 mr-1" />
              {$_("common.delete")}
            </Button>
          </div>
        </div>
      {/each}

      <!-- Add button -->
      <Button size="sm" variant="outline" class="w-full" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_("contactSource.addSource")}
      </Button>
    </div>
  {/if}
</div>

<!-- Add/Edit Dialog -->
<ContactSourceDialog
  bind:open={showAddDialog}
  editSource={editingSource}
  onClose={handleDialogClose}
/>

<!-- Delete Confirmation Dialog -->
<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_("contactSource.deleteTitle")}
  description={$_("contactSource.deleteConfirmName", {
    values: { name: deletingSource?.name || "" }
  })}
  confirmLabel={$_("common.delete")}
  cancelLabel={$_("common.cancel")}
  variant="destructive"
  loading={isDeleting}
  onConfirm={confirmDelete}
  onCancel={cancelDelete}
/>
