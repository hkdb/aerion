<script lang="ts">
  // CalendarSettingsDialog — the Calendar extension's settings dialog.
  //
  // Phase 1A is an empty placeholder so the dispatch path through
  // ExtensionSettingsDialog.svelte can be wired up. Sub-phase 1G replaces
  // the body with the real source list (sync interval + Sync Now + Edit +
  // Delete + Add CalDAV Source), mirroring ContactsSettingsDialog.

  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'

  interface Props {
    open: boolean
    onClose?: () => void
  }

  let { open = $bindable(false), onClose }: Props = $props()

  function handleClose() {
    open = false
    onClose?.()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) handleClose() }}>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Calendar settings</Dialog.Title>
      <Dialog.Description>
        Calendar settings will land alongside source management in a later sub-phase.
      </Dialog.Description>
    </Dialog.Header>

    <div class="text-sm text-muted-foreground py-4">
      No settings to show yet. The Calendar extension's plumbing is in
      place; CalDAV source management, sync intervals, colors, and the
      visibility controls arrive in sub-phases 1B–1G.
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={handleClose}>Close</Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
