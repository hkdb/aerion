<script lang="ts">
  // AddCalendarMenu — inline-expanding "+ Add Calendar" entry for the
  // calendar sidebar. Click expands the button into four stacked source
  // options (Local / CalDAV / Google / Microsoft), each with a small
  // fly-in stagger. Click an option to open its add flow; click the
  // chevron again (or anywhere outside the menu) to collapse.
  //
  // Owns its own dialog mounts for all four source types so the consumer
  // doesn't have to coordinate dialog state.

  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { calendarSources } from '$extensions/calendar/frontend/stores/calendarSources.svelte'
  import AddLocalCalendarDialog from './AddLocalCalendarDialog.svelte'
  import AddCalDAVSourceDialog from './AddCalDAVSourceDialog.svelte'
  import AddGoogleCalendarDialog from './AddGoogleCalendarDialog.svelte'
  import AddMicrosoftCalendarDialog from './AddMicrosoftCalendarDialog.svelte'

  let expanded = $state(false)
  let showAddLocal = $state(false)
  let showAddCalDAV = $state(false)
  let showAddGoogle = $state(false)
  let showAddMicrosoft = $state(false)
  let containerRef = $state<HTMLDivElement | null>(null)

  function toggle() {
    expanded = !expanded
  }

  function pickLocal() {
    expanded = false
    showAddLocal = true
  }

  function pickCalDAV() {
    expanded = false
    showAddCalDAV = true
  }

  function pickGoogle() {
    expanded = false
    showAddGoogle = true
  }

  function pickMicrosoft() {
    expanded = false
    showAddMicrosoft = true
  }

  function handleClickOutside(e: MouseEvent) {
    if (!containerRef) return
    if (containerRef.contains(e.target as Node)) return
    expanded = false
  }

  $effect(() => {
    if (!expanded) return
    document.addEventListener('click', handleClickOutside, true)
    return () => {
      document.removeEventListener('click', handleClickOutside, true)
    }
  })
</script>

<div class="px-3 py-2" bind:this={containerRef}>
  <!-- Trigger button: matches mail's SidebarAddItem visual rhythm so the
       calendar sidebar still feels native, with a chevron added on the
       right to telegraph expandability. -->
  <button
    type="button"
    class="w-full flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground
           hover:text-foreground hover:bg-muted/50 rounded-md transition-colors"
    onclick={toggle}
    aria-expanded={expanded}
  >
    <Icon icon="mdi:plus" class="w-4 h-4" />
    <span class="flex-1 text-left">{$_('calendar.addCalendar.trigger')}</span>
    <Icon
      icon="mdi:chevron-down"
      class="w-4 h-4 transition-transform duration-200 {expanded ? 'rotate-180' : ''}"
    />
  </button>

  {#if expanded}
    <div class="mt-1 space-y-0.5">
      <!-- Local -->
      <button
        type="button"
        class="w-full flex items-center gap-2 px-3 py-2 text-sm text-foreground
               hover:bg-muted/50 rounded-md transition-colors"
        onclick={pickLocal}
        transition:fly={{ y: -8, duration: 180, delay: 0, easing: cubicOut }}
      >
        <Icon icon="mdi:laptop" class="w-4 h-4 text-muted-foreground" />
        <span class="flex-1 text-left">{$_('calendar.addCalendar.local')}</span>
      </button>

      <!-- CalDAV -->
      <button
        type="button"
        class="w-full flex items-center gap-2 px-3 py-2 text-sm text-foreground
               hover:bg-muted/50 rounded-md transition-colors"
        onclick={pickCalDAV}
        transition:fly={{ y: -8, duration: 180, delay: 40, easing: cubicOut }}
      >
        <Icon icon="mdi:server-network" class="w-4 h-4 text-muted-foreground" />
        <span class="flex-1 text-left">{$_('calendar.addCalendar.caldav')}</span>
      </button>

      <!-- Google -->
      <button
        type="button"
        class="w-full flex items-center gap-2 px-3 py-2 text-sm text-foreground
               hover:bg-muted/50 rounded-md transition-colors"
        onclick={pickGoogle}
        transition:fly={{ y: -8, duration: 180, delay: 80, easing: cubicOut }}
      >
        <Icon icon="logos:google-icon" class="w-4 h-4" />
        <span class="flex-1 text-left">{$_('calendar.addCalendar.google')}</span>
      </button>

      <!-- Microsoft -->
      <button
        type="button"
        class="w-full flex items-center gap-2 px-3 py-2 text-sm text-foreground
               hover:bg-muted/50 rounded-md transition-colors"
        onclick={pickMicrosoft}
        transition:fly={{ y: -8, duration: 180, delay: 120, easing: cubicOut }}
      >
        <Icon icon="logos:microsoft-icon" class="w-4 h-4" />
        <span class="flex-1 text-left">{$_('calendar.addCalendar.microsoft')}</span>
      </button>
    </div>
  {/if}
</div>

<AddLocalCalendarDialog
  bind:open={showAddLocal}
  onCreated={() => { void calendarSources.load() }}
/>

<AddCalDAVSourceDialog
  bind:open={showAddCalDAV}
  onClose={() => { void calendarSources.load() }}
/>

<AddGoogleCalendarDialog
  bind:open={showAddGoogle}
  onClose={() => { void calendarSources.load() }}
/>

<AddMicrosoftCalendarDialog
  bind:open={showAddMicrosoft}
  onClose={() => { void calendarSources.load() }}
/>
