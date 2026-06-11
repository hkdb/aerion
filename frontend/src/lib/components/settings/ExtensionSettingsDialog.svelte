<script lang="ts">
  // ExtensionSettingsDialog — host-level dispatcher for per-extension settings
  // dialogs. Watches extensionRegistry's openSettingsExtension state and
  // renders the matching extension's dialog component.
  //
  // Static dispatch by extension ID (same pattern as AccountDialog's
  // account-setup hook dispatch). When a new first-party extension lands,
  // add its case to the {#if} chain. Community extensions (v0.4+) will use
  // a different mechanism since their Svelte components aren't compiled
  // into Aerion's binary.
  //
  // Mount once at App.svelte level so the dialog can be opened from anywhere.

  import { getOpenSettingsExtension, closeExtensionSettings } from '$lib/stores/extensionRegistry.svelte'
  import ContactsSettingsDialog from '$extensions/contacts/frontend/components/ContactsSettingsDialog.svelte'
  import CalendarSettingsDialog from '$extensions/calendar/frontend/components/CalendarSettingsDialog.svelte'

  let openExtension = $derived(getOpenSettingsExtension())

  // Each per-extension dialog binds its `open` prop to a derived true/false
  // based on the open state. Closing the dialog calls closeExtensionSettings.
  let contactsOpen = $derived(openExtension === 'contacts')
  let calendarOpen = $derived(openExtension === 'calendar')
</script>

<ContactsSettingsDialog
  open={contactsOpen}
  onClose={closeExtensionSettings}
/>

<CalendarSettingsDialog
  open={calendarOpen}
  onClose={closeExtensionSettings}
/>
