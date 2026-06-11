<script lang="ts">
  // ColorPicker — kit-facing wrapper around the host's color-picker primitive.
  //
  // Extensions consume THIS component (`$lib/components/kit/ColorPicker.svelte`)
  // instead of reaching into the host's `ui/` namespace
  // (`$lib/components/ui/color-picker/...`). Same pass-through pattern as
  // ConfirmDialog — insulates extensions from the host's underlying primitive
  // and keeps the SDK surface stable.
  //
  // API mirrors the host primitive verbatim. If the host adds new props,
  // surface them here too.

  import HostColorPicker from '$lib/components/ui/color-picker/ColorPicker.svelte'

  interface Props {
    /** Current color as a 7-char hex string (e.g. "#3B82F6"). Empty string is allowed and renders the first preset. */
    value: string
    /** Called whenever the user picks a preset or enters a valid hex. */
    onchange: (color: string) => void
  }

  let { value, onchange }: Props = $props()
</script>

<HostColorPicker {value} {onchange} />
