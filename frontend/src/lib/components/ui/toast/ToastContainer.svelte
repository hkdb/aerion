<script lang="ts">
  import { toasts } from '$lib/stores/toast'
  import Toast from './Toast.svelte'
</script>

<div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-sm w-full pointer-events-none">
  {#each $toasts.slice(-3) as toast (toast.id)}
    <div class="pointer-events-auto">
      <Toast {toast} onClose={() => toasts.remove(toast.id)} />
    </div>
  {/each}
</div>
