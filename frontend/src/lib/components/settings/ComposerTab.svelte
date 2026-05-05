<script lang="ts">
  import { Label } from "$lib/components/ui/label";
  import * as Select from "$lib/components/ui/select";
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";

  interface Props {
    composerMode: string;
    mailtoMode: string;
    composerFormat: string;
    readReceiptResponsePolicy: string;
    onComposerModeChange: (value: string) => void;
    onMailtoModeChange: (value: string) => void;
    onFormatChange: (value: string) => void;
    onPolicyChange: (value: string) => void;
  }

  let {
    composerMode = $bindable(),
    mailtoMode = $bindable(),
    composerFormat = $bindable(),
    readReceiptResponsePolicy = $bindable(),
    onComposerModeChange,
    onMailtoModeChange,
    onFormatChange,
    onPolicyChange
  }: Props = $props();

  const modeOptions = $derived([
    { value: "inline", label: $_("settings.composerModeInline") },
    { value: "detached", label: $_("settings.composerModeDetached") }
  ]);

  const formatOptions = $derived([
    { value: "rich", label: $_("settings.composerFormatRich") },
    { value: "plain", label: $_("settings.composerFormatPlain") }
  ]);

  const readReceiptResponseOptions = $derived([
    { value: "never", label: $_("settingsGeneral.neverSendReceipts") },
    { value: "ask", label: $_("settingsGeneral.askEachTime") },
    { value: "always", label: $_("settingsGeneral.alwaysSendReceipts") }
  ]);

  function getModeLabel(mode: string): string {
    return modeOptions.find((o) => o.value === mode)?.label ?? mode;
  }

  function getPolicyLabel(value: string): string {
    return (
      readReceiptResponseOptions.find((o) => o.value === value)?.label ?? value
    );
  }

  function handleComposerModeChange(value: string | undefined) {
    if (!value) return;
    composerMode = value;
    onComposerModeChange?.(value);
  }

  function handleMailtoModeChange(value: string | undefined) {
    if (!value) return;
    mailtoMode = value;
    onMailtoModeChange?.(value);
  }

  function getFormatLabel(value: string): string {
    return formatOptions.find((o) => o.value === value)?.label ?? value;
  }

  function handleFormatChange(value: string | undefined) {
    if (!value) return;
    composerFormat = value;
    onFormatChange?.(value);
  }

  function handlePolicyChange(value: string | undefined) {
    if (!value) return;
    readReceiptResponsePolicy = value;
    onPolicyChange?.(value);
  }
</script>

<div class="space-y-6 p-1">
  <div class="space-y-2">
    <Label>{$_("settings.composerMode")}</Label>
    <Select.Root value={composerMode} onValueChange={handleComposerModeChange}>
      <Select.Trigger>
        <Select.Value placeholder={$_("settings.composerMode")}>
          {getModeLabel(composerMode)}
        </Select.Value>
      </Select.Trigger>
      <Select.Content>
        {#each modeOptions as opt (opt.value)}
          <Select.Item value={opt.value} label={opt.label} />
        {/each}
      </Select.Content>
    </Select.Root>
    <p class="text-xs text-muted-foreground">
      {$_("settings.composerModeDescription")}
    </p>
  </div>

  <div class="space-y-2">
    <Label>{$_("settings.mailtoMode")}</Label>
    <Select.Root value={mailtoMode} onValueChange={handleMailtoModeChange}>
      <Select.Trigger>
        <Select.Value placeholder={$_("settings.mailtoMode")}>
          {getModeLabel(mailtoMode)}
        </Select.Value>
      </Select.Trigger>
      <Select.Content>
        {#each modeOptions as opt (opt.value)}
          <Select.Item value={opt.value} label={opt.label} />
        {/each}
      </Select.Content>
    </Select.Root>
    <p class="text-xs text-muted-foreground">
      {$_("settings.mailtoModeDescription")}
    </p>
  </div>

  <div class="space-y-2">
    <Label>{$_("settings.composerFormat")}</Label>
    <Select.Root value={composerFormat} onValueChange={handleFormatChange}>
      <Select.Trigger>
        <Select.Value placeholder={$_("settings.composerFormat")}>
          {getFormatLabel(composerFormat)}
        </Select.Value>
      </Select.Trigger>
      <Select.Content>
        {#each formatOptions as opt (opt.value)}
          <Select.Item value={opt.value} label={opt.label} />
        {/each}
      </Select.Content>
    </Select.Root>
    <p class="text-xs text-muted-foreground">
      {$_("settings.composerFormatDescription")}
    </p>
  </div>

  <!-- Divider -->
  <div class="border-border border-t"></div>

  <!-- Read Receipts Section -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium gap-2 flex items-center">
      <Icon icon="mdi:email-check-outline" class="w-4 h-4" />
      {$_("settingsGeneral.readReceipts")}
    </h3>

    <div class="space-y-2">
      <Label>{$_("settingsGeneral.readReceiptPolicy")}</Label>
      <Select.Root
        value={readReceiptResponsePolicy}
        onValueChange={handlePolicyChange}
      >
        <Select.Trigger>
          <Select.Value placeholder={$_("settingsGeneral.selectPolicy")}>
            {getPolicyLabel(readReceiptResponsePolicy)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each readReceiptResponseOptions as opt (opt.value)}
            <Select.Item value={opt.value} label={opt.label} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_("settingsGeneral.readReceiptPolicyHelp")}
      </p>
    </div>
  </div>
</div>
