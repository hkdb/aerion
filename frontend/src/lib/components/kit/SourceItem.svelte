<script lang="ts">
  // SourceItem — clickable row inside SourceSidebar. Rendered as a
  // non-focusable <div role="option"> for the same reason as ListRow:
  // DOM focus stays on the parent SourceSidebar container so keyboard
  // nav (J/K/Up/Down) continues working after a click.
  //
  // Active-row styling mirrors mail's FolderTreeItem (subtle primary-tinted
  // background + primary text + medium weight) so the two sidebars look
  // consistent across the app.

  import Icon from '@iconify/svelte'

  interface Props {
    icon?: string
    label: string
    active?: boolean
    onclick?: (e: MouseEvent) => void
  }

  const { icon, label, active = false, onclick }: Props = $props()
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_interactive_supports_focus -->
<div
  role="option"
  aria-selected={active}
  class="flex items-center gap-2 mx-2 px-3 py-1.5 text-sm rounded-md text-left transition-colors cursor-pointer select-none {active
    ? 'bg-primary/10 text-primary font-medium'
    : 'text-foreground hover:bg-muted/50'}"
  onclick={(e) => onclick?.(e)}
>
  {#if icon}
    <Icon {icon} class="w-4 h-4 flex-shrink-0" />
  {/if}
  <span class="truncate">{label}</span>
</div>
