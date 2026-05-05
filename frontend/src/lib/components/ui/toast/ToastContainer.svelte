<script lang="ts">
  import { toasts } from "$lib/stores/toast";

  import Toast from "./Toast.svelte";
</script>

<div
  class="bottom-4 right-4 gap-2 max-w-sm pointer-events-none fixed z-50 flex w-full flex-col"
>
  {#each $toasts as toast (toast.id)}
    <div class="pointer-events-auto">
      <Toast {toast} onClose={() => toasts.remove(toast.id)} />
    </div>
  {/each}
</div>
