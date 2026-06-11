// Calendar extension i18n registration.
//
// Auto-discovered by core's initI18n() via import.meta.glob — DO NOT add a
// manual import to frontend/src/lib/i18n/index.ts for new locales. Just add
// a register() line below and a matching locales/<code>.json file.
//
// Top-level key is "calendar" (see locales/en.json). No collision with the
// "contacts" namespace or core's keys.

import { register } from 'svelte-i18n'

export function registerExtensionI18n(): void {
  register('en', () => import('./locales/en.json'))
}
