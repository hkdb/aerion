<script lang="ts">
  import RailButton from './RailButton.svelte'
  import { getRailTabs, isRailVisible } from '$lib/stores/extensionRegistry.svelte'
  import { getActiveExtension, setActiveExtension } from '$lib/stores/uiState.svelte'

  // Mail is always present and always first; extensions follow in their
  // registered Order. Rail renders only when at least one extension is enabled
  // (so there's something to switch between).
  let active = $derived(getActiveExtension())
  let visible = $derived(isRailVisible())
  let tabs = $derived(getRailTabs())

  function select(name: string) {
    setActiveExtension(name)
  }
</script>

{#if visible}
  <nav
    class="flex flex-col items-stretch w-12 flex-shrink-0 bg-muted/30 border-r border-border pt-2"
    aria-label="Active extension"
  >
    <RailButton
      icon="mdi:email"
      label="Mail"
      active={active === 'mail'}
      onclick={() => select('mail')}
    />
    {#each tabs as tab (tab.extensionId)}
      <RailButton
        icon={tab.icon || 'mdi:puzzle'}
        label={tab.label}
        active={active === tab.extensionId}
        onclick={() => select(tab.extensionId)}
      />
    {/each}
  </nav>
{/if}
