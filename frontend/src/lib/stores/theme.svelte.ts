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

// Reactive flag mirroring the `.dark` class on <html>. Consumers (e.g., the
// email-content dark-filter toggle) need a Svelte-reactive way to observe it.
let isDarkActive = $state<boolean>(false)

export function getIsDarkActive(): boolean {
  return isDarkActive
}

function parseHsl(hslStr: string): { h: number, s: number, l: number } | null {
  if (!hslStr) return null
  const parts = hslStr.split(/[\s,]+/).map(p => p.trim()).filter(p => p.length > 0)
  if (parts.length >= 3) {
    const h = parseFloat(parts[0])
    const s = parseFloat(parts[1])
    const l = parseFloat(parts[2])
    if (!isNaN(h) && !isNaN(s) && !isNaN(l)) {
      return { h, s, l }
    }
  }
  return null
}

function getLuminance(h: number, s: number, l: number): number {
  s /= 100
  l /= 100
  h = ((h % 360) + 360) % 360 / 360

  const toLinear = (c: number) => {
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  }

  if (s === 0) {
    return 0.2126 * toLinear(l) + 0.7152 * toLinear(l) + 0.0722 * toLinear(l)
  }

  const hue2rgb = (p: number, q: number, t: number) => {
    if (t < 0) t += 1
    if (t > 1) t -= 1
    if (t < 1/6) return p + (q - p) * 6 * t
    if (t < 1/2) return q
    if (t < 2/3) return p + (q - p) * (2/3 - t) * 6
    return p
  }

  const q = l < 0.5 ? l * (1 + s) : l + s - l * s
  const p = 2 * l - q
  const r = hue2rgb(p, q, h + 1/3)
  const g = hue2rgb(p, q, h)
  const b = hue2rgb(p, q, h - 1/3)

  return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b)
}

/** Update avatar foreground custom properties dynamically for optimal WCAG contrast. */
export function updateAvatarForegrounds() {
  const root = document.documentElement
  const style = getComputedStyle(root)

  // Defer calculation if CSS variables are not yet loaded/resolved (e.g. during early startup)
  const testVar = style.getPropertyValue('--avatar-1').trim()
  if (!testVar) {
    setTimeout(updateAvatarForegrounds, 50)
    return
  }

  const bgVal = style.getPropertyValue('--background').trim()
  const fgVal = style.getPropertyValue('--foreground').trim()
  const avatarFgVal = style.getPropertyValue('--avatar-fg').trim()

  const bgHsl = parseHsl(bgVal)
  const fgHsl = parseHsl(fgVal)
  const avatarFgHsl = parseHsl(avatarFgVal)

  let lightColor = '#ffffff'
  let darkColor = '#000000'

  // 1. Establish defaults based on page backgrounds/foregrounds
  if (bgHsl && fgHsl) {
    const bgL = getLuminance(bgHsl.h, bgHsl.s, bgHsl.l)
    const fgL = getLuminance(fgHsl.h, fgHsl.s, fgHsl.l)

    if (bgL > fgL) {
      if (bgL > 0.7) lightColor = `hsl(${bgVal})`
      if (fgL < 0.25) darkColor = `hsl(${fgVal})`
    }
    if (bgL <= fgL) {
      if (fgL > 0.7) lightColor = `hsl(${fgVal})`
      if (bgL < 0.25) darkColor = `hsl(${bgVal})`
    }
  }

  // 2. Refine with theme's custom --avatar-fg if it has high contrast
  if (avatarFgHsl) {
    const avatarFgL = getLuminance(avatarFgHsl.h, avatarFgHsl.s, avatarFgHsl.l)
    if (avatarFgL < 0.25) {
      darkColor = `hsl(${avatarFgVal})`
    }
    if (avatarFgL > 0.7) {
      lightColor = `hsl(${avatarFgVal})`
    }
  }

  // 3. Update the document element with contrast-based colors for each avatar
  for (let i = 1; i <= 14; i++) {
    const varName = `--avatar-${i}`
    const rawVal = style.getPropertyValue(varName).trim()
    if (!rawVal) continue

    const hsl = parseHsl(rawVal)
    if (hsl) {
      const luminance = getLuminance(hsl.h, hsl.s, hsl.l)
      const useDark = luminance > 0.179
      const fgColor = useDark ? darkColor : lightColor
      root.style.setProperty(`${varName}-fg`, fgColor)
    }
  }
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

  // Run dynamic avatar contrast update
  updateAvatarForegrounds()
  requestAnimationFrame(updateAvatarForegrounds)
  setTimeout(updateAvatarForegrounds, 50)
  setTimeout(updateAvatarForegrounds, 200)
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
