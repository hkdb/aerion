import type { Locale } from 'date-fns'

// Lazy-loaded date-fns locale cache
const localeCache = new Map<string, Locale>()

/**
 * Get the date-fns locale for a given i18n locale code.
 * Returns undefined if the locale hasn't been loaded yet (use loadDateFnsLocale first).
 */
export function getDateFnsLocale(code: string): Locale | undefined {
  return localeCache.get(code)
}

/**
 * Load and cache the date-fns locale for a given i18n locale code.
 * English doesn't need an explicit locale (date-fns defaults to en-US).
 */
export async function loadDateFnsLocale(code: string): Promise<Locale | undefined> {
  if (code === 'en') return undefined
  if (localeCache.has(code)) return localeCache.get(code)

  let dateFnsLocale: Locale | undefined

  switch (code) {
    case 'zh-TW': {
      const mod = await import('date-fns/locale/zh-TW')
      dateFnsLocale = mod.zhTW
      break
    }
    case 'zh-HK': {
      const mod = await import('date-fns/locale/zh-HK')
      dateFnsLocale = mod.zhHK
      break
    }
    case 'zh-CN': {
      const mod = await import('date-fns/locale/zh-CN')
      dateFnsLocale = mod.zhCN
      break
    }
    case 'fr': {
      const mod = await import('date-fns/locale/fr')
      dateFnsLocale = mod.fr
      break
    }
  }

  if (dateFnsLocale) {
    localeCache.set(code, dateFnsLocale)
  }

  return dateFnsLocale
}
