import { register, init, waitLocale, locale, _ } from 'svelte-i18n'

// Register CORE locale files with lazy loading. Extensions register their own
// locales via Vite glob auto-discovery in initI18n() — see below.
register('en', () => import('./locales/en.json'))
register('cs', () => import('./locales/cs.json'))
register('de', () => import('./locales/de.json'))
register('fr', () => import('./locales/fr.json'))
register('it', () => import('./locales/it.json'))
register('nb', () => import('./locales/nb.json'))
register('vi', () => import('./locales/vi.json'))
register('zh-CN', () => import('./locales/zh-CN.json'))
register('zh-HK', () => import('./locales/zh-HK.json'))
register('zh-TW', () => import('./locales/zh-TW.json'))

// Vite-discovered extension i18n modules. Each enabled extension that ships
// translations exports a registerExtensionI18n() function from its
// frontend/i18n/index.ts. Adding a new extension's i18n is purely a file
// drop — no edit to this file needed. See docs/EXTENSIONS.md § Extension
// i18n for the contract.
const extensionI18nModules = import.meta.glob<{
  registerExtensionI18n: () => void
}>('../../../../extensions/*/frontend/i18n/index.ts', { eager: true })

// Supported locales for the language picker
export const supportedLocales = [
  { code: 'en', name: 'English' },
  { code: 'cs', name: 'Čeština' },
  { code: 'de', name: 'Deutsch' },
  { code: 'fr', name: 'Français' },
  { code: 'it', name: 'Italiano' },
  { code: 'nb', name: 'Norsk Bokmål' },
  { code: 'vi', name: 'Tiếng Việt' },
  { code: 'zh-CN', name: '简体中文 (中国)' },
  { code: 'zh-HK', name: '繁體中文 (香港)' },
  { code: 'zh-TW', name: '繁體中文 (台灣)' },
] as const

/**
 * Detect system locale from navigator.language and map to supported locales.
 * zh-HK → zh-HK, zh-TW → zh-TW, bare zh → zh-TW (fallback), en-US → en, etc.
 */
export function detectSystemLocale(): string {
  const nav = navigator.language || 'en'
  const lower = nav.toLowerCase()

  // Exact match first
  const exact = supportedLocales.find(l => l.code.toLowerCase() === lower)
  if (exact) return exact.code

  // Language-only match (e.g., "zh" → "zh-TW", "en-US" → "en")
  const lang = lower.split('-')[0]
  if (lang === 'zh') return 'zh-TW' // bare "zh" defaults to Traditional Chinese (Taiwan)

  const langMatch = supportedLocales.find(l => l.code.toLowerCase().split('-')[0] === lang)
  if (langMatch) return langMatch.code

  return 'en'
}

/**
 * Initialize i18n and wait for the initial locale to load.
 * Must be awaited before mounting the Svelte app, otherwise $_ throws.
 * @param savedLocale - Previously saved locale code from backend settings, or undefined for auto-detect
 */
export async function initI18n(savedLocale?: string): Promise<void> {
  const initialLocale = savedLocale || detectSystemLocale()

  // Register every discovered extension's locale loaders before init() so
  // their messages are merged into the active locale on first wait.
  for (const mod of Object.values(extensionI18nModules)) {
    mod.registerExtensionI18n?.()
  }

  init({
    fallbackLocale: 'en',
    initialLocale,
  })

  await waitLocale()
}

/**
 * Change the active locale at runtime.
 */
export function setLocale(code: string) {
  locale.set(code)
}

export { _, locale }
