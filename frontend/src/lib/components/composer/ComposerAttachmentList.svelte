<script lang="ts">
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";

  import { formatFileSize, getFileIcon } from "./composerUtils";

  interface Attachment {
    filename: string;
    contentType: string;
    size: number;
    data: string;
  }

  interface Props {
    attachments: Attachment[];
    onRemove: (index: number) => void;
  }

  let { attachments, onRemove }: Props = $props();
</script>

{#if attachments.length > 0}
  <div class="px-4 py-2 border-border bg-muted/30 border-t">
    <div class="gap-2 flex flex-wrap">
      {#each attachments as attachment, index}
        <div
          class="gap-2 px-3 py-1.5 bg-background border-border rounded-md text-sm group flex items-center border"
        >
          <Icon
            icon={getFileIcon(attachment.contentType)}
            class="w-4 h-4 text-muted-foreground"
          />
          <span class="max-w-[150px] truncate" title={attachment.filename}>
            {attachment.filename}
          </span>
          <span class="text-xs text-muted-foreground">
            ({formatFileSize(attachment.size)})
          </span>
          <button
            onclick={() => onRemove(index)}
            class="ml-1 p-0.5 text-muted-foreground hover:text-destructive opacity-0 transition-colors group-hover:opacity-100"
            title={$_("attachment.removeAttachment")}
          >
            <Icon icon="mdi:close" class="w-3.5 h-3.5" />
          </button>
        </div>
      {/each}
    </div>
  </div>
{/if}
