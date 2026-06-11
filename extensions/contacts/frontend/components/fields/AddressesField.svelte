<script lang="ts">
  // AddressesField — repeating address rows (5 sub-inputs + type picker).
  // Microsoft caps at 3 (homeAddress / businessAddress / otherAddress
  // slots in Graph); the `max` constraint disables Add at that point.

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import TypeSelect, { ADDRESS_TYPES } from '$lib/components/kit/TypeSelect.svelte'
  import type { AddressRow, SlotConstraint } from './types'

  interface Props {
    addresses: AddressRow[]
    disabled?: boolean
    constraint?: SlotConstraint
  }

  let { addresses = $bindable([]), disabled = false, constraint = { kind: 'none' } }: Props = $props()

  const atMax = $derived(
    constraint.kind === 'max' && addresses.length >= constraint.max,
  )
  const maxReason = $derived(constraint.kind === 'max' ? constraint.reason : '')

  function add() {
    addresses = [...addresses, { type: '', street: '', city: '', region: '', postcode: '', country: '' }]
  }
  function remove(i: number) {
    addresses = addresses.filter((_, idx) => idx !== i)
  }
</script>

<div>
  <Label>{$_('contacts.edit.addresses')}</Label>
  <div class="space-y-3">
    {#each addresses as a, i (i)}
      <div class="border border-border rounded p-3 space-y-2">
        <div class="flex justify-between items-center">
          <div class="w-32">
            <TypeSelect
              value={a.type}
              onValueChange={(v) => (addresses[i] = { ...addresses[i], type: v })}
              options={ADDRESS_TYPES}
            />
          </div>
          <Button
            variant="ghost"
            size="icon"
            onclick={() => remove(i)}
            disabled={disabled}
            aria-label={$_('contacts.edit.removeAddress')}
          >
            <Icon icon="mdi:close" class="w-4 h-4" />
          </Button>
        </div>
        <Input type="text" bind:value={a.street} placeholder={$_('contacts.edit.addressStreet')} disabled={disabled} />
        <div class="grid grid-cols-2 gap-2">
          <Input type="text" bind:value={a.city} placeholder={$_('contacts.edit.addressCity')} disabled={disabled} />
          <Input type="text" bind:value={a.region} placeholder={$_('contacts.edit.addressRegion')} disabled={disabled} />
        </div>
        <div class="grid grid-cols-2 gap-2">
          <Input type="text" bind:value={a.postcode} placeholder={$_('contacts.edit.addressPostcode')} disabled={disabled} />
          <Input type="text" bind:value={a.country} placeholder={$_('contacts.edit.addressCountry')} disabled={disabled} />
        </div>
      </div>
    {/each}
  </div>
  <Button variant="outline" size="sm" onclick={add} disabled={disabled || atMax} class="mt-2" title={atMax ? maxReason : undefined}>
    <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
    {$_('contacts.edit.addAddress')}
  </Button>
</div>
