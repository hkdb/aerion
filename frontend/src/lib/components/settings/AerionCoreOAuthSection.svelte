<script lang="ts">
  // AerionCoreOAuthSection — Settings → Accounts disclosure section for
  // Aerion CORE's OAuth client credentials (google-mail, microsoft-mail).
  //
  // COLLAPSED by default — this is advanced power-user territory. Most users
  // never expand it; Aerion ships verified mail creds out of the box.
  //
  // Per-extension OAuth slots (google-contacts, microsoft-contacts, etc.)
  // live inside that extension's own settings dialog, NOT here.
  //
  // Visual disclosure pattern lifted from sidebar/AccountSection.svelte
  // (isExpanded state + chevron icon toggle) — no separate collapsible
  // component exists in the kit.

  import Icon from '@iconify/svelte'
  import OAuthCredsSlotEditor from '$lib/components/kit/OAuthCredsSlotEditor.svelte'

  let isExpanded = $state(false)
</script>

<section class="mt-6 border-t border-border pt-4">
  <button
    type="button"
    class="flex items-center gap-2 w-full text-left hover:text-foreground transition-colors"
    onclick={() => { isExpanded = !isExpanded }}
    aria-expanded={isExpanded}
  >
    <Icon
      icon={isExpanded ? 'mdi:chevron-down' : 'mdi:chevron-right'}
      class="w-4 h-4 text-muted-foreground"
    />
    <span class="font-medium text-foreground">OAuth Credentials</span>
    <span class="text-xs text-muted-foreground">(advanced)</span>
  </button>

  {#if isExpanded}
    <div class="mt-3 space-y-3">
      <p class="text-sm text-muted-foreground">
        Override the OAuth Client ID and Secret used for adding Google and Microsoft
        email accounts. Most users should leave these on the shipped defaults.
        Use your own credentials if your organization requires it, or to bypass
        Aerion's quota / verification status.
      </p>
      <OAuthCredsSlotEditor
        configID="google-mail"
        label="Google Mail"
        secretRequired={true}
      />
      <OAuthCredsSlotEditor
        configID="microsoft-mail"
        label="Microsoft Mail"
        secretRequired={false}
      />
    </div>
  {/if}
</section>
