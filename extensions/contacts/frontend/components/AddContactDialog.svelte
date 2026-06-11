<script lang="ts">
  // AddContactDialog — creates a new contact in the user-picked source.
  //
  // v0.3.0-dev expansion: previously email + name only; now hosts the
  // shared ContactFieldsForm so users can add a contact with phones,
  // addresses, URLs, IMPPs, photo, and the rest of the rich fields in
  // a single step. Backend dispatch by SourceID:
  //   - local sentinel       → CreateContact's local-manual path
  //   - CardDAV source UUID  → CreateContact's CardDAV path
  //   - Google/MS source ID  → CreateContact's Google/MS provider paths
  // (local sentinel is ALWAYS 'local:manual' regardless of which local
  // sub-view the sidebar is showing; the 'collected' kind is reserved for
  // sent-mail collection).

  import { untrack } from 'svelte'
  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import * as Select from '$lib/components/ui/select'
  import { Button } from '$lib/components/ui/button'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import Icon from '@iconify/svelte'
  import { contactsView, createContact, listAddressbooks } from '$extensions/contacts/frontend/stores/contactsView.svelte'
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
  import { v1 } from '$wailsjs/go/models'

  interface Props {
    open: boolean
    onClose?: () => void
    onCreated?: (id: string, sourceId: string) => void
  }

  let { open = $bindable(false), onClose, onCreated }: Props = $props()

  // Local sentinel — single underlying value for the "Local" picker option.
  // 'local:manual' is the only writable local kind; 'local' (parent) and
  // 'local:collected' are filter values, not write targets.
  const LOCAL_VALUE = 'local:manual'

  // Form state.
  let sourceValue = $state<string>(LOCAL_VALUE)
  let addressbookValue = $state<string>('')
  let saving = $state(false)
  let errors = $state<Record<string, string>>({})

  // Field-form state — scalar + repeating, mirrors ContactEditDialog.
  let nameInput = $state('')
  let nicknameInput = $state('')
  let orgInput = $state('')
  let titleInput = $state('')
  let noteInput = $state('')
  let bdayInput = $state('')
  let categoriesInput = $state('')
  let emails = $state<EmailRow[]>([{ email: '', type: '', isPrimary: true }])
  let phones = $state<PhoneRow[]>([])
  let addresses = $state<AddressRow[]>([])
  let urls = $state<URLRow[]>([])
  let impps = $state<IMPPRow[]>([])
  let photo = $state<PhotoState>({ data: '', mediaType: '', url: '' })

  // Addressbook cache for the currently-picked external source. Refreshed
  // on source change; null until first fetch completes.
  let addressbooks = $state<v1.Addressbook[]>([])
  let loadingAddressbooks = $state<boolean>(false)

  // Picker options. Any writable external source qualifies — CardDAV,
  // Google, or Microsoft. The backend's CreateContact dispatches by
  // source.Type to the matching provider create handler.
  type PickerOption = { value: string; label: string }
  const sourceOptions: PickerOption[] = $derived.by(() => {
    const opts: PickerOption[] = [
      { value: LOCAL_VALUE, label: $_('contacts.add.localOption') },
    ]
    for (const s of contactSourcesStore.sources) {
      if (!s.writable) continue
      if (s.type === 'carddav' || s.type === 'google' || s.type === 'microsoft') {
        opts.push({ value: s.id, label: s.name })
      }
    }
    return opts
  })

  function findOption(value: string): PickerOption | undefined {
    return sourceOptions.find(o => o.value === value)
  }

  // Derive the SourceTypeID for ContactFieldsForm constraint gating. Local
  // sentinel → 'local'; external source UUID → look up its type. Empty
  // string when the source isn't (yet) in contactSourcesStore.
  const sourceType: SourceTypeID = $derived.by(() => {
    if (!sourceValue || sourceValue === LOCAL_VALUE || sourceValue.startsWith('local')) {
      return 'local'
    }
    const s = contactSourcesStore.sources.find(s => s.id === sourceValue)
    if (!s) return ''
    if (s.type === 'carddav' || s.type === 'google' || s.type === 'microsoft') {
      return s.type
    }
    return ''
  })

  // Auto-fill from the sidebar's current source when the dialog opens.
  function autoFillFromSidebar(): string {
    const sel = contactsView.selectedSourceId
    if (!sel || sel === 'local' || sel.startsWith('local:')) return LOCAL_VALUE
    const match = sourceOptions.find(o => o.value === sel)
    return match ? sel : LOCAL_VALUE
  }

  // Reset state each time the dialog opens. Wrapped in untrack so it
  // depends only on `open` — otherwise contactSourcesStore.load() (which
  // reassigns sources) re-triggers the effect and resets all inputs
  // mid-typing.
  $effect(() => {
    if (!open) return
    untrack(() => {
      contactSourcesStore.load()
      sourceValue = autoFillFromSidebar()
      addressbookValue = ''
      nameInput = ''
      nicknameInput = ''
      orgInput = ''
      titleInput = ''
      noteInput = ''
      bdayInput = ''
      categoriesInput = ''
      emails = [{ email: '', type: '', isPrimary: true }]
      phones = []
      addresses = []
      urls = []
      impps = []
      photo = { data: '', mediaType: '', url: '' }
      errors = {}
      saving = false
    })
  })

  // Fetch addressbooks whenever the user picks an external source. Local
  // doesn't need it. .catch is critical: an unhandled rejection on this
  // effect's promise has been observed to break Svelte reactivity and
  // freeze the dialog inputs.
  $effect(() => {
    if (!open) return
    if (sourceValue === LOCAL_VALUE) {
      addressbooks = []
      addressbookValue = ''
      return
    }
    loadingAddressbooks = true
    listAddressbooks(sourceValue)
      .then(abs => {
        addressbooks = abs
        addressbookValue = abs.length > 0 ? abs[0].id : ''
      })
      .catch(err => {
        console.error('Failed to load addressbooks for source', sourceValue, err)
        addressbooks = []
        addressbookValue = ''
        toasts.error($_('contacts.toast.failedAdd'))
      })
      .finally(() => {
        loadingAddressbooks = false
      })
  })

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  function isValidEmail(s: string): boolean {
    const t = s.trim().toLowerCase()
    if (t === '') return false
    if (!t.includes('@') || t.indexOf('@') === t.length - 1 || t.startsWith('@')) return false
    return true
  }

  function validate(): boolean {
    const next: Record<string, string> = {}

    // Need at least one valid email — that's the contact's identity.
    const nonEmpty = emails.filter(e => e.email.trim() !== '')
    if (nonEmpty.length === 0) {
      next.email = $_('contacts.add.errorEmailRequired')
    } else {
      emails.forEach((e, i) => {
        if (e.email.trim() !== '' && !isValidEmail(e.email)) {
          next[`email-${i}`] = $_('contacts.add.errorEmailInvalid')
        }
      })
    }

    // Per-source slot guards. The repeater UI gates Add buttons, but
    // type-based caps (Microsoft: 1 mobile phone) can be triggered by
    // changing an existing row's type after adding it — surface here.
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

  function close() {
    open = false
    onClose?.()
  }

  function handleSaveError(err: unknown) {
    const msg = (err as Error)?.message ?? String(err)
    if (/already exists/i.test(msg) || /UNIQUE constraint/i.test(msg)) {
      errors = { ...errors, email: $_('contacts.add.errorEmailExists') }
      return
    }
    console.error('Failed to create contact:', err)
    toasts.error(`${$_('contacts.toast.failedAdd')}: ${msg}`)
  }

  // Build the rich ContactCreateInput from form state. Empty repeater rows
  // are filtered before send. The first non-empty email is treated as the
  // legacy primary if no row carries IsPrimary explicitly.
  function buildCreateInput(): v1.ContactCreateInput {
    const filteredEmails = emails
      .filter(e => e.email.trim() !== '')
      .map(e => ({ email: e.email.trim().toLowerCase(), type: e.type, isPrimary: e.isPrimary }))
    if (!filteredEmails.some(e => e.isPrimary) && filteredEmails.length > 0) {
      filteredEmails[0].isPrimary = true
    }
    const primaryEmail = filteredEmails.find(e => e.isPrimary)?.email ?? filteredEmails[0]?.email ?? ''
    const photoForApi = photo.data ? { data: photo.data, mediaType: photo.mediaType, url: '' } : undefined
    const categories = categoriesInput
      .split(',')
      .map(c => c.trim())
      .filter(c => c.length > 0)

    return v1.ContactCreateInput.createFrom({
      sourceId: sourceValue,
      addressbookId: sourceValue === LOCAL_VALUE ? '' : addressbookValue,
      email: primaryEmail,
      name: nameInput.trim(),
      nickname: nicknameInput.trim(),
      org: orgInput.trim(),
      title: titleInput.trim(),
      note: noteInput.trim(),
      bday: bdayInput.trim(),
      categories: categories.length > 0 ? categories : undefined,
      emails: filteredEmails.length > 0 ? filteredEmails : undefined,
      phones: phones
        .filter(p => p.number.trim() !== '')
        .map(p => ({ number: p.number.trim(), type: p.type, isPrimary: p.isPrimary })),
      addresses: addresses
        .filter(a => a.street || a.city || a.region || a.postcode || a.country)
        .map(a => ({
          type: a.type,
          street: a.street.trim(),
          city: a.city.trim(),
          region: a.region.trim(),
          postcode: a.postcode.trim(),
          country: a.country.trim(),
        })),
      urls: urls
        .filter(u => u.url.trim() !== '')
        .map(u => ({ url: u.url.trim(), type: u.type })),
      impps: impps
        .filter(i => i.handle.trim() !== '')
        .map(i => ({ handle: i.handle.trim(), type: i.type })),
      photo: photoForApi,
    })
  }

  async function save() {
    if (!validate()) return
    saving = true
    try {
      const input = buildCreateInput()
      const id = await createContact(input)
      toasts.success($_('contacts.toast.added'))
      onCreated?.(id, sourceValue)
      close()
    } catch (err) {
      handleSaveError(err)
    } finally {
      saving = false
    }
  }

  let showAddressbookPicker = $derived(
    sourceValue !== LOCAL_VALUE && addressbooks.length > 1,
  )
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-xl max-h-[85vh] overflow-y-auto">
    <Dialog.Header>
      <Dialog.Title>{$_('contacts.add.title')}</Dialog.Title>
      <Dialog.Description>
        {$_('contacts.add.description')}
      </Dialog.Description>
    </Dialog.Header>

    <div class="space-y-5 mt-2">
      <!-- Source picker -->
      <div>
        <Label>{$_('contacts.add.sourceLabel')}</Label>
        <Select.Root value={sourceValue} onValueChange={(v) => { sourceValue = v }} disabled={saving}>
          <Select.Trigger>
            <Select.Value placeholder={$_('contacts.add.sourcePlaceholder')}>
              {findOption(sourceValue)?.label || $_('contacts.add.sourcePlaceholder')}
            </Select.Value>
          </Select.Trigger>
          <Select.Content>
            {#each sourceOptions as opt (opt.value)}
              <Select.Item value={opt.value} label={opt.label} />
            {/each}
          </Select.Content>
        </Select.Root>
      </div>

      <!-- Addressbook sub-picker -->
      {#if showAddressbookPicker}
        <div>
          <Label>{$_('contacts.add.addressbookLabel')}</Label>
          <Select.Root value={addressbookValue} onValueChange={(v) => { addressbookValue = v }} disabled={saving || loadingAddressbooks}>
            <Select.Trigger>
              <Select.Value placeholder={$_('contacts.add.addressbookPlaceholder')}>
                {addressbooks.find(a => a.id === addressbookValue)?.name || $_('contacts.add.addressbookPlaceholder')}
              </Select.Value>
            </Select.Trigger>
            <Select.Content>
              {#each addressbooks as ab (ab.id)}
                <Select.Item value={ab.id} label={ab.name} />
              {/each}
            </Select.Content>
          </Select.Root>
        </div>
      {/if}

      {#if errors.email}
        <p class="text-xs text-destructive">{errors.email}</p>
      {/if}

      <!-- Rich field form -->
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
      <Button onclick={save} disabled={saving}>
        {#if saving}
          <Icon icon="mdi:loading" class="w-4 h-4 mr-1 animate-spin" />
        {/if}
        {$_('contacts.common.save')}
      </Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
