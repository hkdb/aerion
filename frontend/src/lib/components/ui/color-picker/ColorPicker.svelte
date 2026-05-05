<script lang="ts">
  import { _ } from "$lib/i18n";
  import { fly } from "svelte/transition";

  interface Props {
    value: string;
    onchange: (color: string) => void;
  }

  let { value, onchange }: Props = $props();

  // Default color presets
  const presets = [
    "#3B82F6", // blue
    "#10B981", // green
    "#F59E0B", // amber
    "#EF4444", // red
    "#8B5CF6", // purple
    "#EC4899", // pink
    "#06B6D4", // cyan
    "#F97316" // orange
  ];

  let isOpen = $state(false);
  let hexInput = $state(presets[0]);
  let popoverRef: HTMLDivElement | null = $state(null);

  // Sync hex input with value prop (including initial value)
  $effect(() => {
    hexInput = value || presets[0];
  });

  function togglePopover() {
    isOpen = !isOpen;
  }

  function selectPreset(color: string) {
    hexInput = color;
    onchange(color);
    isOpen = false;
  }

  function handleHexInput(e: Event) {
    const input = e.target as HTMLInputElement;
    let hex = input.value.trim();

    // Auto-add # if missing
    if (hex && !hex.startsWith("#")) {
      hex = "#" + hex;
    }

    hexInput = hex;

    // Validate hex color
    if (/^#[0-9A-Fa-f]{6}$/.test(hex)) {
      onchange(hex);
    }
  }

  function handleHexKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      isOpen = false;
    }
  }

  // Close popover when clicking outside
  function handleClickOutside(e: MouseEvent) {
    if (popoverRef && !popoverRef.contains(e.target as Node)) {
      isOpen = false;
    }
  }

  $effect(() => {
    if (isOpen) {
      document.addEventListener("click", handleClickOutside, true);
      return () => {
        document.removeEventListener("click", handleClickOutside, true);
      };
    }
  });

  // Display color (use value or fallback to first preset)
  const displayColor = $derived(value || presets[0]);
</script>

<div class="relative inline-block" bind:this={popoverRef}>
  <!-- Color swatch button -->
  <button
    type="button"
    class="w-8 h-8 rounded-md border-border shadow-sm hover:ring-primary/50 cursor-pointer border transition-all hover:ring-2"
    style="background-color: {displayColor}"
    onclick={togglePopover}
    aria-label={$_("aria.selectColor")}
  ></button>

  <!-- Popover -->
  {#if isOpen}
    <div
      class="left-0 mt-2 bg-popover border-border rounded-lg shadow-lg p-3 w-56 absolute top-full z-50 border"
      transition:fly={{ y: -5, duration: 150 }}
    >
      <!-- Preset colors grid -->
      <div class="gap-2 mb-3 grid grid-cols-4">
        {#each presets as preset}
          <button
            type="button"
            class="w-10 h-10 rounded-md cursor-pointer border-2 transition-transform hover:scale-110 {preset ===
            value
              ? 'border-primary ring-primary/50 ring-2'
              : 'border-transparent'}"
            style="background-color: {preset}"
            onclick={() => selectPreset(preset)}
            aria-label={$_("aria.selectPresetColor", {
              values: { color: preset }
            })}
          ></button>
        {/each}
      </div>

      <!-- Hex input -->
      <div class="gap-2 flex items-center">
        <div
          class="w-8 h-8 rounded border-border flex-shrink-0 border"
          style="background-color: {hexInput}"
        ></div>
        <input
          type="text"
          class="h-8 px-2 text-sm bg-background border-border rounded focus:ring-primary/50 font-mono flex-1 border focus:ring-2 focus:outline-none"
          placeholder="#000000"
          value={hexInput}
          oninput={handleHexInput}
          onkeydown={handleHexKeydown}
          maxlength="7"
        />
      </div>
    </div>
  {/if}
</div>
