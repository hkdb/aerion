<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '@iconify/svelte'
  // @ts-ignore - wailsjs path
  import { GetAppInfo, OpenLogFile } from '../../../../wailsjs/go/app/App.js'
  import { BrowserOpenURL } from '../../../../wailsjs/runtime/runtime'
  import logo from '../../../assets/images/logo-universal.png'
  import { _ } from '$lib/i18n'

  interface AppInfo {
    name: string
    version: string
    description: string
    website: string
    license: string
  }

  let appInfo = $state<AppInfo | null>(null)
  let loading = $state(true)

  onMount(async () => {
    try {
      appInfo = await GetAppInfo()
    } catch (err) {
      console.error('Failed to load app info:', err)
    } finally {
      loading = false
    }
  })

  const PRIVACY_URL = 'https://github.com/hkdb/aerion/blob/main/docs/PRIVACY.md'
  const TERMS_URL = 'https://github.com/hkdb/aerion/blob/main/docs/TERMS.md'

  function openWebsite() {
    if (appInfo?.website) {
      BrowserOpenURL(appInfo.website)
    }
  }

  function openPrivacyPolicy() {
    BrowserOpenURL(PRIVACY_URL)
  }

  function openTermsOfService() {
    BrowserOpenURL(TERMS_URL)
  }

  async function openLogFile() {
    try {
      await OpenLogFile()
    } catch (err) {
      console.error('Failed to open log file:', err)
    }
  }
</script>

<div class="flex flex-col items-center justify-center py-6 space-y-6">
  {#if loading}
    <Icon icon="mdi:loading" class="w-8 h-8 animate-spin text-muted-foreground" />
  {:else if appInfo}
    <!-- Logo + App Name & Version -->
    <div class="flex flex-col items-center space-y-2">
      <img src={logo} alt="{appInfo.name} Logo" class="w-24 h-24" />
      <div class="text-center space-y-1">
        <h2 class="text-2xl font-bold text-foreground">{appInfo.name}</h2>
        <p class="text-sm text-muted-foreground">{$_('settingsAbout.version', { values: { version: appInfo.version } })}</p>
      </div>
    </div>

    <!-- Description -->
    <p class="text-center text-sm text-muted-foreground max-w-xs">
      {appInfo.description}
    </p>

    <!-- Links -->
    <div class="flex flex-col items-center gap-2">
      <button
        onclick={openWebsite}
        class="flex items-center gap-2 text-sm text-primary hover:underline transition-colors"
      >
        <Icon icon="mdi:github" class="w-5 h-5" />
        <span>{$_('settingsAbout.github')}</span>
      </button>
      <button
        onclick={openPrivacyPolicy}
        class="flex items-center gap-2 text-sm text-primary hover:underline transition-colors"
      >
        <Icon icon="mdi:shield-account" class="w-5 h-5" />
        <span>{$_('settingsAbout.privacyPolicy')}</span>
      </button>
      <button
        onclick={openTermsOfService}
        class="flex items-center gap-2 text-sm text-primary hover:underline transition-colors"
      >
        <Icon icon="mdi:file-document" class="w-5 h-5" />
        <span>{$_('settingsAbout.termsOfUse')}</span>
      </button>
      <button
        onclick={openLogFile}
        class="flex items-center gap-2 text-sm text-primary hover:underline transition-colors"
      >
        <Icon icon="mdi:file-document-outline" class="w-5 h-5" />
        <span>{$_('settingsAbout.openLog')}</span>
      </button>
    </div>

  {:else}
    <p class="text-muted-foreground">{$_('settingsAbout.failedToLoad')}</p>
  {/if}
</div>
