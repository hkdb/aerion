<script lang="ts">
  // Read-only attendees + organizer display for EventDetail. Self attendee
  // is suffixed with "(you)" and gets a slightly stronger row treatment.
  // RSVP controls are NOT here — RSVPControls.svelte handles those.

  import { _ } from 'svelte-i18n'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    attendees: backend.Attendee[]
    organizer?: backend.Organizer | null
    selfEmails?: string[]
  }

  let { attendees = [], organizer = null, selfEmails = [] }: Props = $props()

  const selfSet = $derived(new Set(selfEmails.map(e => e.toLowerCase())))

  function isSelf(email: string): boolean {
    return selfSet.has((email || '').toLowerCase())
  }

  function partStatBadgeKey(ps: string | undefined): string {
    switch ((ps || 'NEEDS-ACTION').toUpperCase()) {
      case 'ACCEPTED':
        return 'calendar.attendees.partStatAccepted'
      case 'DECLINED':
        return 'calendar.attendees.partStatDeclined'
      case 'TENTATIVE':
        return 'calendar.attendees.partStatTentative'
      default:
        return 'calendar.attendees.partStatNeedsAction'
    }
  }

  function partStatBadgeClass(ps: string | undefined): string {
    switch ((ps || 'NEEDS-ACTION').toUpperCase()) {
      case 'ACCEPTED':
        return 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
      case 'DECLINED':
        return 'bg-rose-500/15 text-rose-700 dark:text-rose-300'
      case 'TENTATIVE':
        return 'bg-amber-500/15 text-amber-700 dark:text-amber-300'
      default:
        return 'bg-muted text-muted-foreground'
    }
  }
</script>

{#if organizer || (attendees && attendees.length > 0)}
  <div class="space-y-2">
    {#if organizer}
      <div class="text-xs text-muted-foreground">
        {$_('calendar.attendees.organizer')}:
        <span class="text-foreground">
          {organizer.cn ? `${organizer.cn} <${organizer.email}>` : organizer.email}
        </span>
      </div>
    {/if}

    {#if attendees && attendees.length > 0}
      <div class="text-xs font-medium text-muted-foreground">
        {$_('calendar.attendees.label')} ({attendees.length})
      </div>
      <ul class="space-y-1.5">
        {#each attendees as a (a.email)}
          <li class="flex items-center gap-2 text-sm">
            <Avatar email={a.email} name={a.cn || a.email} density="compact" size={24} />
            <span class="min-w-0 flex-1 truncate">
              {a.cn || a.email}
              {#if isSelf(a.email)}
                <span class="text-xs text-muted-foreground">{$_('calendar.attendees.youSuffix')}</span>
              {/if}
            </span>
            {#if a.role === 'OPT-PARTICIPANT'}
              <span class="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                {$_('calendar.attendees.optional')}
              </span>
            {/if}
            <span class={`rounded px-1.5 py-0.5 text-[10px] font-medium ${partStatBadgeClass(a.partStat)}`}>
              {$_(partStatBadgeKey(a.partStat))}
            </span>
            {#if a.scheduleStatus}
              <span class="text-[10px] text-muted-foreground" title={$_('calendar.attendees.scheduleStatusTooltip')}>
                {a.scheduleStatus}
              </span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}
