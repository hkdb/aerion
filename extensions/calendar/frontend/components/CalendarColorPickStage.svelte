<script lang="ts">
  // CalendarColorPickStage — post-add stage 2 shared by every multi-calendar
  // source add flow (CalDAV, Google, Microsoft). Renders the dialog header,
  // a per-calendar row list with color picker + provider-default radio, the
  // global/provider default chooser via AddCalendarDefaultsControl, and the
  // Done footer.
  //
  // Owns:
  //   - Per-row color picking (writes immediately via calendarSources.setColor).
  //   - Per-row provider-default radio (binds providerDefaultTempId).
  //   - Read-only badge for non-writable calendars (disables the default radio).
  //
  // Does NOT own (parent dialog handles):
  //   - Dialog open/close lifecycle.
  //   - Stage transition logic (form → colors).
  //   - finalizeDefaults() / applyDefaultsAfterAdd — fired in the parent's
  //     close() so partial picks survive a dismiss.
  //
  // Single source of truth for the visual layout so the three add flows stay
  // in lockstep through any future tweak.

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import ColorPicker from '$lib/components/kit/ColorPicker.svelte'
  import AddCalendarDefaultsControl from './AddCalendarDefaultsControl.svelte'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    /** Backend ID of the just-persisted source. Passed through to the
     *  defaults control + used as scope for "make default for this provider". */
    sourceId: string
    /** Display label for the provider in default-picker hovertext
     *  (e.g., "Murena", "Google: alice@gmail.com"). */
    providerLabel: string
    /** Calendars to render. Parent passes
     *  calendarSources.calendarsBySource[sourceId]. */
    discoveredCalendars: backend.Calendar[]
    /** tempId of the row the user marked as provider default. Bindable so
     *  the parent can read it during finalizeDefaults() on close. */
    providerDefaultTempId: string
    /** 'new:<tempId>' | '<existingCalendarId>' | ''. Bindable for the same
     *  reason as providerDefaultTempId. */
    globalDefaultRef: string
    /** Called when the Done button is clicked. Parent runs
     *  applyDefaultsAfterAdd() + closes the dialog. */
    onDone: () => void
  }

  let {
    sourceId,
    providerLabel,
    discoveredCalendars,
    providerDefaultTempId = $bindable(''),
    globalDefaultRef = $bindable(''),
    onDone,
  }: Props = $props()
</script>

<Dialog.Header>
  <Dialog.Title>{$_('calendar.add.colorPickStage.title')}</Dialog.Title>
  <Dialog.Description>
    {$_('calendar.add.colorPickStage.help')}
  </Dialog.Description>
</Dialog.Header>

<div class="mt-2 max-h-80 overflow-y-auto">
  {#each discoveredCalendars as cal (cal.id)}
    <div class="flex items-center gap-3 py-2">
      <span
        class="shrink-0 inline-block w-3 h-3 rounded-full"
        style:background-color={calendarSources.colorOf(cal.id)}
        aria-hidden="true"
      ></span>
      <span class="flex-1 min-w-0 truncate text-sm text-foreground">
        {cal.displayName}
        {#if cal.writable === false}
          <span class="ml-1 text-xs text-muted-foreground">({$_('calendar.hooks.readOnlyBadge')})</span>
        {/if}
      </span>
      <ColorPicker
        value={cal.color ?? ''}
        onchange={(hex) => { void calendarSources.setColor(cal.id, hex) }}
      />
      <label class="flex items-center gap-1 text-xs text-muted-foreground shrink-0" title={$_('calendar.add.makeProviderDefault', { values: { provider: providerLabel } })}>
        <input
          type="radio"
          name="cal-provider-default"
          class="accent-primary"
          checked={providerDefaultTempId === cal.id}
          disabled={cal.writable === false}
          onchange={() => { providerDefaultTempId = cal.id }}
        />
        {$_('calendar.add.defaultColumnHeader')}
      </label>
    </div>
  {/each}
</div>

<AddCalendarDefaultsControl
  mode="multi"
  sourceId={sourceId}
  providerLabel={providerLabel}
  candidates={discoveredCalendars.map(c => ({ tempId: c.id, displayName: c.displayName, writable: c.writable !== false }))}
  bind:providerDefaultTempId
  bind:globalDefaultRef
/>

<div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
  <Button onclick={onDone}>
    {$_('calendar.add.colorPickStage.done')}
  </Button>
</div>
