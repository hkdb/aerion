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
}

// Dictionaries bundled as static assets under /spellcheck/<key>.{aff,dic}.
export const SPELLCHECK_DICTS = ['en', 'en-gb', 'cs', 'de', 'fr', 'it', 'nb', 'nl'] as const

// Display-name overrides for dictionaries that don't map 1:1 to an app
// locale (the settings list otherwise names dicts via supportedLocales).
// 'en' is the US SCOWL wordlist, so disambiguate it now that en-gb exists.
export const DICT_NAMES: Record<string, string> = {
  en: 'English (US)',
  'en-gb': 'English (UK)',
  nl: 'Nederlands',
}

export function appLocaleToDict(locale: string | null | undefined): string | null {
  if (!locale) return null
  const lower = locale.toLowerCase()
  // Already a dictionary key (the settings toggles store these) — return it
  // as-is. Base-splitting first would collapse 'en-gb' to the 'en' (US)
  // dictionary and spellcheck the wrong variant.
  if ((SPELLCHECK_DICTS as readonly string[]).includes(lower)) return lower
  const base = lower.split('-')[0]
  return APP_TO_DICT[base] ?? null
}
