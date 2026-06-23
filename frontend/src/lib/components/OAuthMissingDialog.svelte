<script lang="ts">
  // Launch-time warning shown when one or more OAuth provider credentials
  // weren't compiled into this build of Aerion. Sign-in for the listed
  // providers will silently fail otherwise. Acknowledged via the OK button;
  // an optional "Don't show again" toggle persists the opt-out via the
  // SetOAuthWarningDisabled setting in App.svelte.
  import * as AlertDialog from '$lib/components/ui/alert-dialog'
  import { Button } from '$lib/components/ui/button'
  import Switch from '$lib/components/ui/switch/Switch.svelte'
  import Icon from '@iconify/svelte'
  // @ts-ignore - wailsjs path
  import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
  import { _ } from '$lib/i18n'

  interface OAuthStatus {
    google: boolean
    microsoft: boolean
    googleTesting: boolean
  }

  interface Props {
    open: boolean
    oauthStatus: OAuthStatus
    onDismiss: (dontShowAgain: boolean) => void
  }

  let { open = $bindable(false), oauthStatus, onDismiss }: Props = $props()

  let dontShowAgain = $state(false)

  const INSTALL_URL = 'https://aerion.3df.io/docs/getting-started/installation/'

  function iconFor(present: boolean): string {
    if (present) return 'lucide:check'
    return 'lucide:triangle-alert'
  }

  function iconClassFor(present: boolean): string {
    if (present) return 'text-emerald-500'
    return 'text-amber-500'
  }

  function handleOk() {
    onDismiss(dontShowAgain)
    dontShowAgain = false
  }

  function openInstallDocs() {
    BrowserOpenURL(INSTALL_URL)
  }
</script>

<AlertDialog.Root bind:open>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>{$_('oauthMissing.title')}</AlertDialog.Title>
      <AlertDialog.Description>
        <p class="mb-3">{$_('oauthMissing.intro')}</p>
        <ul class="space-y-1.5 mb-3 pl-2 text-sm">
          <li class="flex items-center gap-2">
            <Icon icon={iconFor(oauthStatus.microsoft)} class={iconClassFor(oauthStatus.microsoft)} width="18" height="18" />
            <span>Microsoft</span>
          </li>
          <li class="flex items-center gap-2">
            <Icon icon={iconFor(oauthStatus.google)} class={iconClassFor(oauthStatus.google)} width="18" height="18" />
            <span>Google</span>
          </li>
          <li class="flex items-center gap-2">
            <Icon icon={iconFor(oauthStatus.googleTesting)} class={iconClassFor(oauthStatus.googleTesting)} width="18" height="18" />
            <span>Google (Extensions)</span>
          </li>
        </ul>
        <p class="mb-3">{$_('oauthMissing.implication')}</p>
        <p>{$_('oauthMissing.installInstruction')}</p>
      </AlertDialog.Description>
    </AlertDialog.Header>

    <button
      type="button"
      class="text-sm text-primary hover:underline break-all text-left focus:outline-none focus-visible:outline-none focus:ring-0"
      onclick={openInstallDocs}
    >
      https://aerion.3df.io/docs/getting-started/installation/
    </button>

    <label class="flex items-center gap-3 text-sm mt-2">
      <Switch bind:checked={dontShowAgain} />
      <span>{$_('oauthMissing.dontShowAgain')}</span>
    </label>

    <AlertDialog.Footer>
      <Button onclick={handleOk}>{$_('common.ok')}</Button>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
