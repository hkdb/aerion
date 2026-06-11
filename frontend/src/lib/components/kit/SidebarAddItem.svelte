<script lang="ts">
  // SidebarAddItem — kit primitive for the "+ Add …" entry rendered at the
  // bottom of an extension's sidebar source/account list. Matches mail's
  // in-list add-button pattern so extension sidebars feel native.
  //
  // Intended placement: inside the same scrollable container as the source
  // list, BELOW the last source row (not in a separate footer strip).
  // Pair with whatever footer chrome the sidebar already has (sync status,
  // settings cog, etc.) — this primitive does not draw a divider.

  import Icon from '@iconify/svelte'

  interface Props {
    label: string
    icon?: string
    onclick: () => void
  }

  const { label, icon = 'mdi:plus', onclick }: Props = $props()
</script>

<div class="px-3 py-2">
  <button
    type="button"
    class="w-full flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground
           hover:text-foreground hover:bg-muted/50 rounded-md transition-colors"
    {onclick}
  >
    <Icon {icon} class="w-4 h-4" />
    <span>{label}</span>
  </button>
</div>
