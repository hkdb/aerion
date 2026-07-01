<script lang="ts">
  import Icon from '@iconify/svelte'
  import type { Toast } from '$lib/stores/toast'
  import { _ } from '$lib/i18n'

  interface Props {
    toast: Toast
    onClose: () => void
  }

  let { toast, onClose }: Props = $props()

  const icons = {
    success: 'mdi:check-circle',
    error: 'mdi:alert-circle',
    info: 'mdi:information',
    warning: 'mdi:alert'
  }

  const colors = {
    success: 'bg-background border-border',
    error: 'bg-background border-red-500/50 dark:border-red-500/30',
    info: 'bg-background border-border',
    warning: 'bg-background border-yellow-500/50 dark:border-yellow-500/30'
  }

  const iconColors = {
    success: 'text-green-600 dark:text-green-500',
    error: 'text-red-600 dark:text-red-500',
    info: 'text-blue-600 dark:text-blue-500',
    warning: 'text-yellow-600 dark:text-yellow-500'
  }
</script>

<div
  class="flex items-start gap-3 p-4 rounded-lg border shadow-lg backdrop-blur-sm {colors[toast.type]} animate-slide-in"
  role="alert"
>
  <Icon icon={icons[toast.type]} class="w-5 h-5 flex-shrink-0 mt-0.5 {iconColors[toast.type]}" />

  <div class="flex-1 min-w-0">
    <p class="text-sm text-foreground">{toast.message}</p>

    {#if toast.actions && toast.actions.length > 0}
      <div class="flex gap-2 mt-2">
        {#each toast.actions as action (action.label)}
          <button
            class="text-xs font-medium px-2 py-1 rounded hover:bg-muted transition-colors"
            onclick={action.onClick}
          >
            {action.label}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <button
    class="p-1 rounded hover:bg-muted transition-colors flex-shrink-0"
    onclick={onClose}
    aria-label={$_('aria.dismiss')}
  >
    <Icon icon="mdi:close" class="w-4 h-4 text-muted-foreground" />
  </button>
</div>

<style>
  @keyframes slide-in {
    from {
      transform: translateX(100%);
      opacity: 0;
    }
    to {
      transform: translateX(0);
      opacity: 1;
    }
  }

  .animate-slide-in {
    animation: slide-in 0.2s ease-out;
  }
</style>
