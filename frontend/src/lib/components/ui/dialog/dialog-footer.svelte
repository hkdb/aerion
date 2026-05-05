<script lang="ts">
  import { cn } from "$lib/utils";
  import type { Snippet } from "svelte";

  interface Props {
    class?: string;
    children?: Snippet;
  }

  let { class: className, children }: Props = $props();
</script>

<div
  class={cn(
    "sm:flex-row sm:justify-end sm:space-x-2 flex flex-col-reverse",
    className
  )}
>
  {#if children}
    {@render children()}
  {/if}
</div>
