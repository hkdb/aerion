<script lang="ts">
  import * as AlertDialog from "$lib/components/ui/alert-dialog";
  import { _ } from "$lib/i18n";
  import { accountStore } from "$lib/stores/accounts.svelte";
  import Icon from "@iconify/svelte";

  // @ts-ignore - wailsjs path
  import { account } from "../../../../wailsjs/go/models";

  interface Props {
    /** Whether the dialog is open */
    open?: boolean;
    /** Account to delete */
    account: account.Account | null;
    /** Callback when dialog should close */
    onClose?: () => void;
    /** Callback when account is successfully deleted */
    onSuccess?: () => void;
  }

  let {
    open = $bindable(false),
    account: accountToDelete = null,
    onClose,
    onSuccess
  }: Props = $props();

  let deleting = $state(false);
  let error = $state<string | null>(null);

  async function handleDelete() {
    if (!accountToDelete) return;

    deleting = true;
    error = null;

    try {
      await accountStore.removeAccount(accountToDelete.id);
      onSuccess?.();
      open = false;
      onClose?.();
    } catch (err) {
      console.error("Failed to delete account:", err);
      error = $_("toast.failedToDelete");
    } finally {
      deleting = false;
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
      error = null;
    }
  }
</script>

<AlertDialog.Root bind:open onOpenChange={handleOpenChange}>
  <AlertDialog.Content preventCloseAutoFocus>
    <AlertDialog.Header>
      <AlertDialog.Title>{$_("account.deleteTitle")}</AlertDialog.Title>
      <AlertDialog.Description>
        {$_("account.deleteConfirm", {
          values: {
            name: accountToDelete?.name ?? "",
            email: accountToDelete?.email ?? ""
          }
        })}
        <br /><br />
        {$_("account.deleteWarning")}
      </AlertDialog.Description>
    </AlertDialog.Header>

    {#if error}
      <div
        class="gap-2 p-3 rounded-lg bg-destructive/10 border-destructive/20 flex items-start border"
      >
        <Icon
          icon="mdi:alert-circle"
          class="w-5 h-5 text-destructive mt-0.5 flex-shrink-0"
        />
        <p class="text-sm text-destructive">{error}</p>
      </div>
    {/if}

    <AlertDialog.Footer>
      <AlertDialog.Cancel onclick={handleCancel} disabled={deleting}>
        {$_("common.cancel")}
      </AlertDialog.Cancel>
      <AlertDialog.Action
        onclick={handleDelete}
        disabled={deleting}
        class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
      >
        {#if deleting}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
        {/if}
        {$_("account.deleteAccount")}
      </AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
