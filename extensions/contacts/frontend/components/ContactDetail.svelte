<script lang="ts">
  import { _ } from 'svelte-i18n'
  import DetailPane from '$lib/components/kit/DetailPane.svelte'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  import { Button } from '$lib/components/ui/button'
  import ConfirmDialog from '$lib/components/kit/ConfirmDialog.svelte'
  import Icon from '@iconify/svelte'
  import { contactsView, deleteLocalContact } from '$extensions/contacts/frontend/stores/contactsView.svelte'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import type { v1 } from '$wailsjs/go/models'

  // Edit-dialog state lives in ContactsPane (hoisted so the 'e' keyboard
  // shortcut can open it from anywhere within the pane). The button below
  // calls onEdit; ContactsPane owns the dialog itself.
  interface Props {
    onEdit?: (contact: v1.Contact) => void
  }
  let { onEdit }: Props = $props()

  let contact = $derived(contactsView.detail)
  let primaryEmail = $derived(contact && contact.emails && contact.emails.length > 0 ? contact.emails[0] : '')

  // Local records are always writable. CardDAV records are writable when the
  // source's `writable` flag is enabled (Settings → source → "Enable write
  // access"). Google / Microsoft sources gain write capability in 2b.3
  // alongside the provider-specific write paths.
  let isWritable = $derived(
    contact?.sourceId === 'aerion' || contactSourcesStore.isSourceWritable(contact?.sourceId),
  )

  // Read-only hint discriminator. We want to surface WHY a contact has no
  // Edit/Delete buttons:
  //   - CardDAV non-writable → "Read-only — enable write access in Settings"
  //   - OAuth (Google/Microsoft) → "Read-only — write capability coming in a
  //     future release"
  //   - Local → never read-only.
  let readonlyKind = $derived.by<'none' | 'carddav' | 'oauth'>(() => {
    if (!contact || isWritable) return 'none'
    if (!contact.sourceId || contact.sourceId === 'aerion') return 'none'
    const src = contactSourcesStore.sources.find(s => s.id === contact.sourceId)
    if (src?.type === 'google' || src?.type === 'microsoft') return 'oauth'
    return 'carddav'
  })

  let showDeleteConfirm = $state(false)
  let deleting = $state(false)

  async function copyEmail(email: string) {
    try {
      await navigator.clipboard.writeText(email)
      toasts.success($_('contacts.toast.emailCopied', { values: { email } }))
    } catch {
      toasts.error($_('contacts.toast.emailCopyFailed'))
    }
  }

  function handleKeydown(e: KeyboardEvent, email: string) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      e.stopPropagation()
      copyEmail(email)
    }
  }

  async function confirmDelete() {
    if (!contact) return
    deleting = true
    try {
      await deleteLocalContact(contact.id)
      toasts.success($_('contacts.toast.deleted'))
    } catch (err) {
      console.error('Failed to delete contact:', err)
      toasts.error($_('contacts.toast.failedDelete'))
    } finally {
      deleting = false
    }
  }
</script>

<DetailPane
  empty={!contact}
  emptyIcon="mdi:account-multiple-outline"
  emptyText={$_('contacts.detail.emptyState')}
>
  {#snippet header()}
    {#if contact}
      <Avatar
        email={primaryEmail}
        name={contact.name}
        density="large"
        photoData={contact.photoData}
        photoMediaType={contact.photoMediaType}
      />
      <h1 class="m-0 text-xl font-semibold text-foreground flex-1 min-w-0 truncate">
        {contact.name || $_('contacts.common.unnamed')}
      </h1>
      {#if isWritable}
        <div class="flex items-center gap-1 flex-shrink-0">
          <Button variant="outline" size="sm" onclick={() => { if (contact) onEdit?.(contact) }}>
            <Icon icon="mdi:pencil" class="w-4 h-4 mr-1" />
            {$_('contacts.detail.edit')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="text-destructive hover:text-destructive"
            onclick={() => { showDeleteConfirm = true }}
          >
            <Icon icon="mdi:delete-outline" class="w-4 h-4 mr-1" />
            {$_('contacts.common.delete')}
          </Button>
        </div>
      {:else if readonlyKind !== 'none'}
        <div
          class="flex items-center gap-1.5 flex-shrink-0 text-xs text-muted-foreground"
          title={readonlyKind === 'oauth'
            ? $_('contacts.detail.oauthReadOnlyNote')
            : $_('contacts.detail.cardDAVReadOnlyNote')}
        >
          <Icon icon="mdi:lock-outline" class="w-4 h-4" />
          <span>{$_('contacts.detail.readonlyHint')}</span>
        </div>
      {/if}
    {/if}
  {/snippet}

  {#snippet body()}
    {#if contact}
      <dl class="grid grid-cols-[120px_1fr] gap-y-2 gap-x-4">
        <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.email')}</dt>
        <dd class="m-0 break-words">
          {#if contact.emailItems && contact.emailItems.length > 0}
            {#each contact.emailItems as item (item.email)}
              <div class="flex items-baseline gap-2">
                <span
                  role="button"
                  tabindex="0"
                  class="text-primary hover:underline cursor-pointer"
                  title={$_('contacts.detail.copyTooltip')}
                  onclick={(e) => { e.stopPropagation(); copyEmail(item.email) }}
                  onkeydown={(e) => handleKeydown(e, item.email)}
                >{item.email}</span>
                {#if item.type}
                  <span class="text-xs text-muted-foreground uppercase">{item.type}</span>
                {/if}
                {#if item.isPrimary}
                  <span class="text-xs text-primary">{$_('contacts.common.primary')}</span>
                {/if}
              </div>
            {/each}
          {/if}
          {#if (!contact.emailItems || contact.emailItems.length === 0) && contact.emails && contact.emails.length > 0}
            {#each contact.emails as email (email)}
              <div>
                <span
                  role="button"
                  tabindex="0"
                  class="text-primary hover:underline cursor-pointer"
                  title={$_('contacts.detail.copyTooltip')}
                  onclick={(e) => { e.stopPropagation(); copyEmail(email) }}
                  onkeydown={(e) => handleKeydown(e, email)}
                >{email}</span>
              </div>
            {/each}
          {/if}
        </dd>

        {#if contact.phones && contact.phones.length > 0}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.phone')}</dt>
          <dd class="m-0 break-words text-foreground">
            {#each contact.phones as p (p.number + (p.type ?? ''))}
              <div class="flex items-baseline gap-2">
                <span>{p.number}</span>
                {#if p.type}
                  <span class="text-xs text-muted-foreground uppercase">{p.type}</span>
                {/if}
                {#if p.isPrimary}
                  <span class="text-xs text-primary">{$_('contacts.common.primary')}</span>
                {/if}
              </div>
            {/each}
          </dd>
        {/if}

        {#if contact.addresses && contact.addresses.length > 0}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.address')}</dt>
          <dd class="m-0 break-words text-foreground space-y-2">
            {#each contact.addresses as a, i (i)}
              <div>
                {#if a.type}
                  <span class="text-xs text-muted-foreground uppercase mr-2">{a.type}</span>
                {/if}
                <span>
                  {[a.street, a.city, a.region, a.postcode, a.country].filter(Boolean).join(', ')}
                </span>
              </div>
            {/each}
          </dd>
        {/if}

        {#if contact.org}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.org')}</dt>
          <dd class="m-0 break-words text-foreground">{contact.org}</dd>
        {/if}

        {#if contact.title}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.title')}</dt>
          <dd class="m-0 break-words text-foreground">{contact.title}</dd>
        {/if}

        {#if contact.bday}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.bday')}</dt>
          <dd class="m-0 break-words text-foreground">{contact.bday}</dd>
        {/if}

        {#if contact.nickname}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.nickname')}</dt>
          <dd class="m-0 break-words text-foreground">{contact.nickname}</dd>
        {/if}

        {#if contact.urls && contact.urls.length > 0}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.url')}</dt>
          <dd class="m-0 break-words text-foreground">
            {#each contact.urls as u (u.url + (u.type ?? ''))}
              <div class="flex items-baseline gap-2">
                <a href={u.url} target="_blank" rel="noopener noreferrer" class="text-primary hover:underline">{u.url}</a>
                {#if u.type}
                  <span class="text-xs text-muted-foreground uppercase">{u.type}</span>
                {/if}
              </div>
            {/each}
          </dd>
        {/if}

        {#if contact.impps && contact.impps.length > 0}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.im')}</dt>
          <dd class="m-0 break-words text-foreground">
            {#each contact.impps as i (i.handle + (i.type ?? ''))}
              <div class="flex items-baseline gap-2">
                <span>{i.handle}</span>
                {#if i.type}
                  <span class="text-xs text-muted-foreground uppercase">{i.type}</span>
                {/if}
              </div>
            {/each}
          </dd>
        {/if}

        {#if contact.categories && contact.categories.length > 0}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.categories')}</dt>
          <dd class="m-0 break-words text-foreground">
            <div class="flex flex-wrap gap-1">
              {#each contact.categories as cat (cat)}
                <span class="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground">{cat}</span>
              {/each}
            </div>
          </dd>
        {/if}

        {#if contact.note}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.note')}</dt>
          <dd class="m-0 break-words text-foreground whitespace-pre-wrap">{contact.note}</dd>
        {/if}

        {#if contact.sourceId}
          <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.source')}</dt>
          <dd class="m-0 break-words text-foreground">{contact.sourceId}</dd>
        {/if}

        <dt class="text-sm text-muted-foreground">{$_('contacts.detail.labels.lastUpdated')}</dt>
        <dd class="m-0 text-foreground">
          {contact.updatedAt ? new Date(contact.updatedAt).toLocaleString() : '—'}
        </dd>
      </dl>
    {/if}
  {/snippet}
</DetailPane>

<ConfirmDialog
  bind:open={showDeleteConfirm}
  title={$_('contacts.delete.title')}
  description={contact
    ? contact.sourceId === 'aerion'
      ? $_('contacts.delete.descriptionLocal', { values: { name: contact.name || primaryEmail || $_('contacts.common.unnamed') } })
      : $_('contacts.delete.descriptionCardDAV', { values: { name: contact.name || primaryEmail || $_('contacts.common.unnamed') } })
    : ''}
  confirmLabel={$_('contacts.common.delete')}
  cancelLabel={$_('contacts.common.cancel')}
  variant="destructive"
  loading={deleting}
  onConfirm={confirmDelete}
/>
