import { EventsOn } from '../../wailsjs/runtime/runtime'
import { refreshThemeClassification } from '$lib/stores/theme.svelte'

const customCssElementId = 'aerion-custom-css'

/** Apply the user stylesheet through one reusable style element. */
export function applyCustomCSS(css: string): void {
  let style = document.getElementById(customCssElementId) as HTMLStyleElement | null
  if (!style) {
    style = document.createElement('style')
    style.id = customCssElementId
    document.head.appendChild(style)
  }

  style.textContent = css
  refreshThemeClassification()
}

/** Load the current user stylesheet and subscribe to live backend updates. */
export async function initCustomCSS(getCustomCSS: () => Promise<string>): Promise<() => void> {
  let unsubscribe = () => {}
  try {
    unsubscribe = EventsOn('theme:custom-css-changed', (css: string) => {
      applyCustomCSS(css)
    })
  } catch (err) {
    console.warn('Failed to subscribe to custom CSS changes:', err)
  }

  try {
    applyCustomCSS(await getCustomCSS())
  } catch (err) {
    console.warn('Failed to load custom CSS:', err)
  }

  return unsubscribe
}
