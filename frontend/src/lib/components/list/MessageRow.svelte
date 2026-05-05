<script lang="ts">
  import { getAccentBarUnread } from "$lib/stores/settings.svelte";
  import type { MessageHeader } from "$lib/types";
  import { formatRelativeDate } from "$lib/utils/date";
  import Icon from "@iconify/svelte";

  interface Props {
    message: MessageHeader;
    selected: boolean;
    onSelect: () => void;
  }

  let { message, selected, onSelect }: Props = $props();

  function getInitials(name: string): string {
    return name
      .split(" ")
      .map((n) => n[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  }

  function getAvatarColor(email: string): string {
    const colors = [
      "bg-red-500",
      "bg-orange-500",
      "bg-amber-500",
      "bg-yellow-500",
      "bg-lime-500",
      "bg-green-500",
      "bg-emerald-500",
      "bg-teal-500",
      "bg-cyan-500",
      "bg-sky-500",
      "bg-blue-500",
      "bg-indigo-500",
      "bg-violet-500",
      "bg-purple-500",
      "bg-fuchsia-500",
      "bg-pink-500"
    ];
    let hash = 0;
    for (let i = 0; i < email.length; i++) {
      hash = email.charCodeAt(i) + ((hash << 5) - hash);
    }
    return colors[Math.abs(hash) % colors.length];
  }

  function handleStarClick(e: MouseEvent) {
    e.stopPropagation();
    // TODO: Toggle star
  }
</script>

<div
  class="gap-3 px-4 py-3 border-border flex w-full cursor-pointer items-start border-b text-left transition-colors {selected
    ? 'bg-primary/10'
    : 'hover:bg-muted/50'} {getAccentBarUnread() && message.unread
    ? 'border-l-primary border-l-2'
    : ''}"
  onclick={onSelect}
  onkeydown={(e) => e.key === "Enter" && onSelect()}
  role="button"
  tabindex="0"
>
  <!-- Avatar -->
  <div
    class="w-10 h-10 text-white text-sm font-medium flex flex-shrink-0 items-center justify-center rounded-full {getAvatarColor(
      message.from.email
    )}"
  >
    {getInitials(message.from.name)}
  </div>

  <!-- Content -->
  <div class="min-w-0 flex-1">
    <div class="gap-2 mb-0.5 flex items-center">
      <!-- Sender Name -->
      <span
        class="truncate {message.unread
          ? 'font-semibold text-foreground'
          : 'text-foreground'}"
      >
        {message.from.name}
      </span>

      <!-- Indicators -->
      <div class="gap-1 flex flex-shrink-0 items-center">
        {#if message.hasAttachment}
          <Icon
            icon="mdi:paperclip"
            class="w-3.5 h-3.5 text-muted-foreground"
          />
        {/if}
      </div>

      <!-- Date -->
      <span class="text-xs text-muted-foreground ml-auto flex-shrink-0">
        {formatRelativeDate(message.date)}
      </span>
    </div>

    <!-- Subject -->
    <p
      class="text-sm truncate {message.unread
        ? 'font-medium text-foreground'
        : 'text-muted-foreground'}"
    >
      {message.subject}
    </p>

    <!-- Snippet -->
    <p class="text-sm text-muted-foreground truncate">
      {message.snippet}
    </p>
  </div>

  <!-- Star -->
  <button
    class="p-1 -mr-1 rounded hover:bg-muted flex-shrink-0 transition-colors"
    onclick={handleStarClick}
  >
    <Icon
      icon={message.starred ? "mdi:star" : "mdi:star-outline"}
      class="w-4 h-4 {message.starred
        ? 'text-yellow-500'
        : 'text-muted-foreground'}"
    />
  </button>
</div>
