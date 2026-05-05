<script lang="ts">
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";

  import {
    Quit,
    WindowMinimise,
    WindowToggleMaximise
  } from "../../../../wailsjs/runtime/runtime";

  interface Props {
    onClose?: () => void;
  }

  let { onClose }: Props = $props();

  let isMaximized = $state(false);
  let isHovering = $state(false);

  async function minimize() {
    await WindowMinimise();
  }

  async function toggleMaximize() {
    await WindowToggleMaximise();
    isMaximized = !isMaximized;
  }

  function close() {
    if (onClose) {
      onClose();
    } else {
      // For windows without custom onClose (e.g., composer), just quit directly
      Quit();
    }
  }
</script>

<header
  class="h-10 bg-muted/50 border-border flex shrink-0 items-center justify-between border-b select-none"
>
  <!-- Drag region - left side with app title -->
  <div
    class="gap-2 px-3 flex h-full flex-1 items-center"
    style="--wails-draggable: drag"
  >
    <Icon icon="mdi:email-fast-outline" class="w-5 h-5 text-primary" />
    <span class="text-sm font-medium text-foreground">Aerion</span>
  </div>

  <!-- Mac-style traffic light controls -->
  <div
    class="gap-2 px-3 flex h-full items-center"
    role="group"
    aria-label={$_("aria.windowControls")}
    onmouseenter={() => (isHovering = true)}
    onmouseleave={() => (isHovering = false)}
  >
    <!-- Minimize (yellow) -->
    <button
      class="w-3 h-3 flex items-center justify-center rounded-full bg-[#FEBC2E] transition-all hover:brightness-90 active:brightness-75"
      onclick={minimize}
      title={$_("window.minimize")}
      aria-label={$_("aria.minimizeWindow")}
    >
      {#if isHovering}
        <span class="font-bold text-black/60 text-[10px] leading-none">−</span>
      {/if}
    </button>

    <!-- Maximize/Restore (green) -->
    <button
      class="w-3 h-3 flex items-center justify-center rounded-full bg-[#28C840] transition-all hover:brightness-90 active:brightness-75"
      onclick={toggleMaximize}
      title={isMaximized ? $_("window.restore") : $_("window.maximize")}
      aria-label={isMaximized
        ? $_("aria.restoreWindow")
        : $_("aria.maximizeWindow")}
    >
      {#if isHovering}
        <span class="font-bold text-black/60 text-[10px] leading-none">+</span>
      {/if}
    </button>

    <!-- Close (red) -->
    <button
      class="w-3 h-3 flex items-center justify-center rounded-full bg-[#FF5F57] transition-all hover:brightness-90 active:brightness-75"
      onclick={close}
      title={$_("window.close")}
      aria-label={$_("aria.closeWindow")}
    >
      {#if isHovering}
        <span class="font-bold text-black/60 text-[10px] leading-none">×</span>
      {/if}
    </button>
  </div>
</header>
