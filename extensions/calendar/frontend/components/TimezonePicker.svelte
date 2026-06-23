<script lang="ts">
  // TimezonePicker — calendar-domain searchable dropdown for picking the
  // display timezone. Mounted in ViewSwitcher where the old static
  // `tz: <name>` span used to be.
  //
  // Pattern: trigger button + floating panel anchored relatively, no scrim,
  // click-outside dismiss. Lifts the popover shape from
  // frontend/src/lib/components/ui/color-picker/ColorPicker.svelte; lifts
  // the j/k-style nav from composer/RecipientInput.svelte.
  //
  // Lists `Intl.supportedValuesOf('timeZone')` — ~400 IANA zones — filtered
  // by case-insensitive substring on the search input. "Auto-detect (use
  // system)" is always the first row.

  import { fly } from 'svelte/transition'
  import { _ } from 'svelte-i18n'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'

  // All IANA zones supported by the JS engine. Computed once at module init.
  const ALL_ZONES: string[] = (() => {
    try {
      return Intl.supportedValuesOf('timeZone')
    } catch {
      return []
    }
  })()

  const AUTO_ID = '__auto__'   // sentinel used for the auto-detect row

  let isOpen = $state(false)
  let searchQuery = $state('')
  let highlightedIdx = $state(0)
  let popoverRef = $state<HTMLDivElement | null>(null)
  let triggerRef = $state<HTMLButtonElement | null>(null)
  let searchInputRef = $state<HTMLInputElement | null>(null)
  let listRef = $state<HTMLUListElement | null>(null)

  // Computed coords for the popover. We use position:fixed (anchored to
  // the viewport) rather than position:absolute (anchored to the trigger)
  // so the dropdown isn't clipped when the trigger lives inside a scroll
  // container with overflow-y-auto — exactly the case when this picker
  // is mounted inside CalendarSettingsDialog. The popover auto-flips
  // upward when there isn't room below.
  let popoverStyle = $state('')
  const POPOVER_WIDTH = 288 // w-72 = 18rem ≈ 288px
  const POPOVER_HEIGHT = 320 // search input + max-h-72 list + padding

  function recomputePopoverPosition() {
    if (!triggerRef) return
    const rect = triggerRef.getBoundingClientRect()
    const flipsUp = rect.bottom + POPOVER_HEIGHT + 8 > window.innerHeight
      && rect.top - POPOVER_HEIGHT - 8 > 0
    const top = flipsUp ? rect.top - POPOVER_HEIGHT - 4 : rect.bottom + 4
    // Right-align the popover with the trigger button. Clamp to viewport
    // so it doesn't bleed off-screen on a narrow window.
    let left = rect.right - POPOVER_WIDTH
    if (left < 8) left = 8
    if (left + POPOVER_WIDTH > window.innerWidth - 8) {
      left = window.innerWidth - POPOVER_WIDTH - 8
    }
    popoverStyle = `position: fixed; top: ${top}px; left: ${left}px; width: ${POPOVER_WIDTH}px;`
  }

  // Filter list. The auto-detect row is always first and matches when the
  // user's query is empty or substring-matches "auto" / "system".
  const filtered = $derived.by(() => {
    const q = searchQuery.trim().toLowerCase()
    const matchesAuto = q === '' || 'auto'.includes(q) || 'system'.includes(q)
    const zones = q === ''
      ? ALL_ZONES
      : ALL_ZONES.filter(z => z.toLowerCase().includes(q))
    const rows: { id: string; label: string }[] = []
    if (matchesAuto) {
      rows.push({ id: AUTO_ID, label: $_('calendar.tzSelector.autoDetect') })
    }
    for (const z of zones) rows.push({ id: z, label: z })
    return rows
  })

  // Effective zone label shown on the trigger.
  const triggerLabel = $derived($_('calendar.viewSwitcher.tzLabel', {
    values: { tz: calendarSettings.effectiveTimezone },
  }))

  function openPanel() {
    isOpen = true
    searchQuery = ''
    // Pre-highlight the currently-selected zone (or auto-detect if unset).
    highlightedIdx = 0
    queueMicrotask(() => {
      recomputePopoverPosition()
      searchInputRef?.focus()
    })
  }

  function closePanel() {
    isOpen = false
  }

  function togglePanel() {
    if (isOpen) {
      closePanel()
      return
    }
    openPanel()
  }

  function activate(id: string) {
    const tz = id === AUTO_ID ? '' : id
    calendarSettings.setDisplayTimezone(tz)
    closePanel()
  }

  function onSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (filtered.length > 0) {
        highlightedIdx = Math.min(highlightedIdx + 1, filtered.length - 1)
        scrollHighlightedIntoView()
      }
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (filtered.length > 0) {
        highlightedIdx = Math.max(highlightedIdx - 1, 0)
        scrollHighlightedIntoView()
      }
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const row = filtered[highlightedIdx]
      if (row) activate(row.id)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      closePanel()
    }
  }

  function scrollHighlightedIntoView() {
    if (!listRef) return
    queueMicrotask(() => {
      const el = listRef?.querySelector('[aria-selected="true"]') as HTMLElement | null
      el?.scrollIntoView({ block: 'nearest' })
    })
  }

  // Click-outside to close. Listener lifted from ColorPicker.svelte:67-71.
  function handleClickOutside(e: MouseEvent) {
    if (popoverRef && !popoverRef.contains(e.target as Node)) {
      closePanel()
    }
  }

  $effect(() => {
    if (!isOpen) return
    document.addEventListener('click', handleClickOutside, true)
    // The popover uses position:fixed, so we need to close (or reposition)
    // on viewport changes — otherwise the trigger scrolls away inside the
    // dialog's scroll container while the popover stays put. Closing on
    // scroll is the standard popover UX; the user re-opens it after.
    const onViewportChange = () => closePanel()
    window.addEventListener('scroll', onViewportChange, true)
    window.addEventListener('resize', onViewportChange)
    return () => {
      document.removeEventListener('click', handleClickOutside, true)
      window.removeEventListener('scroll', onViewportChange, true)
      window.removeEventListener('resize', onViewportChange)
    }
  })

  // Reset highlight when the filtered list changes.
  $effect(() => {
    const len = filtered.length
    if (highlightedIdx >= len) highlightedIdx = Math.max(0, len - 1)
  })
</script>

<div class="inline-block" bind:this={popoverRef}>
  <button
    type="button"
    bind:this={triggerRef}
    class="text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40
           rounded px-1 py-0.5 transition-colors"
    title={$_('calendar.tzSelector.tooltip', { default: '' })}
    onclick={togglePanel}
  >
    {triggerLabel}
  </button>

  {#if isOpen}
    <div
      class="z-50 bg-popover border border-border rounded-lg shadow-lg p-2"
      style={popoverStyle}
      transition:fly={{ y: -5, duration: 150 }}
    >
      <input
        bind:this={searchInputRef}
        type="text"
        class="w-full h-8 px-2 mb-2 text-sm bg-background border border-border
               rounded focus:outline-none focus:ring-2 focus:ring-primary/50"
        placeholder={$_('calendar.tzSelector.searchPlaceholder')}
        bind:value={searchQuery}
        onkeydown={onSearchKeydown}
      />

      {#if filtered.length === 0}
        <p class="px-2 py-3 text-xs text-muted-foreground text-center">
          {$_('calendar.tzSelector.noResults')}
        </p>
      {/if}

      {#if filtered.length > 0}
        <ul bind:this={listRef} class="max-h-72 overflow-y-auto" role="listbox">
          {#each filtered as row, idx (row.id)}
            {@const isCurrent = (row.id === AUTO_ID && calendarSettings.displayTimezone === '')
              || row.id === calendarSettings.displayTimezone}
            {@const isHighlighted = idx === highlightedIdx}
            <li>
              <button
                type="button"
                role="option"
                aria-selected={isHighlighted}
                class="w-full text-left px-2 py-1.5 text-xs rounded cursor-pointer
                       {isHighlighted ? 'bg-muted/50' : 'hover:bg-muted/40'}
                       {isCurrent ? 'bg-primary/20 text-foreground' : 'text-foreground'}"
                onclick={() => activate(row.id)}
                onmouseenter={() => { highlightedIdx = idx }}
              >
                {row.label}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</div>
