// Theme store - centralizes all theme application and system theme detection logic
//
// Used by both App.svelte (main window) and ComposerApp.svelte (detached composer).
// The OS theme probe is injected by the caller because each Wails process binds a
// different Go struct (App vs ComposerApp), and importing the wrong binding at the
// module level silently fails at runtime.

import { getThemeMode, getAccentColor, type ThemeMode } from './settings.svelte'

export type { ThemeMode }

/** Convert "#rrggbb" / "#rgb" to an "H S% L%" triple for a CSS custom property,
 *  or null if unparseable. Matches the format the themes use for --primary. */
function hexToHslTriple(hex: string): string | null {
  let h = hex.replace('#', '').trim()
  if (h.length === 3) h = h.split('').map((c) => c + c).join('')
  if (h.length !== 6) return null
  const n = parseInt(h, 16)
  if (Number.isNaN(n)) return null
  const r = ((n >> 16) & 255) / 255
  const g = ((n >> 8) & 255) / 255
  const b = (n & 255) / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  let hue = 0
  let sat = 0
  const d = max - min
  if (d !== 0) {
    sat = l > 0.5 ? d / (2 - max - min) : d / (max + min)
    switch (max) {
      case r: hue = (g - b) / d + (g < b ? 6 : 0); break
      case g: hue = (b - r) / d + 2; break
      default: hue = (r - g) / d + 4
    }
    hue /= 6
  }
  return `${Math.round(hue * 360)} ${Math.round(sat * 100)}% ${Math.round(l * 100)}%`
}

/** Pick a legible foreground (text-on-accent) HSL triple via WCAG luminance. */
function accentForegroundTriple(hex: string): string {
  let h = hex.replace('#', '').trim()
  if (h.length === 3) h = h.split('').map((c) => c + c).join('')
  const n = parseInt(h, 16)
  const lin = (v: number) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
  }
  const L = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  return L > 0.5 ? '240 10% 12%' : '0 0% 100%'
}

/** Override (or clear) the app accent. Sets inline --primary/--ring/--primary-foreground
 *  on <html>, which beats the stylesheet [data-theme] rules, so the chosen accent
 *  persists across light/dark theme switches. Empty hex clears the override and
 *  reverts to the active theme's default accent. */
/** Convert an "H S% L%" triple to "#rrggbb". */
function hslTripleToHex(triple: string): string | null {
  const m = triple.trim().match(/^([\d.]+)\s+([\d.]+)%\s+([\d.]+)%$/)
  if (!m) return null
  const h = parseFloat(m[1]) / 360
  const s = parseFloat(m[2]) / 100
  const l = parseFloat(m[3]) / 100
  const hue2rgb = (p: number, q: number, t: number) => {
    if (t < 0) t += 1
    if (t > 1) t -= 1
    if (t < 1 / 6) return p + (q - p) * 6 * t
    if (t < 1 / 2) return q
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6
    return p
  }
  let r: number, g: number, b: number
  if (s === 0) {
    r = g = b = l
  } else {
    const q = l < 0.5 ? l * (1 + s) : l + s - l * s
    const p = 2 * l - q
    r = hue2rgb(p, q, h + 1 / 3)
    g = hue2rgb(p, q, h)
    b = hue2rgb(p, q, h - 1 / 3)
  }
  const to2 = (v: number) => Math.round(v * 255).toString(16).padStart(2, '0')
  return `#${to2(r)}${to2(g)}${to2(b)}`
}

/** The accent currently in effect, as a hex string — the custom override if set,
 *  otherwise the active theme's default --primary. Used to seed the color picker. */
export function currentAccentHex(): string {
  const custom = getAccentColor()
  if (custom) return custom
  const triple = getComputedStyle(document.documentElement).getPropertyValue('--primary')
  return hslTripleToHex(triple) || '#7c3aed'
}

export function applyAccentColor(hex: string) {
  const root = document.documentElement
  const triple = hex ? hexToHslTriple(hex) : null
  if (!triple) {
    root.style.removeProperty('--primary')
    root.style.removeProperty('--ring')
    root.style.removeProperty('--primary-foreground')
    return
  }
  root.style.setProperty('--primary', triple)
  root.style.setProperty('--ring', triple)
  root.style.setProperty('--primary-foreground', accentForegroundTriple(hex))
}

// Internal state for portal-based system theme (XDG Settings Portal on Linux)
let portalThemeAvailable = false
let portalTheme: 'light' | 'dark' = 'light'

// Reactive flag mirroring the `.dark` class on <html>. Consumers (e.g., the
// email-content dark-filter toggle) need a Svelte-reactive way to observe it.
let isDarkActive = $state<boolean>(false)

export function getIsDarkActive(): boolean {
  return isDarkActive
}

/** Apply a resolved theme to the document element. The dark/light classification
 *  is read from the CSS-declared `color-scheme` property on the matching
 *  [data-theme="..."] block, so each theme owns its own scheme — no JS list to
 *  maintain. We mirror it as the `.dark` class so Tailwind `dark:` variants and
 *  any `.dark mark`-style selectors keep working. */
export function applyTheme(themeName: ThemeMode) {
  document.documentElement.setAttribute('data-theme', themeName)
  const scheme = getComputedStyle(document.documentElement).colorScheme.trim()
  const dark = scheme === 'dark'
  document.documentElement.classList.toggle('dark', dark)
  isDarkActive = dark
  // Re-assert the custom accent (if any) so it survives the theme swap.
  applyAccentColor(getAccentColor())
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
