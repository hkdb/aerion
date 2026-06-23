<script lang="ts" generics="T extends { id: string }">
  // SourceSidebar — sectioned left sidebar primitive for extensions.
  // Owns keyboard navigation (Up/Down/J/K within sidebar) via tabindex+focus.
  // Selection state managed by consumer via selectedId + onSelect.
  //
  // Pane-focus store integration mirrors ListPane: registers focusSlot, takes
  // DOM focus when the slot matches so Alt+H/L cycling routes here.
  //
  // Visual chrome (container styling + responsive overlay + back button +
  // title) is delegated to kit `SidebarFrame`. Future cross-extension settings
  // (sidebar density, font-size) wire into SidebarFrame; SourceSidebar
  // consumers inherit them automatically.

  import { type Snippet, onMount } from 'svelte'
  import SidebarFrame from './SidebarFrame.svelte'
  import { KEY } from '$lib/keyboard/shortcuts'
  import { setFocusedPane, getFocusedPane, isPaneFlashing, registerPaneNav, type FocusablePane } from '$lib/stores/keyboard.svelte'

  type SourceSection<U extends { id: string }> = {
    heading?: string
    items: U[]
  }

  interface Props {
    title?: string
    sections: SourceSection<T>[]
    selectedId: string | null
    focusSlot?: FocusablePane
    label?: string
    item: Snippet<[T, { active: boolean }]>
    header?: Snippet
    sectionEmpty?: Snippet<[SourceSection<T>]>
    /** Optional sticky bottom strip — forwarded to SidebarFrame's footer
     *  slot. Consumers typically render kit `SidebarFooter` here for the
     *  shared sync/settings chrome. */
    footerContent?: Snippet
    onSelect: (id: string) => void
  }

  const {
    title,
    sections,
    selectedId,
    focusSlot = 'sidebar',
    label,
    item,
    header,
    sectionEmpty,
    footerContent,
    onSelect,
  }: Props = $props()

  let containerRef = $state<HTMLElement | null>(null)

  const allItems = $derived(sections.flatMap(s => s.items))

  $effect(() => {
    if (getFocusedPane() === focusSlot && containerRef && document.activeElement !== containerRef) {
      containerRef.focus()
    }
  })

  function indexOf(id: string | null): number {
    if (id == null) return -1
    return allItems.findIndex(it => it.id === id)
  }

  function move(step: number) {
    if (allItems.length === 0) return
    const idx = indexOf(selectedId)
    const next = idx < 0
      ? (step > 0 ? 0 : allItems.length - 1)
      : Math.max(0, Math.min(allItems.length - 1, idx + step))
    onSelect(allItems[next].id)
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (KEY.LIST_NEXT(e)) {
      e.preventDefault()
      e.stopPropagation()
      move(1)
      return
    }
    if (KEY.LIST_PREV(e)) {
      e.preventDefault()
      e.stopPropagation()
      move(-1)
      return
    }
    if (KEY.LIST_OPEN(e)) {
      if (!selectedId) return
      e.preventDefault()
      e.stopPropagation()
      onSelect(selectedId)
      return
    }
  }

  function handleFocus() {
    if (getFocusedPane() !== focusSlot) {
      setFocusedPane(focusSlot)
    }
  }

  function handleMouseDown(_e: MouseEvent) {
    if (containerRef && document.activeElement !== containerRef) {
      containerRef.focus()
    }
  }

  // Register so Alt+J/K dispatched from the global handler routes here.
  onMount(() => registerPaneNav(focusSlot, {
    navigateNext: () => move(1),
    navigatePrev: () => move(-1),
    activate: () => { if (selectedId) onSelect(selectedId) },
  }))

  const flashing = $derived(isPaneFlashing(focusSlot))
</script>

<SidebarFrame
  {title}
  {label}
  bind:containerRef
  focusable
  class={flashing ? 'pane-focus-flash' : ''}
  onkeydown={handleKeyDown}
  onfocus={handleFocus}
  onmousedown={handleMouseDown}
>
  {#snippet body()}
    {#if header}
      {@render header()}
    {/if}

    {#each sections as section, sIdx (sIdx)}
      {#if section.heading}
        <div class="mx-4 mt-3 mb-1 text-[11px] uppercase tracking-wider text-muted-foreground">
          {section.heading}
        </div>
      {/if}

      {#if section.items.length === 0}
        {#if sectionEmpty}
          {@render sectionEmpty(section)}
        {/if}
      {:else}
        {#each section.items as it (it.id)}
          {@render item(it, { active: it.id === selectedId })}
        {/each}
      {/if}
    {/each}
  {/snippet}

  {#snippet footer()}
    {#if footerContent}
      {@render footerContent()}
    {/if}
  {/snippet}
</SidebarFrame>
