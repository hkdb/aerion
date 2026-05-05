<script lang="ts">
  import { _ } from "$lib/i18n";
  import type { Toast } from "$lib/stores/toast";
  import Icon from "@iconify/svelte";

  interface Props {
    toast: Toast;
    onClose: () => void;
  }

  let { toast, onClose }: Props = $props();

  const icons = {
    success: "mdi:check-circle",
    error: "mdi:alert-circle",
    info: "mdi:information",
    warning: "mdi:alert"
  };

  const colors = {
    success: "bg-green-100 dark:bg-green-950 border-green-500",
    error: "bg-red-100 dark:bg-red-950 border-red-500",
    info: "bg-blue-100 dark:bg-blue-950 border-blue-500",
    warning: "bg-yellow-100 dark:bg-yellow-950 border-yellow-500"
  };

  const iconColors = {
    success: "text-green-600 dark:text-green-500",
    error: "text-red-600 dark:text-red-500",
    info: "text-blue-600 dark:text-blue-500",
    warning: "text-yellow-600 dark:text-yellow-500"
  };
</script>

<div
  class="gap-3 p-4 rounded-lg shadow-lg backdrop-blur-sm flex items-start border {colors[
    toast.type
  ]} animate-slide-in"
  role="alert"
>
  <Icon
    icon={icons[toast.type]}
    class="w-5 h-5 mt-0.5 flex-shrink-0 {iconColors[toast.type]}"
  />

  <div class="min-w-0 flex-1">
    <p class="text-sm light:text-background text-foreground">
      {toast.message}
    </p>

    {#if toast.actions && toast.actions.length > 0}
      <div class="gap-2 mt-2 flex">
        {#each toast.actions as action}
          <button
            class="text-xs font-medium px-2 py-1 rounded hover:bg-white/10 transition-colors"
            onclick={action.onClick}
          >
            {action.label}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <button
    class="p-1 rounded hover:bg-white/10 flex-shrink-0 transition-colors"
    onclick={onClose}
    aria-label={$_("aria.dismiss")}
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
