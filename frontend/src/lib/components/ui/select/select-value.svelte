<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    placeholder?: string;
    class?: string;
    children?: Snippet;
  }

  let {
    placeholder = "Select...",
    class: className,
    children
  }: Props = $props();
</script>

<span class="block truncate {className}">
  {#if children}
    {@render children()}
  {:else}
    <span class="text-muted-foreground">{placeholder}</span>
  {/if}
</span>
