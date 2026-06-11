<script lang="ts">
  // URLsField — repeating URL rows with type picker.
  //
  // Microsoft's constraint is `info` (not hard cap): Graph only stores one
  // URL natively (`businessHomePage`), but the backend persists the full
  // list via the ms_field_sidecar so the second+ URLs round-trip in Aerion.
  // Show an info note so users know what non-Aerion clients will see.

  import { _ } from 'svelte-i18n'
  import { Input } from '$lib/components/ui/input'
  import { Label } from '$lib/components/ui/label'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import TypeSelect, { URL_TYPES } from '$lib/components/kit/TypeSelect.svelte'
  import type { URLRow, SlotConstraint } from './types'

  interface Props {
    urls: URLRow[]
    disabled?: boolean
    constraint?: SlotConstraint
  }

  let { urls = $bindable([]), disabled = false, constraint = { kind: 'none' } }: Props = $props()

  const infoMessage = $derived(constraint.kind === 'info' ? constraint.message : '')

  function add() {
    urls = [...urls, { url: '', type: '' }]
  }
  function remove(i: number) {
    urls = urls.filter((_, idx) => idx !== i)
  }
</script>

<div>
  <Label>{$_('contacts.edit.urls')}</Label>
  {#if infoMessage}
    <p class="text-xs text-muted-foreground mb-2" role="note">
      <Icon icon="mdi:information-outline" class="inline w-3.5 h-3.5 mr-0.5" />
      {infoMessage}
    </p>
  {/if}
  <div class="space-y-2">
    {#each urls as u, i (i)}
      <div class="flex gap-2 items-center">
        <Input
          type="url"
          bind:value={u.url}
          placeholder={$_('contacts.edit.urlPlaceholder')}
          disabled={disabled}
        />
        <div class="w-32">
          <TypeSelect
            value={u.type}
            onValueChange={(v) => (urls[i] = { ...urls[i], type: v })}
            options={URL_TYPES}
          />
        </div>
        <Button
          variant="ghost"
          size="icon"
          onclick={() => remove(i)}
          disabled={disabled}
          aria-label={$_('contacts.edit.removeUrl')}
        >
          <Icon icon="mdi:close" class="w-4 h-4" />
        </Button>
      </div>
    {/each}
  </div>
  <Button variant="outline" size="sm" onclick={add} disabled={disabled} class="mt-2">
    <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
    {$_('contacts.edit.addUrl')}
  </Button>
</div>
