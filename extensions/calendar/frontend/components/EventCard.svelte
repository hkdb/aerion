<script lang="ts">
  // EventCard — calendar-domain event pill rendered inside MonthView cells
  // (and Week/Day timelines in 1F). Pure visual: receives an instance, a
  // color, and optional multi-day continuation flags. Click bubbles to the
  // parent via the onclick prop — no internal state.

  import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    instance: backend.EventInstance
    color: string
    /** Show only the continuation marker on the left edge (event started in a previous week). */
    continuesLeft?: boolean
    /** Show only the continuation marker on the right edge (event continues to next week). */
    continuesRight?: boolean
    onclick?: () => void
  }

  let {
    instance,
    color,
    continuesLeft = false,
    continuesRight = false,
    onclick,
  }: Props = $props()

  const isAllDay = $derived(instance.isAllDay)

  // Time prefix for timed events. Locale-aware via Intl; tz-aware via the
  // user's chosen display timezone.
  const timeLabel = $derived.by(() => {
    if (isAllDay) return ''
    const d = new Date(instance.instanceStartUnix * 1000)
    return new Intl.DateTimeFormat(undefined, {
      hour: '2-digit', minute: '2-digit', hour12: false,
      timeZone: calendarSettings.effectiveTimezone,
    }).format(d)
  })

  function handleClick(e: MouseEvent) {
    e.stopPropagation()
    onclick?.()
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key !== 'Enter' && e.key !== ' ') return
    e.preventDefault()
    onclick?.()
  }
</script>

<div
  role="button"
  tabindex="0"
  class="flex items-center gap-1 px-1.5 py-0.5 text-xs text-foreground rounded cursor-pointer
         hover:brightness-110 transition-[filter] truncate"
  style:background-color={`color-mix(in srgb, ${color} 25%, transparent)`}
  style:border-left={`3px solid ${color}`}
  title={instance.summary}
  onclick={handleClick}
  onkeydown={handleKeyDown}
>
  {#if continuesLeft}
    <span class="shrink-0 text-muted-foreground" aria-hidden="true">◀</span>
  {/if}
  {#if !isAllDay}
    <span class="shrink-0 font-mono text-[10px] text-muted-foreground">{timeLabel}</span>
  {/if}
  <span class="flex-1 min-w-0 truncate">{instance.summary || '(no title)'}</span>
  {#if continuesRight}
    <span class="shrink-0 text-muted-foreground" aria-hidden="true">▶</span>
  {/if}
</div>
