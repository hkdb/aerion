// Contacts extension i18n registration.
//
// Auto-discovered by core's initI18n() via import.meta.glob — DO NOT add a
// manual import to frontend/src/lib/i18n/index.ts for new locales. Just add
// a register() line below and a matching locales/<code>.json file.
//
// svelte-i18n merges loaders per-locale: core ships its keys, this file ships
// the contacts.* namespace. No key collisions as long as namespaces stay
// distinct. See docs/EXTENSIONS.md § Extension i18n for the contract.

import { register } from 'svelte-i18n'

export function registerExtensionI18n(): void {
  register('en', () => import('./locales/en.json'))
  register('cs', () => import('./locales/cs.json'))
  register('de', () => import('./locales/de.json'))
  register('it', () => import('./locales/it.json'))
  register('vi', () => import('./locales/vi.json'))
  register('zh-CN', () => import('./locales/zh-CN.json'))
  register('zh-HK', () => import('./locales/zh-HK.json'))
  register('zh-TW', () => import('./locales/zh-TW.json'))
}
