<script lang="ts">
  // AttendeesSection — wraps AttendeeInput with the section label, the
  // identity picker (when >1 alias), and the "Find a time" button.
  //
  // The "Send invitations" choice is NOT in this section. On Create, the
  // composer always sends (matches user intuition: "you added attendees,
  // so you want them notified"). On Edit, the composer intercepts Save
  // with SendInvitationsDialog so users can suppress re-notify spam for
  // minor tweaks (typo / color change).

  import { _ } from 'svelte-i18n'
  import AttendeeInput from './AttendeeInput.svelte'
  import FindATimeDialog from './FindATimeDialog.svelte'
  import { Button } from '$lib/components/ui/button'
  import * as Select from '$lib/components/ui/select'
  import Icon from '@iconify/svelte'
  // @ts-ignore - wailsjs bindings
  import type { backend } from '$wailsjs/go/models'

  interface Props {
    attendees: backend.AttendeeInput[]
    organizer?: backend.OrganizerInput | null
    /** Lowercase emails belonging to the current user — passed to AttendeeInput
     *  for self-suggestion suppression AND used to render the organizer line. */
    selfEmails?: string[]
    /** When >1, identity dropdown is shown so the user picks which alias to
     *  organize as. Sourced from the picked calendar's owning source.
     *  Length 0 means the source has no organizer identity (Local source, or
     *  CalDAV with no calendar-user-address-set yet) — the section is hidden
     *  entirely via the `capability` prop. */
    identities?: { email: string; commonName: string }[]
    /** When false, the whole attendees section (input + organizer line +
     *  Find-a-time) is suppressed. Drives the gate for Local sources and
     *  the recovery window for empty CalDAV identity lists. */
    capability?: boolean
    /** "Find a time" starting day (unix seconds, midnight in user's tz). */
    dayUnixForFreeBusy?: number
    /** Duration hint so picked slot calculates DTEND correctly. */
    durationMinutes?: number
    /** Invoked with (startUnix, endUnix) when the user picks a slot. */
    onFreeBusyPick?: (startUnix: number, endUnix: number) => void
    disabled?: boolean
  }

  let {
    attendees = $bindable([]),
    organizer = $bindable(null),
    selfEmails = [],
    identities = [],
    capability = true,
    dayUnixForFreeBusy = 0,
    durationMinutes = 60,
    onFreeBusyPick,
    disabled = false,
  }: Props = $props()

  let findATimeOpen = $state(false)

  const showIdentityPicker = $derived(identities.length > 1)
  const organizerEmail = $derived(organizer?.email || identities[0]?.email || '')

  function setOrganizer(email: string) {
    const m = identities.find(i => i.email.toLowerCase() === email.toLowerCase())
    if (!m) return
    organizer = { email: m.email.toLowerCase(), cn: m.commonName }
  }

  function identityLabel(email: string): string {
    const m = identities.find(i => i.email.toLowerCase() === email.toLowerCase())
    if (!m) return email
    return m.commonName ? `${m.commonName} <${m.email}>` : m.email
  }
</script>

{#if capability}
<div class="space-y-3">
  <div class="flex items-center justify-between">
    <label class="text-sm font-medium" for="cal-attendees">
      {$_('calendar.attendees.label')}
    </label>
    {#if attendees.length > 0 && dayUnixForFreeBusy > 0}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => (findATimeOpen = true)}
        disabled={disabled}
      >
        <Icon icon="mdi:clock-search-outline" class="w-4 h-4 mr-1" />
        {$_('calendar.attendees.findATime')}
      </Button>
    {/if}
  </div>

  {#if showIdentityPicker}
    <div class="flex items-center gap-2 text-xs">
      <span class="text-muted-foreground shrink-0">{$_('calendar.attendees.organizingAs')}</span>
      <Select.Root
        value={organizerEmail}
        onValueChange={(v) => { if (v) setOrganizer(v) }}
        disabled={disabled}
      >
        <Select.Trigger class="h-8 text-xs flex-1 min-w-0">
          <span class="truncate">{identityLabel(organizerEmail)}</span>
        </Select.Trigger>
        <Select.Content>
          {#each identities as id (id.email)}
            <Select.Item
              value={id.email}
              label={id.commonName ? `${id.commonName} <${id.email}>` : id.email}
            />
          {/each}
        </Select.Content>
      </Select.Root>
    </div>
  {/if}

  <AttendeeInput
    bind:attendees
    selfEmails={selfEmails}
    placeholder={$_('calendar.attendees.placeholder')}
    disabled={disabled}
  />

</div>

<FindATimeDialog
  bind:open={findATimeOpen}
  attendeeEmails={attendees.map(a => (a.email || '').toLowerCase()).filter(e => e !== '')}
  selfEmails={selfEmails}
  dayUnix={dayUnixForFreeBusy}
  durationMinutes={durationMinutes}
  onSelect={(s, e) => onFreeBusyPick?.(s, e)}
/>
{/if}
