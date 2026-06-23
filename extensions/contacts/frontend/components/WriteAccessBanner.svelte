<script lang="ts">
  // WriteAccessBanner — visible at the top of the ContactList whenever the
  // user has a non-writable external contact source (CardDAV, Google,
  // Microsoft). One row per such source, with an Enable button.
  //
  // For CardDAV: the button directly flips the writable flag (basic-auth
  // already grants access; this is a pure preference toggle).
  // For Google / Microsoft: the button opens the WriteAccessAccountPicker
  // dialog, which lists existing matching-provider auth contexts and lets
  // the user pick one to attach the new write grant to.
  //
  // Banner auto-hides when there are no non-writable external sources.

  import Icon from '@iconify/svelte'
  import { _ } from 'svelte-i18n'
  import { Button } from '$lib/components/ui/button'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { contactsView } from '$extensions/contacts/frontend/stores/contactsView.svelte'
  import { toasts } from '$lib/stores/toast'
  import WriteAccessAccountPicker from '$lib/components/oauth/WriteAccessAccountPicker.svelte'
  // @ts-ignore - wailsjs bindings
  import { SetContactSourceWritable } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { v1 } from '$wailsjs/go/models'

  let pendingEnable = $state<Record<string, boolean>>({})

  // Picker state — only one picker can be open at a time.
  let pickerOpen = $state(false)
  let pickerProvider = $state<'google' | 'microsoft'>('google')
  let pickerSourceID = $state('')
  let pickerSourceName = $state('')

  const rows = $derived.by(() => {
    const all = contactSourcesStore.sources.filter(
      (s) =>
        (s.type === 'google' || s.type === 'microsoft' || s.type === 'carddav') &&
        !s.writable,
    )
    const sel = contactsView.selectedSourceId
    if (sel === '') return all
    if (sel === 'local' || sel.startsWith('local:')) return []
    return all.filter((s) => s.id === sel)
  })

  async function enableWriteAccess(source: v1.ContactSource) {
    if (source.type === 'carddav') {
      pendingEnable[source.id] = true
      try {
        await SetContactSourceWritable(source.id, true)
        await contactSourcesStore.load()
        toasts.success(
          $_('contacts.settings.writeAccessEnabled', { values: { name: source.name } }),
        )
      } catch (err) {
        console.error('Enable write access failed:', err)
        toasts.error((err as Error)?.message ?? $_('contacts.settings.writeAccessCanceled'))
      } finally {
        delete pendingEnable[source.id]
      }
      return
    }

    // OAuth path — open the account picker dialog.
    if (source.type !== 'google' && source.type !== 'microsoft') return
    pickerProvider = source.type
    pickerSourceID = source.id
    pickerSourceName = source.name
    pickerOpen = true
  }

  async function onPickerCompleted() {
    await contactSourcesStore.load()
  }

  function providerIcon(type: string): string {
    switch (type) {
      case 'google':
        return 'mdi:google'
      case 'microsoft':
        return 'mdi:microsoft'
      case 'carddav':
        return 'mdi:server'
    }
    return 'mdi:account-multiple-outline'
  }

  function providerLabel(type: string): string {
    switch (type) {
      case 'google':
        return $_('contacts.writeAccessBanner.providerGoogle')
      case 'microsoft':
        return $_('contacts.writeAccessBanner.providerMicrosoft')
      case 'carddav':
        return $_('contacts.writeAccessBanner.providerCardDAV')
    }
    return type
  }
</script>

{#if rows.length > 0}
  <div class="border-b border-border bg-muted/40 px-3 py-2">
    <div class="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
      <Icon icon="mdi:lock-outline" class="w-3.5 h-3.5" />
      <span>{$_('contacts.writeAccessBanner.title')}</span>
    </div>
    <div class="flex flex-wrap gap-2">
      {#each rows as source (source.id)}
        <div class="flex items-center gap-2 rounded-md border border-border bg-background px-2 py-1">
          <Icon icon={providerIcon(source.type)} class="w-4 h-4 text-muted-foreground flex-shrink-0" />
          <span class="text-xs text-foreground truncate max-w-[260px]">
            <span class="font-semibold">{providerLabel(source.type)}</span>
            <span class="text-muted-foreground"> · {source.name}</span>
          </span>
          <Button
            size="sm"
            variant="outline"
            class="h-6 px-2 text-xs"
            onclick={() => enableWriteAccess(source)}
            disabled={pendingEnable[source.id]}
          >
            {#if pendingEnable[source.id]}
              <Icon icon="mdi:loading" class="w-3.5 h-3.5 mr-1 animate-spin" />
            {/if}
            {$_('contacts.writeAccessBanner.enableButton')}
          </Button>
        </div>
      {/each}
    </div>
  </div>
{/if}

<WriteAccessAccountPicker
  bind:open={pickerOpen}
  provider={pickerProvider}
  sourceID={pickerSourceID}
  sourceName={pickerSourceName}
  onCompleted={onPickerCompleted}
/>
