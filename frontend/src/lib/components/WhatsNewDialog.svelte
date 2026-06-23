<script lang="ts">
  // Per-version release announcement. Shown once after the user upgrades
  // to a new Aerion version. Only the explicit OK click records
  // acknowledgement (via onAcknowledge → SetLastSeenVersion in App.svelte).
  // Closing via ESC or outside-click leaves the version unrecorded, so
  // the dialog fires again on next launch.
  //
  // Per-release update workflow: bump Version in app/state.go, then edit
  // the body markup below to be the new release announcement.
  import { Dialog as DialogPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import { Button } from '$lib/components/ui/button'
  // @ts-ignore - wailsjs path
  import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
  import { _ } from '$lib/i18n'

  interface Props {
    open: boolean
    onAcknowledge: () => void
  }

  let { open = $bindable(false), onAcknowledge }: Props = $props()

  const CHANGELOG_URL = 'https://github.com/hkdb/aerion/blob/main/CHANGELOG.md'

  function openChangelog() {
    BrowserOpenURL(CHANGELOG_URL)
  }
</script>

<DialogPrimitive.Root bind:open>
  <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay
      class="fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
    />
    <DialogPrimitive.Content
      class={cn(
        'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg translate-x-[-50%] translate-y-[-50%] gap-6 border bg-background p-8 shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg'
      )}
    >
      <div class="flex flex-col space-y-1.5 text-center sm:text-left">
        <h2 class="text-lg font-semibold leading-none tracking-tight">
          What's new?
        </h2>
      </div>

      <div class="space-y-4 max-h-[60vh] overflow-y-auto text-sm">
        <p>🚀 Welcome to Aerion v0.3.0!</p>

        <p>Some big changes have gone into this release. Here's a TL;DR summary:</p>

        <ul class="list-disc pl-6 space-y-1">
          <li>🛠️ 1st party extension system</li>
          <li>📑 Contacts extension</li>
          <li>🗓️ Calendar extension</li>
          <li>🏗 Sync improvements</li>
          <li>🐛 Other bug fixes and enhancements</li>
        </ul>

        <p>🏷 See the full change log here:</p>

        <p>
          <button
            type="button"
            class="text-primary hover:underline break-all focus:outline-none focus-visible:outline-none focus:ring-0"
            onclick={openChangelog}
          >
            https://github.com/hkdb/aerion/blob/main/CHANGELOG.md
          </button>
        </p>
      </div>

      <div class="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
        <Button onclick={onAcknowledge}>{$_('common.ok')}</Button>
      </div>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
</DialogPrimitive.Root>
