<script lang="ts">
  // PhonesField — repeating phone rows. Honors `maxByType` constraint:
  // when set (Microsoft: mobile capped at 1), the second mobile row is
  // visually flagged and parent's validate() must block save (see
  // ContactFieldsForm.validate).

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import TypeSelect, { PHONE_TYPES } from '$lib/components/kit/TypeSelect.svelte'
  import type { PhoneRow, SlotConstraint } from './types'

  interface Props {
    phones: PhoneRow[]
    disabled?: boolean
    constraint?: SlotConstraint
  }

  let { phones = $bindable([]), disabled = false, constraint = { kind: 'none' } }: Props = $props()

  const atMax = $derived(
    constraint.kind === 'max' && phones.length >= constraint.max,
  )
  const maxReason = $derived(constraint.kind === 'max' ? constraint.reason : '')

  // Phones whose type matches a maxByType constraint and exceed the cap.
  // Index-keyed; used to render the inline warning badge.
  const overByType = $derived.by(() => {
    const result = new Set<number>()
    if (constraint.kind !== 'maxByType') return result
    const target = constraint.type.toLowerCase()
    let count = 0
    phones.forEach((p, idx) => {
      if (p.type.toLowerCase() === target) {
        count++
        if (count > constraint.max) {
          result.add(idx)
        }
      }
    })
    return result
  })
  const overByTypeReason = $derived(constraint.kind === 'maxByType' ? constraint.reason : '')

  function add() {
    phones = [...phones, { number: '', type: '', isPrimary: phones.length === 0 }]
  }
  function remove(i: number) {
    phones = phones.filter((_, idx) => idx !== i)
  }
  function setPrimary(i: number) {
    phones = phones.map((p, idx) => ({ ...p, isPrimary: idx === i }))
  }
</script>

<div>
  <Label>{$_('contacts.edit.phones')}</Label>
  <div class="space-y-2">
    {#each phones as p, i (i)}
      <div class="flex gap-2 items-center">
        <Input
          type="tel"
          bind:value={p.number}
          placeholder={$_('contacts.edit.phonePlaceholder')}
          disabled={disabled}
        />
        <div class="w-32">
          <TypeSelect
            value={p.type}
            onValueChange={(v) => (phones[i] = { ...phones[i], type: v })}
            options={PHONE_TYPES}
          />
        </div>
        <label class="flex items-center gap-1 text-xs cursor-pointer" title={$_('contacts.common.primaryTooltip')}>
          <input
            type="radio"
            name="phone-primary"
            checked={p.isPrimary}
            onchange={() => setPrimary(i)}
          />
          <span>{$_('contacts.common.primaryLabel')}</span>
        </label>
        <Button
          variant="ghost"
          size="icon"
          onclick={() => remove(i)}
          disabled={disabled}
          aria-label={$_('contacts.edit.removePhone')}
        >
          <Icon icon="mdi:close" class="w-4 h-4" />
        </Button>
      </div>
      {#if overByType.has(i)}
        <p class="text-xs text-destructive ml-1" role="alert">{overByTypeReason}</p>
      {/if}
    {/each}
  </div>
  <Button variant="outline" size="sm" onclick={add} disabled={disabled || atMax} class="mt-2" title={atMax ? maxReason : undefined}>
    <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
    {$_('contacts.edit.addPhone')}
  </Button>
</div>
