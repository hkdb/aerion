<script lang="ts">
  import { toasts } from '$lib/stores/toast'
  import Toast from './Toast.svelte'
</script>

<div class="fixed bottom-4 inset-x-4 z-50 flex flex-col gap-2 max-w-sm ml-auto pointer-events-none">
  {#each $toasts as toast (toast.id)}
    <div class="pointer-events-auto">
      <Toast {toast} onClose={() => toasts.remove(toast.id)} />
    </div>
  {/each}
</div>
