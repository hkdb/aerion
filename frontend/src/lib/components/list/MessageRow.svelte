<script lang="ts">
  import Icon from '@iconify/svelte'
  import { formatRelativeDate } from '$lib/utils/date'
  import type { MessageHeader } from '$lib/types'
  import { getAccentBarUnread } from '$lib/stores/settings.svelte'

  interface Props {
    message: MessageHeader
    selected: boolean
    onSelect: () => void
  }

  let { message, selected, onSelect }: Props = $props()

  function getInitials(name: string): string {
    return name
      .split(' ')
      .map(n => n[0])
      .join('')
      .toUpperCase()
      .slice(0, 2)
  }

  function getAvatarColor(email: string): string {
    // Returns a theme-driven avatar class (.avatar-1 .. .avatar-14, defined in themes.css).
    let hash = 0
    for (let i = 0; i < email.length; i++) {
      hash = email.charCodeAt(i) + ((hash << 5) - hash)
    }
    return `avatar-${(Math.abs(hash) % 14) + 1}`
  }

  function handleStarClick(e: MouseEvent) {
    e.stopPropagation()
    // TODO: Toggle star
  }
</script>

<div
  class="w-full flex items-start gap-3 px-4 py-3 text-left border-b border-border transition-colors cursor-pointer {
    selected
      ? 'bg-primary/10'
      : 'hover:bg-muted/50'
  } {getAccentBarUnread() && message.unread ? 'border-l-2 border-l-primary' : ''}"
  onclick={onSelect}
  onkeydown={(e) => e.key === 'Enter' && onSelect()}
  role="button"
  tabindex="0"
>
  <!-- Avatar -->
  <div
    class="w-10 h-10 rounded-full flex-shrink-0 flex items-center justify-center text-sm font-medium {getAvatarColor(message.from.email)}"
  >
    {getInitials(message.from.name)}
  </div>

  <!-- Content -->
  <div class="flex-1 min-w-0">
    <div class="flex items-center gap-2 mb-0.5">
      <!-- Sender Name -->
      <span
        class="truncate {message.unread ? 'font-semibold text-foreground' : 'text-foreground'}"
      >
        {message.from.name}
      </span>

      <!-- Indicators -->
      <div class="flex items-center gap-1 flex-shrink-0">
        {#if message.hasAttachment}
          <Icon icon="mdi:paperclip" class="w-3.5 h-3.5 text-muted-foreground" />
        {/if}
      </div>

      <!-- Date -->
      <span class="text-xs text-muted-foreground flex-shrink-0 ml-auto">
        {formatRelativeDate(message.date)}
      </span>
    </div>

    <!-- Subject -->
    <p class="truncate text-sm {message.unread ? 'font-medium text-foreground' : 'text-muted-foreground'}">
      {message.subject}
    </p>

    <!-- Snippet -->
    <p class="truncate text-sm text-muted-foreground">
      {message.snippet}
    </p>
  </div>

  <!-- Star -->
  <button
    class="flex-shrink-0 p-1 -mr-1 rounded hover:bg-muted transition-colors"
    onclick={handleStarClick}
  >
    <Icon
      icon={message.starred ? 'mdi:star' : 'mdi:star-outline'}
      class="w-4 h-4 {message.starred ? 'text-yellow-500' : 'text-muted-foreground'}"
    />
  </button>
</div>
