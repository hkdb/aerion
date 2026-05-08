// Theme store - centralizes all theme application and system theme detection logic
//
// Used by both App.svelte (main window) and ComposerApp.svelte (detached composer).

// @ts-ignore - wailsjs path
import { GetSystemTheme } from '../../../wailsjs/go/app/App'
import { getThemeMode, type ThemeMode } from './settings.svelte'

export type { ThemeMode }

// Internal state for portal-based system theme (XDG Settings Portal on Linux)
let portalThemeAvailable = false
let portalTheme: 'light' | 'dark' = 'light'

/** Apply a resolved theme to the document element. */
export function applyTheme(themeName: ThemeMode) {
  document.documentElement.setAttribute('data-theme', themeName)

  // Legacy: Also set .dark class for backwards compat
  document.documentElement.classList.remove('dark')
  if (themeName.startsWith('dark')) {
    document.documentElement.classList.add('dark')
  }
}

/** Resolve a ThemeMode (which may be 'system') to a concrete theme and apply it. */
export function applyThemeFromMode(mode: ThemeMode) {
  if (mode !== 'system') {
    applyTheme(mode)
    return
  }

  // System mode: use portal-based theme if available, otherwise fall back to matchMedia
  if (portalThemeAvailable) {
    applyTheme(portalTheme)
    return
  }

  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  applyTheme(mediaQuery.matches ? 'dark' : 'light')
}

/**
 * Initialize the theme on mount.
 * Probes the XDG Settings Portal for system theme, then applies the stored mode.
 */
export async function initTheme(storedMode: ThemeMode) {
  try {
    const sysTheme = await GetSystemTheme()
    if (sysTheme === 'light' || sysTheme === 'dark') {
      portalThemeAvailable = true
      portalTheme = sysTheme
    }
  } catch {
    // Portal not available, will use matchMedia fallback
  }

  applyThemeFromMode(storedMode)
}

/** Handle backend 'theme:system-preference' events (XDG Settings Portal changes). */
export function handleSystemThemeEvent(newTheme: string) {
  if (newTheme !== 'light' && newTheme !== 'dark') return

  portalThemeAvailable = true
  portalTheme = newTheme
  if (getThemeMode() === 'system') {
    applyTheme(portalTheme)
  }
}

/** Handle matchMedia 'change' events (fallback when portal is unavailable). */
export function handleMediaQueryChange(matches: boolean) {
  if (getThemeMode() !== 'system' || portalThemeAvailable) return
  applyTheme(matches ? 'dark' : 'light')
}

/** Handle 'theme:changed' IPC events for composer windows. */
export function handleThemeChanged(newTheme: string) {
  if (newTheme === 'system') {
    applyThemeFromMode('system')
    return
  }
  applyTheme(newTheme as ThemeMode)
}
