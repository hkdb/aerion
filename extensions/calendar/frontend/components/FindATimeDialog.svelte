<script lang="ts">
  // FindATimeDialog — modal opened from AttendeesSection. Renders a
  // 30-min-slot timeline grid: rows = attendees, columns = slots across
  // the proposed day. Busy blocks are shaded; the user clicks a free
  // slot to snap the event DTSTART/DTEND via the onSelect callback.
  //
  // Per-attendee "no data" indicator surfaced when the aggregator
  // couldn't get a response from any provider — better UX than
  // misleading "free all day."

  import { _ } from 'svelte-i18n'
  import * as Dialog from '$lib/components/ui/dialog'
  import { Button } from '$lib/components/ui/button'
  import { dialogGuardOpen, dialogGuardClose } from '$lib/stores/dialogGuard'
  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'
  // @ts-ignore - wailsjs bindings
  import { Calendar_QueryFreeBusy } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    open: boolean
    /** Lowercased attendee emails to plot. */
    attendeeEmails: string[]
    /** Lowercased self-emails (account + identity union); routes local DB scans. */
    selfEmails: string[]
    /** Initial reference day (unix seconds at midnight in user's tz). */
    dayUnix: number
    /** Hint for the slot length the user is trying to schedule, in minutes. */
    durationMinutes?: number
    /** Called when user picks a slot. Args: start unix seconds, end unix seconds. */
    onSelect?: (startUnix: number, endUnix: number) => void
    onClose?: () => void
  }

  let {
    open = $bindable(false),
    attendeeEmails,
    selfEmails,
    dayUnix,
    durationMinutes = 60,
    onSelect,
    onClose,
  }: Props = $props()

  // Grid: from 8 AM to 8 PM local — covers most working hours; future
  // refinement lets users pan via a chip. 30-min slots = 24 columns.
  const SLOT_MINS = 30
  const HOURS_FROM = 8
  const HOURS_TO = 20
  const SLOTS = ((HOURS_TO - HOURS_FROM) * 60) / SLOT_MINS

  let results = $state<backend.FreeBusyResult[]>([])
  let loading = $state(false)
  let loadError = $state('')

  $effect(() => {
    if (open) {
      dialogGuardOpen()
      return () => dialogGuardClose()
    }
  })

  $effect(() => {
    if (!open) return
    loadAvailability()
  })

  function startOfDay(unix: number): number {
    const d = new Date(unix * 1000)
    d.setHours(0, 0, 0, 0)
    return Math.floor(d.getTime() / 1000)
  }

  function windowBounds(): { fromUnix: number; toUnix: number } {
    const d0 = startOfDay(dayUnix)
    return {
      fromUnix: d0 + HOURS_FROM * 3600,
      toUnix: d0 + HOURS_TO * 3600,
    }
  }

  async function loadAvailability() {
    if (attendeeEmails.length === 0) {
      results = []
      return
    }
    loading = true
    loadError = ''
    try {
      const { fromUnix, toUnix } = windowBounds()
      results = await Calendar_QueryFreeBusy(selfEmails, attendeeEmails, fromUnix, toUnix)
    } catch (err) {
      loadError = (err as Error)?.message ?? String(err)
    } finally {
      loading = false
    }
  }

  // For each (attendee, slot-index) pair, is it busy?
  const slotMatrix = $derived.by(() => {
    const { fromUnix } = windowBounds()
    const matrix = new Map<string, boolean[]>()
    for (const r of results) {
      const row = new Array(SLOTS).fill(false)
      for (const b of r.blocks ?? []) {
        const start = b.startUnix
        const end = b.endUnix
        for (let i = 0; i < SLOTS; i++) {
          const slotStart = fromUnix + i * SLOT_MINS * 60
          const slotEnd = slotStart + SLOT_MINS * 60
          if (slotStart < end && slotEnd > start) row[i] = true
        }
      }
      matrix.set(r.email, row)
    }
    return matrix
  })

  function slotLabel(idx: number): string {
    const totalMins = HOURS_FROM * 60 + idx * SLOT_MINS
    const h = Math.floor(totalMins / 60)
    const m = totalMins % 60
    return `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`
  }

  function pickSlot(idx: number) {
    const { fromUnix } = windowBounds()
    const start = fromUnix + idx * SLOT_MINS * 60
    const end = start + durationMinutes * 60
    onSelect?.(start, end)
    close()
  }

  function slotTitle(email: string, idx: number, busy: boolean): string {
    const status = busy ? $_('calendar.attendees.busy') : $_('calendar.attendees.free')
    return `${email} • ${slotLabel(idx)} • ${status}`
  }

  function close() {
    open = false
    onClose?.()
  }
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) close() }}>
  <Dialog.Content class="max-w-3xl">
    <Dialog.Header>
      <Dialog.Title>{$_('calendar.attendees.findATimeTitle')}</Dialog.Title>
      <Dialog.Description>
        {$_('calendar.attendees.findATimeDescription', { values: { tz: calendarSettings.effectiveTimezone } })}
      </Dialog.Description>
    </Dialog.Header>

    <div class="mt-2 space-y-3">
      {#if loading}
        <div class="text-sm text-muted-foreground">{$_('calendar.common.loading')}</div>
      {/if}
      {#if loadError}
        <div class="text-sm text-destructive">{loadError}</div>
      {/if}
      {#if !loading && results.length > 0}
        <!-- Header: time labels -->
        <div class="grid items-center gap-px text-[10px] text-muted-foreground" style:grid-template-columns="180px repeat({SLOTS}, minmax(0, 1fr))">
          <div></div>
          {#each Array(SLOTS) as _, i}
            <div class="text-center">{i % 2 === 0 ? slotLabel(i) : ''}</div>
          {/each}
        </div>

        <!-- Rows -->
        {#each results as r (r.email)}
          <div class="grid items-center gap-px text-xs" style:grid-template-columns="180px repeat({SLOTS}, minmax(0, 1fr))">
            <div class="flex items-center gap-1 truncate pr-2">
              <span class="truncate">{r.email}</span>
              {#if r.source === ''}
                <span class="text-[10px] text-muted-foreground">{$_('calendar.attendees.noData')}</span>
              {/if}
            </div>
            {#each Array(SLOTS) as _, i}
              {@const busy = slotMatrix.get(r.email)?.[i] ?? false}
              <button
                type="button"
                class={`h-5 rounded-sm border border-transparent hover:border-primary ${
                  r.source === '' ? 'bg-muted/30' : busy ? 'bg-rose-500/60' : 'bg-emerald-500/30'
                }`}
                title={slotTitle(r.email, i, busy)}
                onclick={() => pickSlot(i)}
              ></button>
            {/each}
          </div>
        {/each}
      {/if}
    </div>

    <div class="flex items-center justify-end gap-2 pt-4 border-t border-border mt-4">
      <Button variant="ghost" onclick={close}>{$_('calendar.common.close')}</Button>
    </div>
  </Dialog.Content>
</Dialog.Root>
