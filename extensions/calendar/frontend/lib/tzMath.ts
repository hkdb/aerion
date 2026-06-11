// Tiny shim around `date-fns-tz` so consumers don't import the lib from
// every view file. Single point of swap if we ever change libraries.
//
// USAGE PATTERN: pass a real UTC Date through toTzDate() before calling
// native getters/setters that should reflect "wall-clock time as seen in
// the user's chosen display timezone". When done mutating, fromTzDate()
// converts back to a real UTC Date.
//
// Example — startOfDay in tz X:
//   const z = toTzDate(d)
//   z.setHours(0, 0, 0, 0)
//   const result = fromTzDate(z)  // real UTC instant of midnight-in-X

import { toZonedTime, fromZonedTime } from 'date-fns-tz'
import { calendarSettings } from '$extensions/calendar/frontend/stores/calendarSettings.svelte'

/** Return a Date whose `.getFullYear()/getMonth()/getDate()/getHours()/...`
 *  calls produce the wall-clock values for `date` as observed in the user's
 *  chosen display timezone. The numeric epoch of the returned Date is
 *  SHIFTED — never pass it to APIs that expect real UTC seconds. Use
 *  fromTzDate() to convert back. */
export function toTzDate(date: Date): Date {
  return toZonedTime(date, calendarSettings.effectiveTimezone)
}

/** Inverse of toTzDate — converts a shifted Date back to its real UTC
 *  instant. Use this whenever you've manipulated a tz-Date with native
 *  setters and need the UTC equivalent (e.g., for backend queries that
 *  take Unix seconds, or for fields stored as real UTC). */
export function fromTzDate(date: Date): Date {
  return fromZonedTime(date, calendarSettings.effectiveTimezone)
}
