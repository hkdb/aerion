<script lang="ts">
  // AttendeeInput — chip-style input for adding event attendees with
  // contacts autocomplete. Follows the same UX pattern as mail's
  // RecipientInput but produces backend.AttendeeInput[] shape (email +
  // commonName + role + optional partStat). Contact search routes through
  // the calendar's Wails-bound Calendar_SearchContacts wrapper which
  // delegates to coreapi.Contacts.SearchContacts — same backend function
  // mail's composer reaches.

  import { _ } from 'svelte-i18n'
  import Icon from '@iconify/svelte'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  // @ts-ignore - wailsjs bindings
  import { Calendar_SearchContacts } from '$wailsjs/go/app/App'
  // @ts-ignore - wailsjs bindings
  import { backend, v1 } from '$wailsjs/go/models'

  interface Props {
    attendees: backend.AttendeeInput[]
    /** Lowercase emails belonging to the current user — suppressed from suggestions. */
    selfEmails?: string[]
    placeholder?: string
    disabled?: boolean
  }

  let {
    attendees = $bindable([]),
    selfEmails = [],
    placeholder = '',
    disabled = false,
  }: Props = $props()

  let inputValue = $state('')
  let suggestions = $state<v1.Contact[]>([])
  let showSuggestions = $state(false)
  let selectedIndex = $state(-1)
  let inputElement: HTMLInputElement | null = null
  let debounceTimer: ReturnType<typeof setTimeout> | null = null

  const placeholderText = $derived(placeholder || $_('calendar.attendees.placeholder'))
  const selfSet = $derived(new Set(selfEmails.map(e => e.toLowerCase())))
  const attendeeEmailSet = $derived(
    new Set(attendees.map(a => (a.email || '').toLowerCase())),
  )

  // Multi-email contacts produce one suggestion row per (contact, email),
  // so the user picks the right address. Suggestions matching self or
  // already-added attendees are hidden.
  const flattenedSuggestions = $derived.by(() => {
    const out: { contact: v1.Contact; email: string }[] = []
    for (const c of suggestions) {
      const emails = c.emails ?? []
      for (const raw of emails) {
        const email = (raw || '').toLowerCase()
        if (!email) continue
        if (selfSet.has(email)) continue
        if (attendeeEmailSet.has(email)) continue
        out.push({ contact: c, email })
      }
    }
    return out
  })

  async function searchContacts(query: string) {
    if (query.length < 2) {
      suggestions = []
      showSuggestions = false
      return
    }
    try {
      const results = await Calendar_SearchContacts(query, 10)
      suggestions = results || []
      showSuggestions = flattenedSuggestions.length > 0
      selectedIndex = -1
    } catch (err) {
      console.error('Calendar_SearchContacts failed:', err)
      suggestions = []
      showSuggestions = false
    }
  }

  function handleInput() {
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => searchContacts(inputValue), 200)
  }

  function handleKeyDown(e: KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        if (showSuggestions && selectedIndex < flattenedSuggestions.length - 1) {
          selectedIndex++
        }
        return
      case 'ArrowUp':
        e.preventDefault()
        if (showSuggestions && selectedIndex > 0) {
          selectedIndex--
        }
        return
      case 'Enter':
        e.preventDefault()
        if (showSuggestions && selectedIndex >= 0) {
          const s = flattenedSuggestions[selectedIndex]
          addAttendee(s.email, s.contact.name || '')
          return
        }
        if (inputValue.trim()) {
          tryParseAndAdd(inputValue.trim())
        }
        return
      case 'Escape':
        showSuggestions = false
        selectedIndex = -1
        return
      case 'Backspace':
        if (inputValue === '' && attendees.length > 0) {
          removeAttendee(attendees.length - 1)
        }
        return
      case ',':
      case ';':
      case 'Tab':
        if (inputValue.trim()) {
          e.preventDefault()
          tryParseAndAdd(inputValue.trim())
        }
        return
    }
  }

  // Accept `Name <email@host>` or bare `email@host`. Email regex inlined
  // (matches RecipientInput's; do NOT factor out a shared util in this
  // release per plan §Refactor trade-off).
  function tryParseAndAdd(raw: string) {
    const match = raw.match(/^(?:(.+?)\s*<)?([^\s<>]+@[^\s<>]+)>?$/)
    if (!match) return
    const cn = (match[1] || '').trim()
    const email = match[2].toLowerCase()
    addAttendee(email, cn)
  }

  function addAttendee(email: string, cn: string) {
    const lower = email.toLowerCase()
    if (!lower.includes('@')) return
    if (selfSet.has(lower)) return
    if (attendeeEmailSet.has(lower)) {
      inputValue = ''
      return
    }
    const entry = backend.AttendeeInput.createFrom({
      email: lower,
      cn,
      partStat: 'NEEDS-ACTION',
      role: 'REQ-PARTICIPANT',
      rsvp: true,
    })
    attendees = [...attendees, entry]
    inputValue = ''
    suggestions = []
    showSuggestions = false
    selectedIndex = -1
    inputElement?.focus()
  }

  function removeAttendee(i: number) {
    attendees = attendees.filter((_, idx) => idx !== i)
  }

  function toggleOptional(i: number) {
    attendees = attendees.map((a, idx) => {
      if (idx !== i) return a
      const isOpt = a.role === 'OPT-PARTICIPANT'
      return backend.AttendeeInput.createFrom({
        ...a,
        role: isOpt ? 'REQ-PARTICIPANT' : 'OPT-PARTICIPANT',
      })
    })
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

<div class="space-y-2">
  <div class="flex flex-wrap gap-1.5">
    {#each attendees as a, i (a.email + '-' + i)}
      <div
        class="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted/50 pl-1 pr-1 py-0.5 text-xs"
        title={a.cn ? `${a.cn} <${a.email}>` : a.email}
      >
        <Avatar email={a.email} name={a.cn || a.email} density="compact" size={20} />
        <span class="max-w-[12rem] truncate">{a.cn || a.email}</span>
        <span class={`rounded px-1.5 py-0.5 text-[10px] font-medium ${partStatBadgeClass(a.partStat)}`}>
          {$_(partStatBadgeKey(a.partStat))}
        </span>
        <button
          type="button"
          class="rounded px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
          onclick={() => toggleOptional(i)}
          disabled={disabled}
          title={a.role === 'OPT-PARTICIPANT'
            ? $_('calendar.attendees.markRequired')
            : $_('calendar.attendees.markOptional')}
        >
          {a.role === 'OPT-PARTICIPANT' ? $_('calendar.attendees.optional') : $_('calendar.attendees.required')}
        </button>
        <button
          type="button"
          class="rounded p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          onclick={() => removeAttendee(i)}
          disabled={disabled}
          aria-label={$_('calendar.attendees.remove')}
        >
          <Icon icon="mdi:close" class="w-3 h-3" />
        </button>
      </div>
    {/each}
  </div>

  <div class="relative">
    <input
      type="text"
      class="w-full rounded-md border border-input bg-background px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
      placeholder={placeholderText}
      bind:value={inputValue}
      bind:this={inputElement}
      oninput={handleInput}
      onkeydown={handleKeyDown}
      onblur={() => setTimeout(() => (showSuggestions = false), 150)}
      onfocus={() => {
        if (flattenedSuggestions.length > 0) showSuggestions = true
      }}
      disabled={disabled}
    />
    {#if showSuggestions && flattenedSuggestions.length > 0}
      <div
        class="absolute z-10 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-border bg-popover shadow-lg"
        role="listbox"
      >
        {#each flattenedSuggestions as s, idx (s.email + '-' + idx)}
          <button
            type="button"
            class={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent ${
              idx === selectedIndex ? 'bg-accent' : ''
            }`}
            onmousedown={(e) => {
              // mousedown (not click) — the input's onblur fires before
              // click and would close the panel first.
              e.preventDefault()
              addAttendee(s.email, s.contact.name || '')
            }}
            role="option"
            aria-selected={idx === selectedIndex}
          >
            <Avatar email={s.email} name={s.contact.name || s.email} density="compact" size={28} />
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm">{s.contact.name || s.email}</div>
              {#if s.contact.name}
                <div class="truncate text-xs text-muted-foreground">{s.email}</div>
              {/if}
            </div>
          </button>
        {/each}
      </div>
    {/if}
  </div>
</div>
