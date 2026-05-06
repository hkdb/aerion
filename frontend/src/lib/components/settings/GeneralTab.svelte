<script lang="ts">
  import Icon from '@iconify/svelte'
  import * as Select from '$lib/components/ui/select'
  import { Label } from '$lib/components/ui/label'
  import { Input } from '$lib/components/ui/input'
  import Switch from '$lib/components/ui/switch/Switch.svelte'
  import { _, setLocale } from '$lib/i18n'
  import { supportedLocales } from '$lib/i18n'

  interface Props {
    markAsReadDelaySeconds: number
    messageListDensity: string
    themeMode: string
    nativeTitleBar: boolean
    showTitleBar: boolean
    runBackground: boolean
    startHidden: boolean
    autostart: boolean
    language: string
    onDelayChange: (value: number) => void
    onDensityChange: (value: string) => void
    onThemeChange: (value: string) => void
    onTitleBarChange: (nativeTitleBar: boolean, showTitleBar: boolean) => void
    onRunBackgroundChange: (value: boolean) => void
    onStartHiddenChange: (value: boolean) => void
    onAutostartChange: (value: boolean) => void
    onLanguageChange: (value: string) => void
    accentBarUnread: boolean
    showMessageListCircles: boolean
    showViewerCircles: boolean
  }

  let {
    markAsReadDelaySeconds = $bindable(),
    messageListDensity = $bindable(),
    themeMode = $bindable(),
    nativeTitleBar = $bindable(),
    showTitleBar = $bindable(),
    runBackground = $bindable(),
    startHidden = $bindable(),
    autostart = $bindable(),
    language = $bindable(),
    onDelayChange,
    onDensityChange,
    onThemeChange,
    onTitleBarChange,
    onRunBackgroundChange,
    onStartHiddenChange,
    onAutostartChange,
    onLanguageChange,
    accentBarUnread = $bindable(),
    showMessageListCircles = $bindable(),
    showViewerCircles = $bindable(),
  }: Props = $props()

  // Message list density options
  const densityOptions = $derived([
    { value: 'micro', label: $_('settingsGeneral.densityMicro') },
    { value: 'compact', label: $_('settingsGeneral.densityCompact') },
    { value: 'standard', label: $_('settingsGeneral.densityStandard') },
    { value: 'large', label: $_('settingsGeneral.densityLarge') },
  ])

  // Title bar options
  const titleBarOptions = $derived([
    { value: 'aerion', label: $_('settingsGeneral.titleBarAerion'), description: $_('settingsGeneral.titleBarAerionDesc') },
    { value: 'native', label: $_('settingsGeneral.titleBarNative'), description: $_('settingsGeneral.titleBarNativeDesc') },
    { value: 'disable', label: $_('settingsGeneral.titleBarDisable'), description: $_('settingsGeneral.titleBarDisableDesc') },
  ])

  const titleBarValue = $derived(
    nativeTitleBar ? 'native' : showTitleBar ? 'aerion' : 'disable'
  )

  // Theme mode options
  const themeModeOptions = $derived([
    { value: 'system', label: $_('settingsGeneral.themeSystem') },
    { value: 'light', label: $_('settingsGeneral.themeLight') },
    { value: 'light-blue', label: $_('settingsGeneral.themeLightBlue') },
    { value: 'light-orange', label: $_('settingsGeneral.themeLightOrange') },
    { value: 'light-balanced', label: $_('settingsGeneral.themeLightBalanced') },
    { value: 'dark', label: $_('settingsGeneral.themeDark') },
    { value: 'dark-gray', label: $_('settingsGeneral.themeDarkGray') },
    { value: 'dark-balanced', label: $_('settingsGeneral.themeDarkBalanced') },
  ])

  function getDensityLabel(value: string): string {
    return densityOptions.find(opt => opt.value === value)?.label || value
  }

  function getThemeModeLabel(value: string): string {
    return themeModeOptions.find(opt => opt.value === value)?.label || value
  }

  // Language picker
  function getLanguageLabel(code: string): string {
    return supportedLocales.find(l => l.code === code)?.name || code || 'English'
  }

  function handleDensityChange(value: string) {
    messageListDensity = value
    onDensityChange?.(value)
  }

  function handleThemeChange(value: string) {
    themeMode = value
    onThemeChange?.(value)
  }

  function handleTitleBarChange(value: string) {
    switch (value) {
      case 'aerion':
        nativeTitleBar = false
        showTitleBar = true
        break
      case 'native':
        nativeTitleBar = true
        showTitleBar = false
        break
      case 'disable':
        nativeTitleBar = false
        showTitleBar = false
        break
    }
    onTitleBarChange?.(nativeTitleBar, showTitleBar)
  }

  function getTitleBarLabel(value: string): string {
    return titleBarOptions.find(opt => opt.value === value)?.label || value
  }

  function handleDelayInput(e: Event) {
    const target = e.target as HTMLInputElement
    const value = parseFloat(target.value)
    markAsReadDelaySeconds = value
    onDelayChange?.(value)
  }

  function handleRunBackgroundChange(value: boolean) {
    runBackground = value
    if (!value) {
      startHidden = false
    }
    onRunBackgroundChange?.(value)
  }

  function handleStartHiddenChange(value: boolean) {
    startHidden = value
    if (value && !runBackground) {
      runBackground = true
      onRunBackgroundChange?.(true)
    }
    onStartHiddenChange?.(value)
  }

  function handleAutostartChange(value: boolean) {
    autostart = value
    onAutostartChange?.(value)
  }

  function handleLanguageChange(value: string) {
    language = value
    // Apply immediately for live preview
    setLocale(value)
    onLanguageChange?.(value)
  }


</script>

<div class="space-y-6">
  <!-- Display Section -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:format-size" class="w-4 h-4" />
      {$_('settingsGeneral.display')}
    </h3>

    <div class="space-y-2">
      <Label>{$_('settingsGeneral.titleBar')}</Label>
      <Select.Root value={titleBarValue} onValueChange={handleTitleBarChange}>
        <Select.Trigger>
          <Select.Value>
            {getTitleBarLabel(titleBarValue)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each titleBarOptions as opt (opt.value)}
            <Select.Item value={opt.value} label={opt.label} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_('settingsGeneral.titleBarHelp')}
      </p>
    </div>

    <div class="space-y-2">
      <Label>{$_('settingsGeneral.language')}</Label>
      <Select.Root value={language || 'en'} onValueChange={handleLanguageChange}>
        <Select.Trigger>
          <Select.Value>
            {getLanguageLabel(language || 'en')}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each supportedLocales as loc (loc.code)}
            <Select.Item value={loc.code} label={loc.name} />
          {/each}
        </Select.Content>
      </Select.Root>
    </div>

    <div class="space-y-2">
      <Label>{$_('settingsGeneral.theme')}</Label>
      <Select.Root value={themeMode} onValueChange={handleThemeChange}>
        <Select.Trigger>
          <Select.Value placeholder={$_('settingsGeneral.selectTheme')}>
            {getThemeModeLabel(themeMode)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each themeModeOptions as opt (opt.value)}
            <Select.Item value={opt.value} label={opt.label} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_('settingsGeneral.themeHelp')}
      </p>
    </div>

    <!-- Show colored circles in message list -->
    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div>
          <Label for="show-message-list-circles">{$_('settingsGeneral.showMessageListCircles')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.showMessageListCirclesHelp')}
          </p>
        </div>
        <Switch
          id="show-message-list-circles"
          bind:checked={showMessageListCircles}
        />
      </div>
    </div>

    <!-- Show colored circles in conversation viewer -->
    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div>
          <Label for="show-viewer-circles">{$_('settingsGeneral.showViewerCircles')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.showViewerCirclesHelp')}
          </p>
        </div>
        <Switch
          id="show-viewer-circles"
          bind:checked={showViewerCircles}
        />
      </div>
    </div>

    <!-- Accent bar for unread messages -->
    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div>
          <Label for="accent-bar-unread">{$_('settingsGeneral.accentBarUnread')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.accentBarUnreadHelp')}
          </p>
        </div>
        <Switch
          id="accent-bar-unread"
          bind:checked={accentBarUnread}
        />
      </div>
    </div>

    <div class="space-y-2">
      <Label>{$_('settingsGeneral.messageListDensity')}</Label>
      <Select.Root value={messageListDensity} onValueChange={handleDensityChange}>
        <Select.Trigger>
          <Select.Value placeholder={$_('settingsGeneral.selectDensity')}>
            {getDensityLabel(messageListDensity)}
          </Select.Value>
        </Select.Trigger>
        <Select.Content>
          {#each densityOptions as opt (opt.value)}
            <Select.Item value={opt.value} label={opt.label} />
          {/each}
        </Select.Content>
      </Select.Root>
      <p class="text-xs text-muted-foreground">
        {$_('settingsGeneral.messageListDensityHelp')}
      </p>
    </div>
  </div>

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Mark as Read Section -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:email-open-outline" class="w-4 h-4" />
      {$_('settingsGeneral.markAsRead')}
    </h3>

    <div class="space-y-2">
      <Label>{$_('settingsGeneral.markAsReadAfter')}</Label>
      <div class="flex items-center gap-2">
        <Input
          type="number"
          value={markAsReadDelaySeconds}
          oninput={handleDelayInput}
          min={-1}
          max={5}
          step={0.1}
          class="w-24"
        />
        <span class="text-sm text-muted-foreground">{$_('common.seconds')}</span>
      </div>
      <p class="text-xs text-muted-foreground">
        {$_('settingsGeneral.markAsReadHelp')}
      </p>
    </div>
  </div>

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Background Section -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:application-cog-outline" class="w-4 h-4" />
      {$_('settingsGeneral.background')}
    </h3>

    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label for="run-background">{$_('settingsGeneral.runInBackground')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.runInBackgroundHelp')}
          </p>
        </div>
        <Switch
          id="run-background"
          bind:checked={runBackground}
          onCheckedChange={handleRunBackgroundChange}
        />
      </div>
    </div>

    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label for="start-hidden" class={!runBackground ? 'text-muted-foreground' : ''}>{$_('settingsGeneral.startHidden')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.startHiddenHelp')}
          </p>
        </div>
        <Switch
          id="start-hidden"
          bind:checked={startHidden}
          onCheckedChange={handleStartHiddenChange}
          disabled={!runBackground}
        />
      </div>
    </div>
  </div>

  <!-- Divider -->
  <div class="border-t border-border"></div>

  <!-- Startup Section -->
  <div class="space-y-4">
    <h3 class="text-sm font-medium flex items-center gap-2">
      <Icon icon="mdi:power" class="w-4 h-4" />
      {$_('settingsGeneral.startup')}
    </h3>

    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label for="autostart">{$_('settingsGeneral.autostartOnLogin')}</Label>
          <p class="text-xs text-muted-foreground">
            {$_('settingsGeneral.autostartHelp')}
          </p>
        </div>
        <Switch
          id="autostart"
          bind:checked={autostart}
          onCheckedChange={handleAutostartChange}
        />
      </div>
    </div>
  </div>

</div>

