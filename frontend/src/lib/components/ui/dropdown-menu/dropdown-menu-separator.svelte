<script lang="ts">
  import { cn } from "$lib/utils";
  import { DropdownMenu as DropdownMenuPrimitive } from "bits-ui";

  interface Props {
    class?: string;
  }

  let { class: className }: Props = $props();
</script>

<DropdownMenuPrimitive.Separator
  class={cn("-mx-1 my-1 bg-border h-px", className)}
/>
