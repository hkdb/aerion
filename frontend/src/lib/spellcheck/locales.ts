// Maps an app locale (svelte-i18n code, e.g. "en", "de", "zh-TW") to a hunspell
// dictionary key, or null when the language isn't spellcheckable. Only
// Latin-script locales Aerion ships dictionaries for are mapped; the three
// zh-* locales and vi return null (CJK has no Latin-style per-word spelling;
// vi has no bundled dictionary yet).
const APP_TO_DICT: Record<string, string> = {
  en: 'en',
  cs: 'cs',
  de: 'de',
  fr: 'fr',
  it: 'it',
  nb: 'nb',
  nl: 'nl',
}

// Dictionaries bundled as static assets under /spellcheck/<key>.{aff,dic}.
export const SPELLCHECK_DICTS = ['en', 'cs', 'de', 'fr', 'it', 'nb', 'nl'] as const

export function appLocaleToDict(locale: string | null | undefined): string | null {
  if (!locale) return null
  const base = locale.toLowerCase().split('-')[0]
  return APP_TO_DICT[base] ?? null
}
