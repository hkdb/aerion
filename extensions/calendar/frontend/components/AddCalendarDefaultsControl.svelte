<script lang="ts">
  // AddCalendarDefaultsControl — captures the user's "make this/these the
  // default" intent during the four add-calendar flows.
  //
  // Two render modes:
  //   - mode="single": two checkboxes ("Set as provider default" + "Set as
  //     global default"). Used by AddLocalCalendarDialog where there's only
  //     one calendar being added.
  //   - mode="multi": one Select.Root listing existing writable calendars
  //     plus the calendars about to be added (marked "(new)"). The per-row
  //     "Default" radio that picks the provider default is rendered inline
  //     by the parent dialog — it lives next to each picker row and can't
  //     be cleanly encapsulated here.
  //
  // Bindable state:
  //   - providerDefaultTempId: tempId of the candidate the user marked as
  //     provider default. For mode="single" this gets set to candidates[0]
  //     .tempId iff the checkbox is on.
  //   - globalDefaultRef: 'new:<tempId>' | '<existingCalendarId>' | ''.
  //
  // The actual write-to-store happens in applyDefaultsAfterAdd() (called by
  // the parent after the backend assigns real IDs); this component only
  // collects intent.

  import { _ } from 'svelte-i18n'
  import * as Select from '$lib/components/ui/select'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'

  interface Candidate {
    tempId: string
    displayName: string
    writable: boolean
  }

  interface Props {
    mode: 'single' | 'multi'
    candidates: Candidate[]
    sourceId: string
    /** Display label for the provider, e.g., "Local calendars", "Murena". */
    providerLabel: string
    providerDefaultTempId: string
    globalDefaultRef: string
  }

  let {
    mode,
    candidates,
    sourceId,
    providerLabel,
    providerDefaultTempId = $bindable(''),
    globalDefaultRef = $bindable(''),
  }: Props = $props()

  // mode="single": map the checkbox state to the bindables.
  // candidates[0] is the single calendar being added.
  let singleProviderChecked = $state(false)
  let singleGlobalChecked = $state(false)

  $effect(() => {
    if (mode !== 'single') return
    const tempId = candidates[0]?.tempId ?? ''
    providerDefaultTempId = singleProviderChecked ? tempId : ''
    globalDefaultRef = singleGlobalChecked ? `new:${tempId}` : ''
  })

  // mode="multi": build the dropdown's contents — existing writable
  // calendars (grouped by source) plus the new candidates with "(new)"
  // marker. Sorted alphabetically within each group.
  interface OptionRow {
    value: string         // raw ID or 'new:<tempId>'
    label: string         // "Personal · Murena"
    isNew: boolean
    isSection: boolean    // true for source-name headers
  }

  const options = $derived.by<OptionRow[]>(() => {
    if (mode !== 'multi') return []
    const rows: OptionRow[] = []

    for (const src of calendarSources.sources) {
      if (!src.writable) continue
      const cals = (calendarSources.calendarsBySource[src.id] || [])
        .filter(c => c.writable !== false)
      if (cals.length === 0) continue
      rows.push({ value: '', label: src.name, isNew: false, isSection: true })
      for (const cal of cals) {
        rows.push({
          value: cal.id,
          label: `${cal.displayName} · ${src.name}`,
          isNew: false,
          isSection: false,
        })
      }
    }

    // Append the new candidates as "(new)" entries under a synthetic group.
    const writableNew = candidates.filter(c => c.writable)
    if (writableNew.length > 0) {
      rows.push({ value: '', label: `${providerLabel} (new)`, isNew: true, isSection: true })
      for (const c of writableNew) {
        rows.push({
          value: `new:${c.tempId}`,
          label: `${c.displayName} (new)`,
          isNew: true,
          isSection: false,
        })
      }
    }

    return rows
  })

  // Initial value: prefer the currently-stored global default (validated
  // writable), else leave empty so the user picks.
  $effect(() => {
    if (mode !== 'multi') return
    if (globalDefaultRef !== '') return
    const stored = calendarSettings.globalDefaultCalendarId
    if (stored) globalDefaultRef = stored
  })

  function triggerLabel(): string {
    if (globalDefaultRef === '') return $_('calendar.add.globalDefaultUnset')
    const match = options.find(o => o.value === globalDefaultRef)
    if (match) return match.label
    return globalDefaultRef
  }
</script>

{#if mode === 'single'}
  <div class="flex flex-col gap-2 mt-3">
    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" bind:checked={singleProviderChecked} class="accent-primary" />
      <span>{$_('calendar.add.makeProviderDefault', { values: { provider: providerLabel } })}</span>
    </label>
    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" bind:checked={singleGlobalChecked} class="accent-primary" />
      <span>{$_('calendar.add.makeGlobalDefault')}</span>
    </label>
  </div>
{/if}

{#if mode === 'multi'}
  <div class="flex items-center justify-between gap-3 mt-3">
    <span class="text-sm text-foreground shrink-0">{$_('calendar.add.globalDefaultLabel')}</span>
    <Select.Root
      value={globalDefaultRef}
      onValueChange={(v) => { globalDefaultRef = v ?? '' }}
    >
      <Select.Trigger class="h-8 max-w-xs text-xs">
        {triggerLabel()}
      </Select.Trigger>
      <Select.Content>
        {#each options as opt, i (i)}
          {#if opt.isSection}
            <div class="px-2 py-1 text-xs font-medium text-muted-foreground">{opt.label}</div>
          {/if}
          {#if !opt.isSection}
            <Select.Item value={opt.value} label={opt.label} />
          {/if}
        {/each}
      </Select.Content>
    </Select.Root>
  </div>
{/if}
