<script lang="ts">
  import * as AlertDialog from "$lib/components/ui/alert-dialog";
  import Icon from "@iconify/svelte";

  interface Props {
    open: boolean; // bindable
    title: string;
    description: string;
    confirmLabel?: string; // default: "Confirm"
    cancelLabel?: string; // default: "Cancel"
    variant?: "default" | "destructive"; // default: 'default'
    loading?: boolean; // show spinner on confirm button
    onConfirm: () => void;
    onCancel?: () => void;
  }

  let {
    open = $bindable(false),
    title,
    description,
    confirmLabel = "Confirm",
    cancelLabel = "Cancel",
    variant = "default",
    loading = false,
    onConfirm,
    onCancel
  }: Props = $props();

  let closedByButton = false;

  function handleOpenChange(isOpen: boolean) {
    open = isOpen;
    if (!isOpen) {
      if (!closedByButton) {
        onCancel?.();
      }
      closedByButton = false;
    }
  }

  function handleConfirm() {
    closedByButton = true;
    open = false;
    onConfirm();
  }

  function handleCancel() {
    closedByButton = true;
    onCancel?.();
    open = false;
  }
</script>

<AlertDialog.Root bind:open onOpenChange={handleOpenChange}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>{title}</AlertDialog.Title>
      {#if description}
        <AlertDialog.Description>{description}</AlertDialog.Description>
      {/if}
    </AlertDialog.Header>

    <AlertDialog.Footer>
      <AlertDialog.Cancel onclick={handleCancel} disabled={loading}>
        {cancelLabel}
      </AlertDialog.Cancel>
      <AlertDialog.Action
        onclick={handleConfirm}
        disabled={loading}
        class={variant === "destructive"
          ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
          : ""}
      >
        {#if loading}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
        {/if}
        {confirmLabel}
      </AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
