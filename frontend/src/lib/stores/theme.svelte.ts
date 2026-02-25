// lib/stores/theme.svelte.ts
import { loadSettings, getThemeMode, type ThemeMode } from './settings.svelte'
// @ts-ignore - wailsjs path
import { GetSystemTheme } from '../../../wailsjs/go/app/App'
// @ts-ignore - wailsjs runtime
import { EventsOn } from '../../../wailsjs/runtime/runtime'

let theme = $state<ThemeMode>('light')
let portalThemeAvailable = $state(false)
let portalTheme = $state<'light' | 'dark'>('light')

export function getTheme(): ThemeMode {
    return theme
}

export function isPortalThemeAvailable(): boolean {
    return portalThemeAvailable
}

export function getPortalTheme(): 'light' | 'dark' {
    return portalTheme
}

/**
 * Initializes theme management. Should be called once on app startup.
 */
export async function initTheme() {
    // Ensure settings are loaded first so we have the initial theme mode
    const storedThemeMode = await loadSettings()

    // Try to get system theme from backend (XDG Settings Portal on Linux)
    try {
        const sysTheme = await GetSystemTheme()
        if (sysTheme === 'light' || sysTheme === 'dark') {
            portalThemeAvailable = true
            portalTheme = sysTheme
        }
    } catch {
        // Portal not available, will use matchMedia fallback
    }

    // Initial application of theme
    applyThemeFromMode(getThemeMode())

    // Listen for system theme changes from backend (XDG Settings Portal)
    EventsOn('theme:system-preference', (newTheme: string) => {
        if (newTheme === 'light' || newTheme === 'dark') {
            portalThemeAvailable = true
            portalTheme = newTheme
            if (getThemeMode() === 'system') {
                applyTheme(portalTheme)
            }
        }
    })

    // Listen for system theme changes via matchMedia (fallback when portal unavailable)
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    mediaQuery.addEventListener('change', (e) => {
        if (getThemeMode() === 'system' && !portalThemeAvailable) {
            applyTheme(e.matches ? 'dark' : 'light')
        }
    })

    // React to theme mode changes from settings store
    $effect(() => {
        const mode = getThemeMode()
        applyThemeFromMode(mode)
    })
}

function applyTheme(themeName: ThemeMode) {
    theme = themeName
    document.documentElement.setAttribute('data-theme', themeName)

    // Legacy: Also set .dark class for backwards compat
    if (themeName.startsWith('dark')) {
        document.documentElement.classList.add('dark')
    } else {
        document.documentElement.classList.remove('dark')
    }
}

function applyThemeFromMode(mode: ThemeMode) {
    if (mode === 'system') {
        // Use portal-based theme if available, otherwise fall back to matchMedia
        if (portalThemeAvailable) {
            applyTheme(portalTheme)
        } else {
            const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
            applyTheme(mediaQuery.matches ? 'dark' : 'light')
        }
    } else {
        applyTheme(mode)
    }
}
