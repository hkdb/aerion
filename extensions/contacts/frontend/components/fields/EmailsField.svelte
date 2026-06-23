<script lang="ts">
  // EmailsField — repeating email rows with TypeSelect, primary radio,
  // remove button, and Add button. Shared by AddContactDialog and
  // ContactEditDialog. Constraint prop is the per-source slot rule;
  // currently emails are never gated, but the wiring is in place for
  // future provider-specific rules.

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import TypeSelect, { EMAIL_TYPES } from '$lib/components/kit/TypeSelect.svelte'
  import type { EmailRow, SlotConstraint } from './types'

  interface Props {
    emails: EmailRow[]
    errors?: Record<string, string>
    disabled?: boolean
    constraint?: SlotConstraint
  }

  let { emails = $bindable([]), errors = {}, disabled = false, constraint = { kind: 'none' } }: Props = $props()

  const atMax = $derived(
    constraint.kind === 'max' && emails.length >= constraint.max,
  )
  const maxReason = $derived(constraint.kind === 'max' ? constraint.reason : '')

  function add() {
    emails = [...emails, { email: '', type: '', isPrimary: emails.length === 0 }]
  }
  function remove(i: number) {
    emails = emails.filter((_, idx) => idx !== i)
  }
  function setPrimary(i: number) {
    emails = emails.map((e, idx) => ({ ...e, isPrimary: idx === i }))
  }
</script>

<div>
  <Label>{$_('contacts.edit.emails')}</Label>
  <div class="space-y-2">
    {#each emails as e, i (i)}
      <div class="flex gap-2 items-start">
        <div class="flex-1">
          <Input
            type="email"
            bind:value={e.email}
            placeholder={$_('contacts.edit.emailPlaceholder')}
            disabled={disabled}
            aria-invalid={errors[`email-${i}`] ? 'true' : undefined}
          />
          {#if errors[`email-${i}`]}
            <p class="text-xs text-destructive mt-1">{errors[`email-${i}`]}</p>
          {/if}
        </div>
        <div class="w-32">
          <TypeSelect
            value={e.type}
            onValueChange={(v) => (emails[i] = { ...emails[i], type: v })}
            options={EMAIL_TYPES}
          />
        </div>
        <label class="flex items-center gap-1 text-xs cursor-pointer pt-2" title={$_('contacts.common.primaryTooltip')}>
          <input
            type="radio"
            name="email-primary"
            checked={e.isPrimary}
            onchange={() => setPrimary(i)}
          />
          <span>{$_('contacts.common.primaryLabel')}</span>
        </label>
        <Button
          variant="ghost"
          size="icon"
          onclick={() => remove(i)}
          disabled={disabled}
          aria-label={$_('contacts.edit.removeEmail')}
        >
          <Icon icon="mdi:close" class="w-4 h-4" />
        </Button>
      </div>
    {/each}
  </div>
  <Button variant="outline" size="sm" onclick={add} disabled={disabled || atMax} class="mt-2" title={atMax ? maxReason : undefined}>
    <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
    {$_('contacts.edit.addEmail')}
  </Button>
</div>
