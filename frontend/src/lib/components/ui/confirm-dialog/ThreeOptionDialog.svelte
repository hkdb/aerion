<script lang="ts">
  import * as AlertDialog from "$lib/components/ui/alert-dialog";
  import Icon from "@iconify/svelte";

  interface Props {
    open: boolean; // bindable
    title: string;
    description?: string;
    option1Label?: string; // default: "Option 1" (destructive action)
    option2Label?: string; // default: "Option 2" (primary action)
    option3Label?: string; // default: "Cancel" (cancel action)
    option1Variant?: "default" | "destructive"; // default: 'destructive'
    option2Variant?: "default" | "destructive"; // default: 'default'
    loading?: "option1" | "option2" | null; // which button is loading
    onOption1: () => void;
    onOption2: () => void;
    onOption3?: () => void; // cancel/keep editing
  }

  let {
    open = $bindable(false),
    title,
    description = "",
    option1Label = "Option 1",
    option2Label = "Option 2",
    option3Label = "Cancel",
    option1Variant = "destructive",
    option2Variant = "default",
    loading = null,
    onOption1,
    onOption2,
    onOption3
  }: Props = $props();

  // Button refs for keyboard navigation
  let button1Ref = $state<HTMLButtonElement | null>(null);
  let button2Ref = $state<HTMLButtonElement | null>(null);
  let button3Ref = $state<HTMLButtonElement | null>(null);

  // Handle auto-focus when dialog opens - focus "Save & Close" (button 2)
  function handleOpenAutoFocus(e: Event) {
    e.preventDefault(); // Prevent bits-ui default focus behavior
    // Use requestAnimationFrame to wait for next paint cycle before focusing
    requestAnimationFrame(() => {
      button2Ref?.focus();
    });
  }

  // Handle keyboard navigation between buttons
  function handleKeyDown(e: KeyboardEvent) {
    const buttons = [button1Ref, button2Ref, button3Ref];
    const currentIndex = buttons.findIndex((b) => b === document.activeElement);
    if (currentIndex === -1) return;

    let newIndex = currentIndex;

    if (e.key === "ArrowLeft" || e.key === "h") {
      e.preventDefault();
      newIndex = (currentIndex - 1 + 3) % 3;
    } else if (e.key === "ArrowRight" || e.key === "l") {
      e.preventDefault();
      newIndex = (currentIndex + 1) % 3;
    } else if (e.key === "Tab") {
      e.preventDefault();
      if (e.shiftKey) {
        newIndex = (currentIndex - 1 + 3) % 3;
      } else {
        newIndex = (currentIndex + 1) % 3;
      }
    } else {
      return;
    }

    buttons[newIndex]?.focus();
  }

  function handleOpenChange(isOpen: boolean) {
    open = isOpen;
    if (!isOpen) {
      onOption3?.();
    }
  }

  function handleOption1() {
    onOption1();
  }

  function handleOption2() {
    onOption2();
  }

  function handleOption3() {
    open = false;
    onOption3?.();
  }

  const isLoading = $derived(loading !== null);
</script>

<AlertDialog.Root bind:open onOpenChange={handleOpenChange}>
  <AlertDialog.Content onOpenAutoFocus={handleOpenAutoFocus}>
    <AlertDialog.Header>
      <AlertDialog.Title>{title}</AlertDialog.Title>
      {#if description}
        <AlertDialog.Description>{description}</AlertDialog.Description>
      {/if}
    </AlertDialog.Header>

    <AlertDialog.Footer>
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="gap-2 sm:flex-row flex" onkeydown={handleKeyDown}>
        <!-- Option 1 (e.g., Discard) -->
        <button
          bind:this={button1Ref}
          onclick={handleOption1}
          disabled={isLoading}
          class="rounded-md text-sm font-medium ring-offset-background focus-visible:ring-ring h-10 px-4 py-2 inline-flex items-center justify-center whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50 {option1Variant ===
          'destructive'
            ? 'bg-destructive text-destructive-foreground hover:bg-destructive/90'
            : 'bg-primary text-primary-foreground hover:bg-primary/90'}"
        >
          {#if loading === "option1"}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
          {/if}
          {option1Label}
        </button>

        <!-- Option 2 (e.g., Save & Close) -->
        <button
          bind:this={button2Ref}
          onclick={handleOption2}
          disabled={isLoading}
          class="rounded-md text-sm font-medium ring-offset-background focus-visible:ring-ring h-10 px-4 py-2 inline-flex items-center justify-center whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50 {option2Variant ===
          'destructive'
            ? 'bg-destructive text-destructive-foreground hover:bg-destructive/90'
            : 'bg-primary text-primary-foreground hover:bg-primary/90'}"
        >
          {#if loading === "option2"}
            <Icon icon="mdi:loading" class="w-4 h-4 mr-2 animate-spin" />
          {/if}
          {option2Label}
        </button>

        <!-- Option 3 (e.g., Keep Editing) - styled as cancel -->
        <button
          bind:this={button3Ref}
          onclick={handleOption3}
          disabled={isLoading}
          class="rounded-md text-sm font-medium ring-offset-background focus-visible:ring-ring h-10 px-4 py-2 border-input bg-background hover:bg-accent hover:text-accent-foreground inline-flex items-center justify-center border whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50"
        >
          {option3Label}
        </button>
      </div>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
