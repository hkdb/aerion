<script lang="ts">
  // IMPPsField — repeating IM-handle rows with type picker. No constraints
  // surfaced today; the field component still accepts the constraint prop
  // for symmetry so ContactFieldsForm can stay uniform.

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import TypeSelect, { IMPP_TYPES } from '$lib/components/kit/TypeSelect.svelte'
  import type { IMPPRow, SlotConstraint } from './types'

  interface Props {
    impps: IMPPRow[]
    disabled?: boolean
    constraint?: SlotConstraint
  }

  // constraint accepted for shape parity with other repeaters; not currently
  // applied (no provider gates IMPPs today).
  let { impps = $bindable([]), disabled = false }: Props = $props()

  function add() {
    impps = [...impps, { handle: '', type: '' }]
  }
  function remove(i: number) {
    impps = impps.filter((_, idx) => idx !== i)
  }
</script>

<div>
  <Label>{$_('contacts.edit.impps')}</Label>
  <div class="space-y-2">
    {#each impps as im, i (i)}
      <div class="flex gap-2 items-center">
        <Input
          type="text"
          bind:value={im.handle}
          placeholder={$_('contacts.edit.imppPlaceholder')}
          disabled={disabled}
        />
        <div class="w-32">
          <TypeSelect
            value={im.type}
            onValueChange={(v) => (impps[i] = { ...impps[i], type: v })}
            options={IMPP_TYPES}
          />
        </div>
        <Button
          variant="ghost"
          size="icon"
          onclick={() => remove(i)}
          disabled={disabled}
          aria-label={$_('contacts.edit.removeImpp')}
        >
          <Icon icon="mdi:close" class="w-4 h-4" />
        </Button>
      </div>
    {/each}
  </div>
  <Button variant="outline" size="sm" onclick={add} disabled={disabled} class="mt-2">
    <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
    {$_('contacts.edit.addImpp')}
  </Button>
</div>
