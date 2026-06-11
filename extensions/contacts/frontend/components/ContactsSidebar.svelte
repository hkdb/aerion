<script lang="ts">
  import { onMount } from 'svelte'
  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import SourceSidebar from '$lib/components/kit/SourceSidebar.svelte'
  import SourceItem from '$lib/components/kit/SourceItem.svelte'
  import SidebarFooter from '$lib/components/kit/SidebarFooter.svelte'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { contactsView, selectSource } from '$extensions/contacts/frontend/stores/contactsView.svelte'

  interface Props {
    onSelect: () => void
    onOpenSettings?: () => void
  }

  const { onSelect, onOpenSettings }: Props = $props()

  onMount(() => {
    contactSourcesStore.load()
  })

  // Source IDs:
  //   ''                  → all (merged search across every source)
  //   'local'             → all local contacts (manual + collected)
  //   'local:manual'      → user-added local contacts
  //   'local:collected'   → auto-collected from sent mail
  // Plus the user's configured CardDAV sources, each with a UUID id.
  type SidebarItem = {
    id: string
    label: string
    icon: string
  }

  // Reactive — re-runs when locale changes because $_ is referenced inside.
  const sections = $derived.by(() => {
    const builtins: SidebarItem[] = [
      { id: '', label: $_('contacts.sidebar.all'), icon: 'mdi:account-multiple' },
      { id: 'local', label: $_('contacts.sidebar.localAll'), icon: 'mdi:folder-account-outline' },
      { id: 'local:manual', label: $_('contacts.sidebar.localManual'), icon: 'mdi:account-plus-outline' },
      { id: 'local:collected', label: $_('contacts.sidebar.localCollected'), icon: 'mdi:email-outline' },
    ]
    const carddavItems: SidebarItem[] = contactSourcesStore.sources.map(s => ({
      id: s.id,
      label: s.name,
      icon: 'mdi:server',
    }))
    return [
      { items: builtins },
      { heading: $_('contacts.sidebar.sourcesHeading'), items: carddavItems },
    ]
  })

  function pick(id: string) {
    selectSource(id)
    onSelect()
  }
</script>

<SourceSidebar
  title={$_('contacts.sidebar.title')}
  {sections}
  selectedId={contactsView.selectedSourceId}
  onSelect={pick}
>
  {#snippet item(it: SidebarItem, { active })}
    <SourceItem icon={it.icon} label={it.label} {active} onclick={() => pick(it.id)} />
  {/snippet}

  {#snippet sectionEmpty(_section: { heading?: string; items: SidebarItem[] })}
    <p class="mx-4 my-1 text-xs text-muted-foreground">{$_('contacts.sidebar.noSources')}</p>
  {/snippet}

  {#snippet footerContent()}
    <SidebarFooter>
      {#snippet leading()}
        {#if contactSourcesStore.syncing}
          <Icon icon="mdi:sync" class="w-4 h-4 shrink-0 animate-spin" />
          <span class="truncate">{$_('contacts.sidebar.syncing')}</span>
        {/if}
        {#if !contactSourcesStore.syncing}
          <Icon icon="mdi:sync" class="w-4 h-4 shrink-0" />
          <span class="truncate">{$_('contacts.sidebar.idle')}</span>
        {/if}
      {/snippet}
      {#snippet trailing()}
        <button
          class="p-1 rounded hover:bg-muted/40"
          title={$_('contacts.sidebar.settings')}
          onclick={() => onOpenSettings?.()}
          type="button"
          aria-label={$_('contacts.sidebar.settings')}
        >
          <Icon icon="mdi:cog-outline" class="w-4 h-4" />
        </button>
      {/snippet}
    </SidebarFooter>
  {/snippet}
</SourceSidebar>
