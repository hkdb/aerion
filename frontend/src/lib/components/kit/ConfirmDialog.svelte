<script lang="ts">
  // ConfirmDialog — kit-facing wrapper around the host's confirm-dialog primitive.
  //
  // Extensions consume THIS component (`$lib/components/kit/ConfirmDialog.svelte`)
  // instead of reaching into the host's `ui/` namespace
  // (`$lib/components/ui/confirm-dialog/...`). Insulates extensions from the
  // host's choice of underlying primitives (bits-ui today, possibly something
  // else tomorrow) and keeps the SDK surface stable.
  //
  // API mirrors the host primitive verbatim so passing props through is
  // mechanical. If the host adds new props, surface them here too.

  import HostConfirmDialog from '$lib/components/ui/confirm-dialog/ConfirmDialog.svelte'

  interface Props {
    /** Bindable open state. */
    open: boolean
    title: string
    description: string
    /** Default: "Confirm" */
    confirmLabel?: string
    /** Default: "Cancel" */
    cancelLabel?: string
    /** "destructive" applies red styling to the confirm button. Default: "default". */
    variant?: 'default' | 'destructive'
    /** Show a spinner on the confirm button and disable both buttons while true. */
    loading?: boolean
    onConfirm: () => void
    onCancel?: () => void
  }

  let {
    open = $bindable(false),
    title,
    description,
    confirmLabel,
    cancelLabel,
    variant,
    loading,
    onConfirm,
    onCancel,
  }: Props = $props()
</script>

<HostConfirmDialog
  bind:open
  {title}
  {description}
  {confirmLabel}
  {cancelLabel}
  {variant}
  {loading}
  {onConfirm}
  {onCancel}
/>
