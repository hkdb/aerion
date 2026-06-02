<script lang="ts">
  // CalendarPane — root component for the Calendar extension's main view.
  //
  // Phase 1A is a placeholder so the extension lifecycle can be smoke-tested
  // end-to-end: enable Calendar in Settings → rail shows the calendar icon →
  // click → THIS placeholder renders. Sub-phase 1D replaces the placeholder
  // with the real shell (PaneLayout + CalendarSidebar + ViewSwitcher + the
  // active view).
  import Icon from '@iconify/svelte'
</script>

<div class="flex-1 flex items-center justify-center bg-background text-muted-foreground">
  <div class="flex flex-col items-center gap-3 max-w-md text-center px-6">
    <Icon icon="mdi:calendar-clock" class="w-12 h-12 text-muted-foreground/60" />
    <h2 class="text-lg font-medium text-foreground">Calendar</h2>
    <p class="text-sm">
      The Calendar extension is wired up but read-only CalDAV sync, calendar
      sources, and the month/week/day/agenda views land in later sub-phases.
    </p>
    <p class="text-xs text-muted-foreground/80">
      Phase 1A · plumbing checkpoint
    </p>
  </div>
</div>
