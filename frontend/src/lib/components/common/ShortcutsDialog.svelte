<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'

  interface Props {
    open: boolean
  }

  let { open = $bindable(false) }: Props = $props()

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  const isMac = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform)
  const mod = isMac ? '⌘' : 'Ctrl'
  const shift = isMac ? '⇧' : 'Shift'
  const alt = isMac ? '⌥' : 'Alt'

  interface Shortcut {
    keys: string[]
    label: string
  }
  interface Group {
    title: string
    items: Shortcut[]
  }

  const groups: Group[] = [
    {
      title: 'General',
      items: [
        { keys: [mod, 'K'], label: 'Open command palette' },
        { keys: [mod, 'N'], label: 'New message' },
        { keys: [mod, 'Z'], label: 'Undo last action' },
        { keys: [mod, 'R'], label: 'Refresh / sync all' },
        { keys: [mod, ','], label: 'Settings' },
        { keys: [mod, 'Q'], label: 'Quit' },
      ],
    },
    {
      title: 'Navigation',
      items: [
        { keys: ['↑', '↓'], label: 'Move between messages' },
        { keys: ['j', 'k'], label: 'Move between messages' },
        { keys: ['Enter'], label: 'Open selected message' },
        { keys: ['Space'], label: 'Toggle checkbox' },
        { keys: [alt, '←'], label: 'Focus previous pane' },
        { keys: [alt, '→'], label: 'Focus next pane' },
        { keys: [alt, '↑'], label: 'Previous folder' },
        { keys: [alt, '↓'], label: 'Next folder' },
        { keys: ['Esc'], label: 'Close / clear selection' },
      ],
    },
    {
      title: 'Message actions',
      items: [
        { keys: ['c'], label: 'Compose' },
        { keys: ['m'], label: 'Move to folder' },
        { keys: ['s'], label: 'Star / unstar' },
        { keys: ['u'], label: 'Toggle read / unread' },
        { keys: [mod, 'J'], label: 'Mark as spam' },
        { keys: ['Delete'], label: 'Move to trash' },
        { keys: [shift, 'Delete'], label: 'Delete permanently' },
        { keys: ['f'], label: 'Focus thread' },
        { keys: [shift, 'F'], label: 'Focus single message' },
      ],
    },
    {
      title: 'Reply & search',
      items: [
        { keys: [mod, 'R'], label: 'Reply' },
        { keys: [mod, shift, 'R'], label: 'Reply all' },
        { keys: [mod, 'F'], label: 'Forward' },
        { keys: [mod, 'S'], label: 'Search messages' },
        { keys: [mod, 'A'], label: 'Select all' },
        { keys: [mod, shift, 'A'], label: 'Sync all accounts' },
        { keys: [mod, 'L'], label: 'Load images' },
      ],
    },
  ]
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Keyboard shortcuts</Dialog.Title>
    </Dialog.Header>

    <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-6 max-h-[70vh] overflow-y-auto pr-1 scrollbar-thin">
      {#each groups as group (group.title)}
        <div>
          <h3 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-2">
            {group.title}
          </h3>
          <div class="space-y-1.5">
            {#each group.items as sc (sc.label + sc.keys.join())}
              <div class="flex items-center justify-between gap-3 text-sm">
                <span class="text-foreground">{sc.label}</span>
                <span class="flex items-center gap-1 shrink-0">
                  {#each sc.keys as key (key)}
                    <kbd class="min-w-[1.5rem] text-center rounded border border-border bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
                      {key}
                    </kbd>
                  {/each}
                </span>
              </div>
            {/each}
          </div>
        </div>
      {/each}
    </div>
  </Dialog.Content>
</Dialog.Root>
