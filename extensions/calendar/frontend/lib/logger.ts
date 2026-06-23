// Calendar extension's frontend logger. Routes through
// `Calendar_LogFrontend(level, message)` so messages land in the host's
// zerolog stream with `extension=calendar` automatically stamped — no
// reliance on the unprefixed host-level `LogFrontend` Wails method.
//
// Fire-and-forget: callers don't need to await; errors are silently
// swallowed so a logging failure can't cascade into the caller's path.
//
// Usage:
//   import { logger } from '$extensions/calendar/frontend/lib/logger'
//   logger.warn(`failed to read localStorage: ${err}`)

// @ts-ignore - Wails generated bindings
import { Calendar_LogFrontend } from '$wailsjs/go/app/App.js'

function emit(level: string, message: string): void {
  Calendar_LogFrontend(level, message).catch(() => {})
}

export const logger = {
  debug: (message: string) => emit('debug', message),
  info: (message: string) => emit('info', message),
  warn: (message: string) => emit('warn', message),
  error: (message: string) => emit('error', message),
}
