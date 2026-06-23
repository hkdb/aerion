<script lang="ts">
  // Accept / Decline / Tentative buttons. Mounted by EventDetail only when
  // the current user matches an attendee on the event. Calls the
  // Calendar_UpdateMyAttendeeStatus bridge method which dispatches to the
  // provider-native RSVP path (Microsoft /accept, Google PATCH, CalDAV
  // re-PUT with PARTSTAT changed).

  import { _ } from 'svelte-i18n'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { Calendar_UpdateMyAttendeeStatus } from '$wailsjs/go/app/App'

  interface Props {
    eventId: string
    /** Current PARTSTAT of the self-attendee — used to highlight the
     *  matching button so users can see their current state at a glance. */
    currentPartStat?: string
    /** Lowercase emails belonging to the user. Backend uses these to
     *  resolve which attendee row to mutate. Sourced by EventDetail from
     *  the union of account.email + identity.email. */
    selfEmails: string[]
    /** Called after a successful RSVP so the parent re-fetches the event
     *  to surface the new PartStat in AttendeeListDisplay. */
    onUpdated?: () => void
    disabled?: boolean
  }

  let { eventId, currentPartStat = '', selfEmails, onUpdated, disabled = false }: Props = $props()

  let working = $state(false)

  async function rsvp(partStat: string) {
    if (working || disabled || !eventId) return
    working = true
    try {
      await Calendar_UpdateMyAttendeeStatus(eventId, selfEmails, partStat)
      toasts.success($_('calendar.attendees.rsvpToast'))
      onUpdated?.()
    } catch (err) {
      const msg = (err as Error)?.message ?? String(err)
      toasts.error(`${$_('calendar.attendees.rsvpFailed')}: ${msg}`)
    } finally {
      working = false
    }
  }

  const current = $derived((currentPartStat || '').toUpperCase())
</script>

<div class="flex flex-wrap gap-2">
  <Button
    variant={current === 'ACCEPTED' ? 'default' : 'outline'}
    size="sm"
    disabled={disabled || working}
    onclick={() => rsvp('ACCEPTED')}
  >
    <Icon icon="mdi:check" class="w-4 h-4 mr-1" />
    {$_('calendar.attendees.accept')}
  </Button>
  <Button
    variant={current === 'TENTATIVE' ? 'default' : 'outline'}
    size="sm"
    disabled={disabled || working}
    onclick={() => rsvp('TENTATIVE')}
  >
    <Icon icon="mdi:help" class="w-4 h-4 mr-1" />
    {$_('calendar.attendees.tentative')}
  </Button>
  <Button
    variant={current === 'DECLINED' ? 'default' : 'outline'}
    size="sm"
    disabled={disabled || working}
    onclick={() => rsvp('DECLINED')}
  >
    <Icon icon="mdi:close" class="w-4 h-4 mr-1" />
    {$_('calendar.attendees.decline')}
  </Button>
</div>
