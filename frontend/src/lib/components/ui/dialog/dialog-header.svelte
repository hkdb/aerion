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
  class={cn("space-y-1.5 sm:text-left flex flex-col text-center", className)}
>
  {#if children}
    {@render children()}
  {/if}
</div>
