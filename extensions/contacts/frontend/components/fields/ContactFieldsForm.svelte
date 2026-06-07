<script lang="ts" module>
  import type { FieldConstraints, SourceTypeID } from './types'

  // Per-source slot rule table. Lives in module scope so consumers can also
  // call slotConstraintsFor(sourceType) directly to gate their own UI
  // (e.g., AddContactDialog's save button when phones over a maxByType
  // limit). The Add button gating is inside each repeater component; the
  // save-time guard belongs to validate() below.
  export function slotConstraintsFor(sourceType: SourceTypeID): FieldConstraints {
    if (sourceType === 'microsoft') {
      return {
        emails: { kind: 'none' },
        // Microsoft has only mobilePhone (single string slot) — multiple
        // mobile phones can't be persisted; warn and block save instead of
        // disabling Add (the user may add then change type).
        phones: {
          kind: 'maxByType',
          type: 'mobile',
          max: 1,
          reason:
            'Microsoft Contacts only supports one mobile phone. Change one phone’s type or remove it.',
        },
        // Microsoft has 3 address slots (home, business, other) only.
        addresses: {
          kind: 'max',
          max: 3,
          reason: 'Microsoft Contacts supports up to 3 addresses.',
        },
        // Microsoft has 1 native URL slot (businessHomePage). Aerion's
        // sidecar persists the rest, so the user isn't blocked, but show
        // an info note so they know what other clients will see.
        urls: {
          kind: 'info',
          message:
            'Only the first URL is visible in Outlook.com / other Microsoft clients. Additional URLs persist in Aerion only.',
        },
        impps: { kind: 'none' },
      }
    }
    // Google, CardDAV, Local: no material caps in the current scope.
    return {
      emails: { kind: 'none' },
      phones: { kind: 'none' },
      addresses: { kind: 'none' },
      urls: { kind: 'none' },
      impps: { kind: 'none' },
    }
  }
</script>

<script lang="ts">
  // ContactFieldsForm — bundles all field sub-components + scalar inputs in
  // the canonical Edit-dialog order. Used by both AddContactDialog and
  // ContactEditDialog so the two dialogs stay in lockstep.
  //
  // Parent owns the source-of-truth state via bind: props. Validation
  // (e.g., "at least one valid email" or "Microsoft mobile-count rule")
  // is exposed via slotConstraintsFor() — parents call it to make their
  // own save-time decisions.

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import PhotoField from './PhotoField.svelte'
  import EmailsField from './EmailsField.svelte'
  import PhonesField from './PhonesField.svelte'
  import AddressesField from './AddressesField.svelte'
  import URLsField from './URLsField.svelte'
  import IMPPsField from './IMPPsField.svelte'
  import type {
    EmailRow,
    PhoneRow,
    AddressRow,
    URLRow,
    IMPPRow,
    PhotoState,
  } from './types'

  interface Props {
    nameInput: string
    nicknameInput: string
    orgInput: string
    titleInput: string
    noteInput: string
    bdayInput: string
    categoriesInput: string
    emails: EmailRow[]
    phones: PhoneRow[]
    addresses: AddressRow[]
    urls: URLRow[]
    impps: IMPPRow[]
    photo: PhotoState
    errors?: Record<string, string>
    saving?: boolean
    sourceType?: SourceTypeID
    // When provided, picks which scalar inputs render. Add dialog hides
    // nickname/org/title/categories/bday/note inside a collapsible (see
    // AddContactDialog); Edit dialog shows everything inline. Default
    // (everything visible) matches Edit-dialog parity.
    showAllScalars?: boolean
  }

  let {
    nameInput = $bindable(''),
    nicknameInput = $bindable(''),
    orgInput = $bindable(''),
    titleInput = $bindable(''),
    noteInput = $bindable(''),
    bdayInput = $bindable(''),
    categoriesInput = $bindable(''),
    emails = $bindable([]),
    phones = $bindable([]),
    addresses = $bindable([]),
    urls = $bindable([]),
    impps = $bindable([]),
    photo = $bindable({ data: '', mediaType: '', url: '' }),
    errors = {},
    saving = false,
    sourceType = '',
    showAllScalars = true,
  }: Props = $props()

  const constraints = $derived(slotConstraintsFor(sourceType))
  const primaryEmailForAvatar = $derived(
    emails.find((e) => e.isPrimary)?.email ?? emails[0]?.email ?? '',
  )
</script>

<div class="space-y-5">
  <!-- Photo -->
  <PhotoField
    bind:photo
    nameForAvatar={nameInput}
    emailForAvatar={primaryEmailForAvatar}
    disabled={saving}
  />

  <!-- Display name -->
  <div>
    <Label for="cf-name">{$_('contacts.edit.name')}</Label>
    <Input
      id="cf-name"
      type="text"
      bind:value={nameInput}
      disabled={saving}
      aria-invalid={errors.name ? 'true' : undefined}
    />
    {#if errors.name}
      <p class="text-xs text-destructive mt-1">{errors.name}</p>
    {/if}
  </div>

  {#if showAllScalars}
    <!-- Nickname -->
    <div>
      <Label for="cf-nickname">{$_('contacts.edit.nickname')}</Label>
      <Input id="cf-nickname" type="text" bind:value={nicknameInput} disabled={saving} />
    </div>
  {/if}

  <!-- Emails -->
  <EmailsField bind:emails errors={errors} disabled={saving} constraint={constraints.emails} />

  <!-- Phones -->
  <PhonesField bind:phones disabled={saving} constraint={constraints.phones} />

  <!-- Addresses -->
  <AddressesField bind:addresses disabled={saving} constraint={constraints.addresses} />

  {#if showAllScalars}
    <!-- Org / Title -->
    <div class="grid grid-cols-2 gap-3">
      <div>
        <Label for="cf-org">{$_('contacts.edit.org')}</Label>
        <Input id="cf-org" type="text" bind:value={orgInput} disabled={saving} />
      </div>
      <div>
        <Label for="cf-title">{$_('contacts.edit.titleField')}</Label>
        <Input id="cf-title" type="text" bind:value={titleInput} disabled={saving} />
      </div>
    </div>
  {/if}

  <!-- URLs -->
  <URLsField bind:urls disabled={saving} constraint={constraints.urls} />

  <!-- IMPPs -->
  <IMPPsField bind:impps disabled={saving} constraint={constraints.impps} />

  {#if showAllScalars}
    <!-- Categories -->
    <div>
      <Label for="cf-categories">{$_('contacts.edit.categoriesLabel')}</Label>
      <Input
        id="cf-categories"
        type="text"
        bind:value={categoriesInput}
        placeholder={$_('contacts.edit.categoriesPlaceholder')}
        disabled={saving}
      />
    </div>

    <!-- Birthday -->
    <div>
      <Label for="cf-bday">{$_('contacts.edit.bday')}</Label>
      <Input id="cf-bday" type="date" bind:value={bdayInput} disabled={saving} />
    </div>

    <!-- Note -->
    <div>
      <Label for="cf-note">{$_('contacts.edit.note')}</Label>
      <textarea
        id="cf-note"
        bind:value={noteInput}
        disabled={saving}
        rows={3}
        class="flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring resize-y"
      ></textarea>
    </div>
  {/if}
</div>
