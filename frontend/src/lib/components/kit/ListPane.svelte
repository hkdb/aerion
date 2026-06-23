<script lang="ts" generics="T extends { id: string }">
  // ListPane — generic vertical list with keyboard navigation, selection, and
  // accent-bar styling. Owns its keyboard via tabindex=0 + local keydown
  // listener; stopPropagation when matched so the global App.svelte handler
  // doesn't double-fire.
  //
  // Key bindings come from frontend/src/lib/keyboard/shortcuts.ts — same
  // source mail's MessageList consumes. Rebinding j/k → other keys changes
  // shortcuts.ts only; both sides follow.
  //
  // Pane-focus store integration: when the user clicks into the list or it
  // receives DOM focus, we call setFocusedPane(focusSlot). When focusedPane
  // === focusSlot, we DOM-focus the container so subsequent keypresses route
  // here. This lets Alt+H/L cycle uniformly across mail and extension panes.

  import { onMount, type Snippet } from 'svelte'
  import { KEY } from '$lib/keyboard/shortcuts'
  import { setFocusedPane, getFocusedPane, isPaneFlashing, registerPaneNav, type FocusablePane } from '$lib/stores/keyboard.svelte'

  type Density = 'micro' | 'compact' | 'standard' | 'large'

  interface Props {
    /** Items to render. Each must have a stable `id`. */
    items: T[]
    /** Currently-selected id; null when nothing selected. */
    selectedId: string | null
    /** Density preset propagated to per-row layout (ListRow defaults pull from this). */
    density?: Density
    /** Which pane-focus slot this list occupies. */
    focusSlot?: FocusablePane
    /** ARIA label for the list container. */
    label?: string
    /** Renderer per row. Consumer composes ListRow + content. */
    row: Snippet<[T, { selected: boolean }]>
    /** Empty-state snippet shown when items.length === 0. */
    empty?: Snippet
    /** Loading snippet shown when `loading` is true. */
    loading?: boolean
    loadingSnippet?: Snippet

    /** Fired on j/k/Arrow navigation when the highlighted row changes.
     *  Semantics: this is "focus change," NOT "open." Consumers should use
     *  it to update their selectedId state and refresh visual highlight — and
     *  nothing else. In particular: do NOT load detail / open viewer here. */
    onSelect: (id: string) => void
    /** Fired on Enter when an item is activated. Consumers wire actual "open"
     *  behavior here (load detail, slide in the viewer overlay, etc.). When
     *  absent, Enter is a no-op — there is intentionally no fallback to
     *  onSelect so consumers can't accidentally couple "highlight changed" with
     *  "viewer opens." Mouse clicks on rows fire onActivate via the canonical
     *  pattern: the consumer's row snippet wires `onclick` to invoke onActivate
     *  for that item id (see ContactList.svelte for the reference example). */
    onActivate?: (id: string) => void
    /** Fired on Space — optional multi-select hook. */
    onToggleCheck?: (id: string) => void
    /** Fired on Ctrl+A — optional bulk-select hook. */
    onSelectAll?: () => void
    /** Fired on Shift+J / Shift+ArrowDown to extend a multi-select range DOWN.
     *  Called with (fromId, toId) — the IDs that should be added to the
     *  consumer's checked set. Matches mail's `selectNextWithCheck` semantics
     *  (`MessageList.svelte:1095–1116`): cursor advances one row, both the
     *  previous and new rows get checked. ListPane swallows the key whether or
     *  not this handler is provided, so the event doesn't bubble to mail's
     *  window handler. */
    onRangeNext?: (fromId: string, toId: string) => void
    /** Fired on Shift+K / Shift+ArrowUp to extend a multi-select range UP.
     *  Mirror of onRangeNext (mail's `selectPreviousWithCheck`,
     *  `MessageList.svelte:1071–1092`). */
    onRangePrev?: (fromId: string, toId: string) => void
    /** Fired on Delete/Backspace with the currently-selected id. When provided,
     *  ListPane intercepts the key, calls preventDefault + stopPropagation, and
     *  invokes the handler. The stopPropagation matters: without it the event
     *  bubbles to mail's global window-level handler which deletes the focused
     *  message in the background. Even when onDelete is NOT provided, ListPane
     *  still swallows Delete/Backspace when focused so the mail handler stays
     *  off our turf. */
    onDelete?: (id: string) => void
    /** Fired when the user invokes the focus-search global shortcut (Ctrl+S).
     *  The consumer (e.g., ContactList) typically focuses its own search input. */
    onFocusSearch?: () => void
  }

  const {
    items,
    selectedId,
    // Destructured but not used locally — density is honored by the consumer's
    // ListRow renderer, not by ListPane's container styling. Kept in the Props
    // interface so consumers can still pass <ListPane density="...">; the
    // underscore prefix tells eslint this unused-on-purpose.
    density: _density = 'standard',
    focusSlot = 'messageList',
    label,
    row,
    empty,
    loading = false,
    loadingSnippet,
    onSelect,
    onActivate,
    onToggleCheck,
    onSelectAll,
    onRangeNext,
    onRangePrev,
    onDelete,
    onFocusSearch,
  }: Props = $props()

  let containerRef = $state<HTMLDivElement | null>(null)
  // Inner scrollable region — referenced so keyboard navigation can scroll the
  // newly-selected row into view (matches MessageList's pattern).
  let scrollRegionRef = $state<HTMLDivElement | null>(null)

  // Take DOM focus when this slot becomes the focused pane.
  $effect(() => {
    if (getFocusedPane() === focusSlot && containerRef && document.activeElement !== containerRef) {
      containerRef.focus()
    }
  })

  // When selectedId changes (keyboard nav or programmatic select), scroll the
  // matching row into view. ListRow sets aria-selected="true" on the active
  // row — query for that. queueMicrotask defers the lookup so the DOM has
  // settled with the new aria-selected state by the time we read it.
  // block:'nearest' is a no-op when the row is already in view (mouse clicks),
  // so this only kicks in when the user navigates with j/k or arrow keys.
  $effect(() => {
    const _ = selectedId  // dep-tracked
    if (!scrollRegionRef || selectedId == null) return
    queueMicrotask(() => {
      if (!scrollRegionRef) return
      const row = scrollRegionRef.querySelector('[aria-selected="true"]') as HTMLElement | null
      row?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    })
  })

  function indexOf(id: string | null): number {
    if (id == null) return -1
    return items.findIndex(it => it.id === id)
  }

  function move(step: number) {
    if (items.length === 0) return
    const idx = indexOf(selectedId)
    const next = idx < 0
      ? (step > 0 ? 0 : items.length - 1)
      : Math.max(0, Math.min(items.length - 1, idx + step))
    onSelect(items[next].id)
  }

  function handleKeyDown(e: KeyboardEvent) {
    // Range-extend (Shift+J / Shift+ArrowDown / Shift+K / Shift+ArrowUp) —
    // checked before LIST_NEXT/PREV because those predicates require !shiftKey.
    // Match mail's selectNextWithCheck / selectPreviousWithCheck pattern: cursor
    // moves one step, BOTH the leaving row and the arriving row are extended
    // into the consumer's check set. Always swallow the key (preventDefault +
    // stopPropagation) so it doesn't bubble to mail's global handler, even
    // when no callback is wired — same defensive shape as onDelete.
    if (KEY.LIST_NEXT_CHECK(e)) {
      e.preventDefault()
      e.stopPropagation()
      if (items.length === 0) return
      const idx = indexOf(selectedId)
      if (idx < 0 || idx >= items.length - 1) return
      const fromId = items[idx].id
      const toId = items[idx + 1].id
      onSelect(toId)
      if (onRangeNext) onRangeNext(fromId, toId)
      return
    }
    if (KEY.LIST_PREV_CHECK(e)) {
      e.preventDefault()
      e.stopPropagation()
      if (items.length === 0) return
      const idx = indexOf(selectedId)
      if (idx <= 0) return
      const fromId = items[idx].id
      const toId = items[idx - 1].id
      onSelect(toId)
      if (onRangePrev) onRangePrev(fromId, toId)
      return
    }
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
      const id = selectedId
      if (!id) return
      // No fallback to onSelect — that would couple "highlight changed" with
      // "viewer opens." If the consumer hasn't wired onActivate, Enter is a
      // no-op (mild bug visible during dev) rather than the silent wrong
      // behavior of auto-opening on every j/k navigation. See the onActivate
      // docstring in Props for the canonical pattern.
      if (!onActivate) return
      e.preventDefault()
      e.stopPropagation()
      onActivate(id)
      return
    }
    if (KEY.LIST_TOGGLE_CHECK(e)) {
      if (!onToggleCheck || !selectedId) return
      e.preventDefault()
      e.stopPropagation()
      onToggleCheck(selectedId)
      return
    }
    if (KEY.LIST_SELECT_ALL(e)) {
      if (!onSelectAll) return
      e.preventDefault()
      e.stopPropagation()
      onSelectAll()
      return
    }
    if (KEY.LIST_DELETE(e)) {
      // Always swallow Delete/Backspace when focused — even if no onDelete is
      // wired — so the event doesn't bubble to mail's window-level handler
      // and delete a message in a different pane. If onDelete IS provided,
      // invoke it with the current selection.
      e.preventDefault()
      e.stopPropagation()
      if (onDelete && selectedId) {
        onDelete(selectedId)
      }
      return
    }
  }

  function handleFocus() {
    if (getFocusedPane() !== focusSlot) {
      setFocusedPane(focusSlot)
    }
  }

  // Clicking anywhere in the list — including a row — should put DOM focus
  // on the container (the keyboard target), not on the clicked row. Rows
  // are non-focusable <div role="option"> elements (see ListRow); without
  // this, DOM focus would stay on whatever was previously focused, and
  // arrow keys would not flow back into the list.
  function handleMouseDown(_e: MouseEvent) {
    if (containerRef && document.activeElement !== containerRef) {
      containerRef.focus()
    }
  }

  // Register pane-nav so global Alt+? shortcuts can dispatch here.
  // (No mail-equivalent Alt shortcut targets messageList today, but the
  // registry is symmetric with SourceSidebar for future use.)
  onMount(() => registerPaneNav(focusSlot, {
    navigateNext: () => move(1),
    navigatePrev: () => move(-1),
    activate: () => {
      if (selectedId) (onActivate ?? onSelect)(selectedId)
    },
    focusSearch: onFocusSearch,
  }))

  const flashing = $derived(isPaneFlashing(focusSlot))
</script>

<div
  bind:this={containerRef}
  role="listbox"
  aria-label={label ?? 'List'}
  tabindex="0"
  class="flex-1 min-w-0 min-h-0 flex flex-col outline-none {flashing ? 'pane-focus-flash' : ''}"
  onkeydown={handleKeyDown}
  onfocus={handleFocus}
  onmousedown={handleMouseDown}
>
  <div bind:this={scrollRegionRef} class="flex-1 min-h-0 overflow-y-auto" aria-busy={loading}>
    {#if loading}
      {#if loadingSnippet}
        {@render loadingSnippet()}
      {:else}
        <p class="m-4 text-sm text-muted-foreground">Loading…</p>
      {/if}
    {:else if items.length === 0}
      {#if empty}
        {@render empty()}
      {:else}
        <p class="m-4 text-sm text-muted-foreground">No items.</p>
      {/if}
    {:else}
      {#each items as item (item.id)}
        {@render row(item, { selected: item.id === selectedId })}
      {/each}
    {/if}
  </div>
</div>

<style>
  /* density prop is propagated by the row renderer; container has no density-
     specific styles of its own. */
</style>
