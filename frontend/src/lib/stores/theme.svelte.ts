// Theme store - centralizes all theme application and system theme detection logic
//
// Used by both App.svelte (main window) and ComposerApp.svelte (detached composer).
// The OS theme probe is injected by the caller because each Wails process binds a
// different Go struct (App vs ComposerApp), and importing the wrong binding at the
// module level silently fails at runtime.

import { getThemeMode, type ThemeMode } from './settings.svelte'

export type { ThemeMode }

// Internal state for portal-based system theme (XDG Settings Portal on Linux)
let portalThemeAvailable = false
let portalTheme: 'light' | 'dark' = 'light'

/** Apply a resolved theme to the document element. The dark/light classification
 *  is read from the CSS-declared `color-scheme` property on the matching
 *  [data-theme="..."] block, so each theme owns its own scheme — no JS list to
 *  maintain. We mirror it as the `.dark` class so Tailwind `dark:` variants and
 *  any `.dark mark`-style selectors keep working. */
export function applyTheme(themeName: ThemeMode) {
  document.documentElement.setAttribute('data-theme', themeName)
  const scheme = getComputedStyle(document.documentElement).colorScheme.trim()
  document.documentElement.classList.toggle('dark', scheme === 'dark')
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
 * Probes the XDG Settings Portal for system theme via the caller-supplied binding,
 * then applies the stored mode.
 */
export async function initTheme(
  storedMode: ThemeMode,
  getSystemTheme: () => Promise<string>,
) {
  try {
    const sysTheme = await getSystemTheme()
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
