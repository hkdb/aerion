<script lang="ts">
  interface Props {
    checked?: boolean;
    disabled?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    id?: string;
    class?: string;
  }

  let {
    checked = $bindable(false),
    disabled = false,
    onCheckedChange,
    id,
    class: className = ""
  }: Props = $props();

  function handleClick() {
    if (disabled) return;
    checked = !checked;
    onCheckedChange?.(checked);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (disabled) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      handleClick();
    }
  }
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-disabled={disabled}
  aria-label={checked ? "Toggle on" : "Toggle off"}
  {id}
  class="h-6 w-11 focus-visible:ring-primary focus-visible:ring-offset-background relative inline-flex items-center rounded-full transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 {checked
    ? 'bg-primary'
    : 'bg-muted-foreground'} {className}"
  onclick={handleClick}
  onkeydown={handleKeyDown}
  {disabled}
>
  <span
    class="h-5 w-5 bg-background shadow-lg inline-block transform rounded-full transition-transform {checked
      ? 'translate-x-5'
      : 'translate-x-1'}"
  ></span>
</button>
