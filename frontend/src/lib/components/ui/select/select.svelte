<script lang="ts">
  import { Select as SelectPrimitive } from "bits-ui";
  import type { Snippet } from "svelte";

  interface Props {
    value?: string;
    open?: boolean;
    onValueChange?: (value: string) => void;
    onOpenChange?: (open: boolean) => void;
    disabled?: boolean;
    name?: string;
    children?: Snippet;
  }

  let {
    value = $bindable(),
    open = $bindable(false),
    onValueChange,
    onOpenChange,
    disabled,
    name,
    children
  }: Props = $props();
</script>

<SelectPrimitive.Root
  type="single"
  bind:value
  bind:open
  {onValueChange}
  {onOpenChange}
  {disabled}
  {name}
>
  {#if children}
    {@render children()}
  {/if}
</SelectPrimitive.Root>
