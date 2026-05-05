// Runes-based settings store
// Provides reactive state for application settings
// @ts-ignore - wailsjs path
import { setLocale as setI18nLocale } from "$lib/i18n";
import { getDateFnsLocale, loadDateFnsLocale } from "$lib/i18n/dateFnsLocale";
import type { Locale } from "date-fns";

import {
  GetAccentBarUnread,
  GetAlwaysLoadImages,
  GetAutostart,
  GetComposerFormat,
  GetComposerMode,
  GetLanguage,
  GetMailtoMode,
  GetMessageListDensity,
  GetMessageListSortOrder,
  GetNativeTitleBar,
  GetRunBackground,
  GetShowTitleBar,
  GetStartHidden,
  GetThemeMode
} from "../../../wailsjs/go/app/App";

export type ComposerMode = "inline" | "detached";
export type ComposerFormat = "rich" | "plain";
export type MessageListDensity = "micro" | "compact" | "standard" | "large";
export type MessageListSortOrder = "newest" | "oldest";
export type ThemeMode =
  | "system"
  | "light"
  | "light-blue"
  | "light-orange"
  | "light-balanced"
  | "dark"
  | "dark-gray"
  | "dark-balanced"
  | "dark-catppuccin-mocha";

// Module-level reactive state
let messageListDensity = $state<MessageListDensity>("standard");
let messageListSortOrder = $state<MessageListSortOrder>("newest");
let themeMode = $state<ThemeMode>("system");
let showTitleBar = $state<boolean>(true);
let runBackground = $state<boolean>(false);
let startHidden = $state<boolean>(false);
let autostart = $state<boolean>(false);
let language = $state<string>("");
let composerMode = $state<ComposerMode>("inline");
let mailtoMode = $state<ComposerMode>("inline");
let composerFormat = $state<ComposerFormat>("rich");
let nativeTitleBar = $state<boolean>(false);
let alwaysLoadImages = $state<boolean>(false);
let accentBarUnread = $state<boolean>(false);

// Getter functions to access the state
export function getMessageListDensity(): MessageListDensity {
  return messageListDensity;
}

export function getMessageListSortOrder(): MessageListSortOrder {
  return messageListSortOrder;
}

export function getThemeMode(): ThemeMode {
  return themeMode;
}

export function getShowTitleBar(): boolean {
  return showTitleBar;
}

export function getRunBackground(): boolean {
  return runBackground;
}

export function getStartHidden(): boolean {
  return startHidden;
}

export function getAutostart(): boolean {
  return autostart;
}

export function getLanguage(): string {
  return language;
}

export function getComposerMode(): ComposerMode {
  return composerMode;
}

export function getMailtoMode(): ComposerMode {
  return mailtoMode;
}

export function getComposerFormat(): ComposerFormat {
  return composerFormat;
}

export function getNativeTitleBar(): boolean {
  return nativeTitleBar;
}

export function getAlwaysLoadImages(): boolean {
  return alwaysLoadImages;
}

export function getAccentBarUnread(): boolean {
  return accentBarUnread;
}

export function getCurrentDateFnsLocale(): Locale | undefined {
  return getDateFnsLocale(language || "en");
}

// Setter functions to update the state
export function setMessageListDensity(density: MessageListDensity) {
  messageListDensity = density;
}

export function setMessageListSortOrder(sortOrder: MessageListSortOrder) {
  messageListSortOrder = sortOrder;
}

export function setThemeMode(mode: ThemeMode) {
  themeMode = mode;
}

export function setShowTitleBar(show: boolean) {
  showTitleBar = show;
}

export function setRunBackground(v: boolean) {
  runBackground = v;
}

export function setStartHidden(v: boolean) {
  startHidden = v;
}

export function setAutostart(v: boolean) {
  autostart = v;
}

export function setLanguage(lang: string) {
  language = lang;
  if (lang) {
    setI18nLocale(lang);
    loadDateFnsLocale(lang);
  }
}

export function setComposerMode(mode: ComposerMode) {
  composerMode = mode;
}

export function setMailtoMode(mode: ComposerMode) {
  mailtoMode = mode;
}

export function setComposerFormat(format: ComposerFormat) {
  composerFormat = format;
}

export function setNativeTitleBar(v: boolean) {
  nativeTitleBar = v;
}

export function setAlwaysLoadImages(v: boolean) {
  alwaysLoadImages = v;
}

export function setAccentBarUnread(v: boolean) {
  accentBarUnread = v;
}

// Load settings from backend (call on app startup)
export async function loadSettings(): Promise<ThemeMode> {
  try {
    const [
      density,
      sortOrder,
      theme,
      titleBar,
      runBg,
      startHid,
      autoSt,
      lang,
      compMode,
      mailMode,
      compFormat,
      nativeTB,
      alwaysImages,
      accentBar
    ] = await Promise.all([
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
      GetAccentBarUnread()
    ]);
    messageListDensity = (density as MessageListDensity) || "standard";
    messageListSortOrder = (sortOrder as MessageListSortOrder) || "newest";
    themeMode = (theme as ThemeMode) || "system";
    showTitleBar = titleBar ?? true; // Default to true
    runBackground = runBg ?? false;
    startHidden = startHid ?? false;
    autostart = autoSt ?? false;
    composerMode = (compMode as ComposerMode) || "inline";
    mailtoMode = (mailMode as ComposerMode) || "inline";
    composerFormat = (compFormat as ComposerFormat) || "rich";
    nativeTitleBar = nativeTB ?? false;
    alwaysLoadImages = alwaysImages ?? false;
    accentBarUnread = accentBar ?? false;
    // Apply saved language (if set, overrides system detection from initI18n)
    if (lang) {
      language = lang;
      setI18nLocale(lang);
      await loadDateFnsLocale(lang);
    }
    return themeMode;
  } catch (err) {
    console.error("Failed to load settings:", err);
    return "system";
  }
}
