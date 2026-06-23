// Type shim for `date-fns-tz` v3.x. The library ships its own .d.ts files
// at `dist/esm/index.d.ts` but its package.json exports map omits the
// `types` condition, so TypeScript with moduleResolution:"bundler" can't
// auto-resolve them through the bare specifier. Declare only the two
// functions tzMath.ts uses; if a future caller needs more, extend here.

declare module 'date-fns-tz' {
  export function toZonedTime(date: Date | number | string, timeZone: string): Date
  export function fromZonedTime(date: Date | number | string, timeZone: string): Date
}
