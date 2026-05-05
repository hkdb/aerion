<script lang="ts">
  import { cn } from "$lib/utils";
  import { ContextMenu as ContextMenuPrimitive } from "bits-ui";

  interface Props {
    class?: string;
  }

  let { class: className }: Props = $props();
</script>

<ContextMenuPrimitive.Separator
  class={cn("-mx-1 my-1 bg-border h-px", className)}
/>
