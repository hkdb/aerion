<script lang="ts">
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";
  import { onMount } from "svelte";

  // @ts-ignore - wailsjs path
  import { GetAppInfo } from "../../../../wailsjs/go/app/App.js";
  import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";
  import logo from "../../../assets/images/logo-universal.png";

  interface AppInfo {
    name: string;
    version: string;
    description: string;
    website: string;
    license: string;
  }

  let appInfo = $state<AppInfo | null>(null);
  let loading = $state(true);

  onMount(async () => {
    try {
      appInfo = await GetAppInfo();
    } catch (err) {
      console.error("Failed to load app info:", err);
    } finally {
      loading = false;
    }
  });

  const PRIVACY_URL =
    "https://github.com/hkdb/aerion/blob/main/docs/PRIVACY.md";
  const TERMS_URL = "https://github.com/hkdb/aerion/blob/main/docs/TERMS.md";

  function openWebsite() {
    if (appInfo?.website) {
      BrowserOpenURL(appInfo.website);
    }
  }

  function openPrivacyPolicy() {
    BrowserOpenURL(PRIVACY_URL);
  }

  function openTermsOfService() {
    BrowserOpenURL(TERMS_URL);
  }
</script>

<div class="py-6 space-y-6 flex flex-col items-center justify-center">
  {#if loading}
    <Icon
      icon="mdi:loading"
      class="w-8 h-8 animate-spin text-muted-foreground"
    />
  {:else if appInfo}
    <!-- Logo + App Name & Version -->
    <div class="space-y-2 flex flex-col items-center">
      <img src={logo} alt="{appInfo.name} Logo" class="w-24 h-24" />
      <div class="space-y-1 text-center">
        <h2 class="text-2xl font-bold text-foreground">{appInfo.name}</h2>
        <p class="text-sm text-muted-foreground">
          {$_("settingsAbout.version", {
            values: { version: appInfo.version }
          })}
        </p>
      </div>
    </div>

    <!-- Description -->
    <p class="text-sm text-muted-foreground max-w-xs text-center">
      {appInfo.description}
    </p>

    <!-- Links -->
    <div class="gap-2 flex flex-col items-center">
      <button
        onclick={openWebsite}
        class="gap-2 text-sm text-primary flex items-center transition-colors hover:underline"
      >
        <Icon icon="mdi:github" class="w-5 h-5" />
        <span>{$_("settingsAbout.github")}</span>
      </button>
      <button
        onclick={openPrivacyPolicy}
        class="gap-2 text-sm text-primary flex items-center transition-colors hover:underline"
      >
        <Icon icon="mdi:shield-account" class="w-5 h-5" />
        <span>{$_("settingsAbout.privacyPolicy")}</span>
      </button>
      <button
        onclick={openTermsOfService}
        class="gap-2 text-sm text-primary flex items-center transition-colors hover:underline"
      >
        <Icon icon="mdi:file-document" class="w-5 h-5" />
        <span>{$_("settingsAbout.termsOfUse")}</span>
      </button>
    </div>
  {:else}
    <p class="text-muted-foreground">{$_("settingsAbout.failedToLoad")}</p>
  {/if}
</div>
