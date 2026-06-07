<!--
  ContactEditDialog — multi-field Edit dialog for local + CardDAV +
  Google + Microsoft contacts.

  Layout owns the source/header/buttons; the actual field section is
  rendered by the shared <ContactFieldsForm> component which AddContactDialog
  also uses. Per-source slot constraints (Microsoft: 3 addresses, 1 mobile
  phone, single-URL info banner) flow through the form via the sourceType
  prop derived from the contact's source.
-->
<script lang="ts">
  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import { updateContact } from '$extensions/contacts/frontend/stores/contactsView.svelte'
  import { contactSourcesStore } from '$extensions/contacts/frontend/stores/contactSources.svelte'
  import { toasts } from '$lib/stores/toast'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import ContactFieldsForm, { slotConstraintsFor } from './fields/ContactFieldsForm.svelte'
  import type {
    EmailRow,
    PhoneRow,
    AddressRow,
    URLRow,
    IMPPRow,
    PhotoState,
    SourceTypeID,
  } from './fields/types'
  // @ts-ignore - wailsjs bindings
  import type { v1 } from '$wailsjs/go/models'

  interface Props {
    open: boolean
    contact: v1.Contact | null
    onClose?: () => void
  }

  let { open = $bindable(false), contact, onClose }: Props = $props()

  // Form state — same shape AddContactDialog uses, owned here so save()
  // can read everything in one place.
  let nameInput = $state('')
  let nicknameInput = $state('')
  let orgInput = $state('')
  let titleInput = $state('')
  let noteInput = $state('')
  let bdayInput = $state('')
  let emails = $state<EmailRow[]>([])
  let phones = $state<PhoneRow[]>([])
  let addresses = $state<AddressRow[]>([])
  let urls = $state<URLRow[]>([])
  let impps = $state<IMPPRow[]>([])
  let categoriesInput = $state('')
  let photo = $state<PhotoState>({ data: '', mediaType: '', url: '' })

  let saving = $state(false)
  let errors = $state<Record<string, string>>({})

  // Hydrate state from `contact` each time the dialog opens. Reading from
  // `contact` here (not inside reactive markup) prevents a flash of stale
  // data on dialog reopen.
  $effect(() => {
    if (open && contact) {
      nameInput = contact.name ?? ''
      nicknameInput = contact.nickname ?? ''
      orgInput = contact.org ?? ''
      titleInput = contact.title ?? ''
      noteInput = contact.note ?? ''
      bdayInput = contact.bday ?? ''
      categoriesInput = (contact.categories ?? []).join(', ')

      // Emails: prefer emailItems (carries type + isPrimary). Fall back to
      // the flat emails list when emailItems is empty (records that haven't
      // been re-synced under 2b.2.a yet).
      if (contact.emailItems && contact.emailItems.length > 0) {
        emails = contact.emailItems.map((e) => ({
          email: e.email,
          type: e.type ?? '',
          isPrimary: e.isPrimary ?? false,
        }))
      } else if (contact.emails && contact.emails.length > 0) {
        emails = contact.emails.map((e, i) => ({ email: e, type: '', isPrimary: i === 0 }))
      } else {
        emails = []
      }

      phones = (contact.phones ?? []).map((p) => ({
        number: p.number,
        type: p.type ?? '',
        isPrimary: p.isPrimary ?? false,
      }))
      addresses = (contact.addresses ?? []).map((a) => ({
        type: a.type ?? '',
        street: a.street ?? '',
        city: a.city ?? '',
        region: a.region ?? '',
        postcode: a.postcode ?? '',
        country: a.country ?? '',
      }))
      urls = (contact.urls ?? []).map((u) => ({ url: u.url, type: u.type ?? '' }))
      impps = (contact.impps ?? []).map((i) => ({ handle: i.handle, type: i.type ?? '' }))
      photo = {
        data: contact.photoData ?? '',
        mediaType: contact.photoMediaType ?? '',
        url: contact.photoUrl ?? '',
      }
      errors = {}
    }
  })

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  const recordID = $derived(contact?.id ?? '')

  // Derive the SourceTypeID for the constraint dispatcher. ContactSourceID
  // on the contact is the carddav.Source.id; look up its type in the
  // sources store. Local contacts have sourceId="local" or unset.
  const sourceType: SourceTypeID = $derived.by(() => {
    const sid = contact?.sourceId ?? ''
    if (!sid || sid === 'local' || sid.startsWith('local')) return 'local'
    const s = contactSourcesStore.sources.find(s => s.id === sid)
    if (!s) return ''
    if (s.type === 'carddav' || s.type === 'google' || s.type === 'microsoft') {
      return s.type
    }
    return ''
  })

  function isValidEmail(s: string): boolean {
    const t = s.trim().toLowerCase()
    if (t === '') return false
    if (!t.includes('@') || t.indexOf('@') === t.length - 1 || t.startsWith('@')) return false
    return true
  }

  function isNonEmptyAddress(a: AddressRow): boolean {
    return !!(a.street || a.city || a.region || a.postcode || a.country)
  }

  function validate(): boolean {
    const next: Record<string, string> = {}
    if (nameInput.trim() === '') {
      next.name = $_('contacts.edit.nameRequired')
    }
    emails.forEach((e, i) => {
      if (e.email.trim() !== '' && !isValidEmail(e.email)) {
        next[`email-${i}`] = $_('contacts.edit.emailInvalid')
      }
    })

    // Per-source slot guards — symmetric with AddContactDialog.
    const constraints = slotConstraintsFor(sourceType)
    if (constraints.phones.kind === 'maxByType') {
      const c = constraints.phones
      const target = c.type.toLowerCase()
      const count = phones.filter(p => p.type.toLowerCase() === target).length
      if (count > c.max) {
        toasts.error(c.reason)
        return false
      }
    }

    errors = next
    return Object.keys(next).length === 0
  }

  async function save() {
    if (!recordID) return
    if (!validate()) return
    saving = true
    try {
      // Wails-generated `v1.ContactPatch` is a class; the runtime accepts
      // plain objects since marshaling is JSON-based, so cast through
      // `unknown` to type-check at the call site without instantiation.
      const patch = ({
        name: nameInput.trim(),
        nickname: nicknameInput.trim(),
        org: orgInput.trim(),
        title: titleInput.trim(),
        note: noteInput.trim(),
        bday: bdayInput.trim(),
        emails: emails
          .filter((e) => e.email.trim() !== '')
          .map((e) => ({ email: e.email.trim().toLowerCase(), type: e.type, isPrimary: e.isPrimary })),
        phones: phones
          .filter((p) => p.number.trim() !== '')
          .map((p) => ({ number: p.number.trim(), type: p.type, isPrimary: p.isPrimary })),
        addresses: addresses.filter(isNonEmptyAddress).map((a) => ({
          type: a.type,
          street: a.street.trim(),
          city: a.city.trim(),
          region: a.region.trim(),
          postcode: a.postcode.trim(),
          country: a.country.trim(),
        })),
        urls: urls
          .filter((u) => u.url.trim() !== '')
          .map((u) => ({ url: u.url.trim(), type: u.type })),
        impps: impps
          .filter((i) => i.handle.trim() !== '')
          .map((i) => ({ handle: i.handle.trim(), type: i.type })),
        categories: categoriesInput
          .split(',')
          .map((c) => c.trim())
          .filter((c) => c !== ''),
        photo: {
          data: photo.data,
          mediaType: photo.mediaType,
          url: photo.url,
        },
      }) as unknown as v1.ContactPatch
      await updateContact(recordID, patch)
      toasts.success($_('contacts.toast.updated'))
      close()
    } catch (err) {
      console.error('Failed to update contact:', err)
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(`${$_('contacts.toast.failedUpdate')}: ${msg}`)
    } finally {
      saving = false
    }
  }

  function close() {
    open = false
    onClose?.()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-xl max-h-[85vh] overflow-y-auto">
    <Dialog.Header>
      <Dialog.Title>{$_('contacts.edit.title')}</Dialog.Title>
    </Dialog.Header>

    <div class="mt-2">
      <ContactFieldsForm
        bind:nameInput
        bind:nicknameInput
        bind:orgInput
        bind:titleInput
        bind:noteInput
        bind:bdayInput
        bind:categoriesInput
        bind:emails
        bind:phones
        bind:addresses
        bind:urls
        bind:impps
        bind:photo
        errors={errors}
        saving={saving}
        sourceType={sourceType}
      />
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4 sticky bottom-0 bg-background">
      <Button variant="ghost" onclick={close} disabled={saving}>{$_('contacts.common.cancel')}</Button>
      <Button onclick={save} disabled={saving || !recordID}>
        {#if saving}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
        {/if}
        {$_('contacts.common.save')}
      </Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
