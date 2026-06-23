<!--
  TypeSelect — curated dropdown + "Custom…" override for vCard TYPE attributes
  (e.g. HOME / WORK / CELL / FAX / etc.). Renders the existing ui/select
  primitive in the curated state; switches to an inline <input> when the user
  picks "Custom…". Switching back to a curated option restores the Select.

  Used by ContactEditDialog repeater rows for emails / phones / addresses /
  URLs / IMPPs to expose a consistent type picker across all those fields
  without inventing per-field component shapes.

  Standard option lists are exported alongside so callers don't redefine them
  per dialog section.
-->
<script lang="ts" module>
  export type TypeOption = { value: string; label: string }

  export const EMAIL_TYPES: TypeOption[] = [
    { value: 'HOME', label: 'Home' },
    { value: 'WORK', label: 'Work' },
    { value: 'INTERNET', label: 'Internet' },
    { value: 'OTHER', label: 'Other' },
  ]

  export const PHONE_TYPES: TypeOption[] = [
    { value: 'HOME', label: 'Home' },
    { value: 'WORK', label: 'Work' },
    { value: 'CELL', label: 'Cell' },
    { value: 'FAX', label: 'Fax' },
    { value: 'PAGER', label: 'Pager' },
    { value: 'VIDEO', label: 'Video' },
    { value: 'VOICE', label: 'Voice' },
    { value: 'OTHER', label: 'Other' },
  ]

  export const ADDRESS_TYPES: TypeOption[] = [
    { value: 'HOME', label: 'Home' },
    { value: 'WORK', label: 'Work' },
    { value: 'POSTAL', label: 'Postal' },
    { value: 'OTHER', label: 'Other' },
  ]

  export const URL_TYPES: TypeOption[] = [
    { value: 'HOME', label: 'Home' },
    { value: 'WORK', label: 'Work' },
    { value: 'OTHER', label: 'Other' },
  ]

  export const IMPP_TYPES: TypeOption[] = [
    { value: 'PERSONAL', label: 'Personal' },
    { value: 'WORK', label: 'Work' },
    { value: 'OTHER', label: 'Other' },
  ]

  // Sentinel value used to signal "user wants to type a custom TYPE." Picked
  // to be uglier than any real vCard TYPE so it can't collide.
  export const CUSTOM_TYPE_SENTINEL = '__custom__'
</script>

<script lang="ts">
  import * as Select from '$lib/components/ui/select'

  interface Props {
    value: string
    onValueChange: (v: string) => void
    options: TypeOption[]
    placeholder?: string
  }

  let { value, onValueChange, options, placeholder = 'Type' }: Props = $props()

  // Custom-mode tracking. `customMode` becomes true when the user picks the
  // sentinel option (or when an existing value isn't in the curated list).
  // Switching back to a curated option exits custom mode.
  let customMode = $state(false)

  $effect(() => {
    // Initialize / re-sync custom mode whenever the bound value changes.
    if (value && !options.some((o) => o.value === value)) {
      customMode = true
    }
  })

  const labelForValue = $derived(
    options.find((o) => o.value === value)?.label ?? (customMode ? 'Custom…' : placeholder),
  )

  function handleSelectChange(v: string) {
    if (v === CUSTOM_TYPE_SENTINEL) {
      customMode = true
      // Don't clear the value — let the user start from whatever's there.
      return
    }
    customMode = false
    onValueChange(v)
  }

  function handleInputChange(e: Event) {
    const target = e.target as HTMLInputElement
    onValueChange(target.value)
  }

  function exitCustomMode() {
    customMode = false
    onValueChange('')
  }
</script>

{#if customMode}
  <div class="flex gap-1 items-center">
    <input
      type="text"
      class="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      placeholder="Custom type"
      bind:value
      oninput={handleInputChange}
    />
    <button
      type="button"
      class="text-xs text-muted-foreground hover:text-foreground px-1"
      onclick={exitCustomMode}
      title="Back to standard types"
      aria-label="Cancel custom type"
    >
      ✕
    </button>
  </div>
{:else}
  <Select.Root value={value} onValueChange={handleSelectChange}>
    <Select.Trigger>
      <Select.Value placeholder={placeholder}>{labelForValue}</Select.Value>
    </Select.Trigger>
    <Select.Content>
      {#each options as opt (opt.value)}
        <Select.Item value={opt.value} label={opt.label} />
      {/each}
      <Select.Item value={CUSTOM_TYPE_SENTINEL} label="Custom…" />
    </Select.Content>
  </Select.Root>
{/if}
