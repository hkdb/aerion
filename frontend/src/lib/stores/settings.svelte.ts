// Runes-based settings store
// Provides reactive state for application settings

// @ts-ignore - wailsjs path
import { GetMessageListDensity, GetMessageListSortOrder, GetThemeMode, GetShowTitleBar, GetRunBackground, GetStartHidden, GetAutostart, GetLanguage, GetComposerMode, GetMailtoMode, GetComposerFormat, GetNativeTitleBar, GetAlwaysLoadImages, GetDarkMailContent, GetOverrideEmailColors, SetOverrideEmailColors, GetAccentBarUnread, GetShowMessageListCircles, GetShowViewerCircles, GetShowActionToasts } from '../../../wailsjs/go/app/App'
import { setLocale as setI18nLocale } from '$lib/i18n'
import { loadDateFnsLocale, getDateFnsLocale } from '$lib/i18n/dateFnsLocale'
import type { Locale } from 'date-fns'

export type ComposerMode = 'inline' | 'detached'
export type ComposerFormat = 'rich' | 'plain'
export type MessageListDensity = 'micro' | 'compact' | 'standard' | 'large'
export type MessageListSortOrder = 'newest' | 'oldest'
export type ThemeMode =
  | 'system'
  | 'light'
  | 'light-blue'
  | 'light-orange'
  | 'light-balanced'
  | 'adwaita-light'
  | 'breeze-light'
  | 'dark'
  | 'dark-gray'
  | 'dark-balanced'
  | 'adwaita-dark'
  | 'breeze-dark'
  | 'catppuccin-latte'
  | 'catppuccin-frappe'
  | 'catppuccin-macchiato'
  | 'catppuccin-mocha'
  | 'dracula'
  | 'github-light'
  | 'github-dark'
  | 'github-soft-dark'
  | 'tokyo-night'
  | 'nord-light'
  | 'nord-dark'
  | 'pop-light'
  | 'pop-dark'
  | 'yaru-light'
  | 'yaru-dark'
  | 'vs-code-light'
  | 'vs-code-dark'
  | 'ethereal'
  | 'everforest'
  | 'flexoki-light'
  | 'gruvbox'
  | 'hackerman'
  | 'kanagawa'
  | 'lumon'
  | 'matte-black'
  | 'miasma'
  | 'osaka-jade'
  | 'retro-82'
  | 'ristretto'
  | 'rose-pine'
  | 'vantablack'
  | 'white'
  | 'flexoki-dark'

// Module-level reactive state
let messageListDensity = $state<MessageListDensity>('standard')
let messageListSortOrder = $state<MessageListSortOrder>('newest')
let themeMode = $state<ThemeMode>('system')
let showTitleBar = $state<boolean>(true)
let runBackground = $state<boolean>(false)
let startHidden = $state<boolean>(false)
let autostart = $state<boolean>(false)
let language = $state<string>('')
let composerMode = $state<ComposerMode>('inline')
let mailtoMode = $state<ComposerMode>('inline')
let composerFormat = $state<ComposerFormat>('rich')
let nativeTitleBar = $state<boolean>(false)
let alwaysLoadImages = $state<boolean>(false)
let darkMailContent = $state<boolean>(false)
let overrideEmailColors = $state<boolean>(false)
let accentBarUnread = $state<boolean>(false)
let showMessageListCircles = $state<boolean>(true)
let showViewerCircles = $state<boolean>(true)
let showActionToasts = $state<boolean>(true)

// Getter functions to access the state
export function getMessageListDensity(): MessageListDensity {
  return messageListDensity
}

export function getMessageListSortOrder(): MessageListSortOrder {
  return messageListSortOrder
}

export function getThemeMode(): ThemeMode {
  return themeMode
}

export function getShowTitleBar(): boolean {
  return showTitleBar
}

export function getRunBackground(): boolean {
  return runBackground
}

export function getStartHidden(): boolean {
  return startHidden
}

export function getAutostart(): boolean {
  return autostart
}

export function getLanguage(): string {
  return language
}

export function getComposerMode(): ComposerMode {
  return composerMode
}

export function getMailtoMode(): ComposerMode {
  return mailtoMode
}

export function getComposerFormat(): ComposerFormat {
  return composerFormat
}

export function getNativeTitleBar(): boolean {
  return nativeTitleBar
}

export function getAlwaysLoadImages(): boolean {
  return alwaysLoadImages
}

export function getDarkMailContent(): boolean {
  return darkMailContent
}

export function getOverrideEmailColors(): boolean {
  return overrideEmailColors
}

export function getAccentBarUnread(): boolean {
  return accentBarUnread
}

export function getShowMessageListCircles(): boolean {
  return showMessageListCircles
}

export function getShowViewerCircles(): boolean {
  return showViewerCircles
}

export function getShowActionToasts(): boolean {
  return showActionToasts
}

export function getCurrentDateFnsLocale(): Locale | undefined {
  return getDateFnsLocale(language || 'en')
}

// Setter functions to update the state
export function setMessageListDensity(density: MessageListDensity) {
  messageListDensity = density
}

export function setMessageListSortOrder(sortOrder: MessageListSortOrder) {
  messageListSortOrder = sortOrder
}

export function setThemeMode(mode: ThemeMode) {
  themeMode = mode
}

export function setShowTitleBar(show: boolean) {
  showTitleBar = show
}

export function setRunBackground(v: boolean) {
  runBackground = v
}

export function setStartHidden(v: boolean) {
  startHidden = v
}

export function setAutostart(v: boolean) {
  autostart = v
}

export function setLanguage(lang: string) {
  language = lang
  if (lang) {
    setI18nLocale(lang)
    loadDateFnsLocale(lang)
  }
}

export function setComposerMode(mode: ComposerMode) {
  composerMode = mode
}

export function setMailtoMode(mode: ComposerMode) {
  mailtoMode = mode
}

export function setComposerFormat(format: ComposerFormat) {
  composerFormat = format
}

export function setNativeTitleBar(v: boolean) {
  nativeTitleBar = v
}

export function setAlwaysLoadImages(v: boolean) {
  alwaysLoadImages = v
}

export function setDarkMailContent(v: boolean) {
  darkMailContent = v
}

export function setOverrideEmailColors(v: boolean) {
  overrideEmailColors = v
}

export function setAccentBarUnread(v: boolean) {
  accentBarUnread = v
}

export function setShowMessageListCircles(v: boolean) {
  showMessageListCircles = v
}

export function setShowViewerCircles(v: boolean) {
  showViewerCircles = v
}

export function setShowActionToasts(v: boolean) {
  showActionToasts = v
}

// Load settings from backend (call on app startup)
export async function loadSettings(): Promise<ThemeMode> {
  try {
    const [density, sortOrder, theme, titleBar, runBg, startHid, autoSt, lang, compMode, mailMode, compFormat, nativeTB, alwaysImages, darkMail, overrideColors, accentBar, listCircles, viewerCircles, actionToasts] = await Promise.all([
      GetMessageListDensity(),
      GetMessageListSortOrder(),
      GetThemeMode(),
      GetShowTitleBar(),
      GetRunBackground(),
      GetStartHidden(),
      GetAutostart(),
      GetLanguage(),
      GetComposerMode(),
      GetMailtoMode(),
      GetComposerFormat(),
      GetNativeTitleBar(),
      GetAlwaysLoadImages(),
      GetDarkMailContent(),
      GetOverrideEmailColors(),
      GetAccentBarUnread(),
      GetShowMessageListCircles(),
      GetShowViewerCircles(),
      GetShowActionToasts(),
    ])
    messageListDensity = (density as MessageListDensity) || 'standard'
    messageListSortOrder = (sortOrder as MessageListSortOrder) || 'newest'
    themeMode = (theme as ThemeMode) || 'system'
    showTitleBar = titleBar ?? true // Default to true
    runBackground = runBg ?? false
    startHidden = startHid ?? false
    autostart = autoSt ?? false
    composerMode = (compMode as ComposerMode) || 'inline'
    mailtoMode = (mailMode as ComposerMode) || 'inline'
    composerFormat = (compFormat as ComposerFormat) || 'rich'
    nativeTitleBar = nativeTB ?? false
    alwaysLoadImages = alwaysImages ?? false
    darkMailContent = darkMail ?? false
    overrideEmailColors = overrideColors ?? false
    accentBarUnread = accentBar ?? false
    showMessageListCircles = listCircles ?? true
    showViewerCircles = viewerCircles ?? true
    showActionToasts = actionToasts ?? true
    // Apply saved language (if set, overrides system detection from initI18n)
    if (lang) {
      language = lang
      setI18nLocale(lang)
      await loadDateFnsLocale(lang)
    }
    return themeMode
  } catch (err) {
    console.error('Failed to load settings:', err)
    return 'system'
  }
}
