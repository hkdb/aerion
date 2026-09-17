// Chip fill for an event's calendar color. A flat 25% alpha collapses hue
// over dark backgrounds (dark orange reads as brown — #406), so the dark
// theme uses a stronger color share. The percentages are the visual-tuning
// knobs; the full-opacity left border next to the fill always shows the
// true color.
export function eventChipFill(color: string, dark: boolean): string {
  const pct = dark ? 45 : 25
  return `color-mix(in srgb, ${color} ${pct}%, transparent)`
}
