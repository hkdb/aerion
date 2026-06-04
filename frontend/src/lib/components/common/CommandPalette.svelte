<script module lang="ts">
  export interface Command {
    id: string
    label: string
    /** Right-aligned hint, e.g. a keyboard shortcut */
    hint?: string
    /** Iconify icon name */
    icon?: string
    /** Extra search terms */
    keywords?: string
    /** Whether the command is currently actionable; non-actionable ones are hidden */
    enabled?: boolean
    run: () => void
  }
</script>

<script lang="ts">
  import Icon from '@iconify/svelte'
  import * as Dialog from '$lib/components/ui/dialog'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { _ } from '$lib/i18n'

  interface Props {
    open: boolean
    commands: Command[]
  }

  let { open = $bindable(false), commands }: Props = $props()

  let query = $state('')
  let focusedIndex = $state(0)
  let active = $state(false)
  let inputEl = $state<HTMLInputElement | null>(null)
  let listEl = $state<HTMLDivElement | null>(null)

  const visibleCommands = $derived(commands.filter((c) => c.enabled !== false))

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase()
    if (!q) return visibleCommands
    return visibleCommands.filter((c) =>
      c.label.toLowerCase().includes(q) || (c.keywords ?? '').toLowerCase().includes(q)
    )
  })

  // Activate dialog guard while open so background shortcuts don't fire.
  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  // Reset state on open; focus the input.
  $effect(() => {
    if (!open) {
      active = false
      query = ''
      return
    }
    focusedIndex = 0
    active = false
    const timer = setTimeout(() => {
      active = true
      inputEl?.focus()
    }, 0)
    return () => clearTimeout(timer)
  })

  // Keep focus in range as the filter changes.
  $effect(() => {
    void query
    if (focusedIndex > filtered.length - 1) focusedIndex = Math.max(0, filtered.length - 1)
  })

  // Scroll focused row into view.
  $effect(() => {
    if (!listEl) return
    const rows = listEl.querySelectorAll('[data-command-row]')
    ;(rows[focusedIndex] as HTMLElement | undefined)?.scrollIntoView({ block: 'nearest' })
  })

  function runAt(index: number) {
    const cmd = filtered[index]
    if (!cmd) return
    open = false
    // Run after close so the palette tears down cleanly first.
    queueMicrotask(() => cmd.run())
  }

  function handleKeydown(e: KeyboardEvent) {
    if (!active || !open) return
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        e.stopPropagation()
        if (filtered.length > 0) focusedIndex = (focusedIndex + 1) % filtered.length
        break
      case 'ArrowUp':
        e.preventDefault()
        e.stopPropagation()
        if (filtered.length > 0) focusedIndex = (focusedIndex - 1 + filtered.length) % filtered.length
        break
      case 'Enter':
        e.preventDefault()
        e.stopPropagation()
        runAt(focusedIndex)
        break
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-lg p-0 gap-0 overflow-hidden shadow-2xl rounded-2xl sm:rounded-2xl" showOverlay={false} showClose={false}>
    <!-- Search input -->
    <div class="relative border-b border-border">
      <Icon icon="feather:search" class="absolute left-4 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
      <input
        bind:this={inputEl}
        bind:value={query}
        placeholder={$_('commandPalette.placeholder')}
        class="flex h-12 w-full bg-transparent pl-11 pr-4 text-sm placeholder:text-muted-foreground focus-visible:outline-none"
      />
    </div>

    <!-- Command list -->
    <div class="max-h-80 overflow-y-auto py-1 scrollbar-thin" bind:this={listEl} role="listbox">
      {#if filtered.length === 0}
        <div class="px-4 py-6 text-center text-sm text-muted-foreground">
          {$_('commandPalette.noResults')}
        </div>
      {:else}
        {#each filtered as cmd, i (cmd.id)}
          <button
            type="button"
            role="option"
            data-command-row
            aria-selected={i === focusedIndex}
            class="w-full flex items-center gap-3 px-4 py-2.5 text-left text-sm transition-colors {i === focusedIndex ? 'bg-primary/10 text-primary' : 'hover:bg-muted/50'}"
            onmousemove={() => (focusedIndex = i)}
            onclick={() => runAt(i)}
          >
            {#if cmd.icon}
              <Icon icon={cmd.icon} class="h-4 w-4 shrink-0 {i === focusedIndex ? 'text-primary' : 'text-muted-foreground'}" />
            {/if}
            <span class="flex-1 truncate">{cmd.label}</span>
            {#if cmd.hint}
              <span class="shrink-0 text-xs text-muted-foreground">{cmd.hint}</span>
            {/if}
          </button>
        {/each}
      {/if}
    </div>
  </Dialog.Content>
</Dialog.Root>
