/**
 * Keyboard and pane focus management store
 *
 * Tracks which pane is focused and manages focus state for keyboard navigation.
 */

export type FocusablePane = 'sidebar' | 'messageList' | 'viewer'

// Pane cycle order for navigation
const PANE_ORDER: FocusablePane[] = ['sidebar', 'messageList', 'viewer']

// Reactive state using Svelte 5 runes
let focusedPane = $state<FocusablePane>('messageList')
let flashingPane = $state<FocusablePane | null>(null)
let flashTimeoutId: ReturnType<typeof setTimeout> | null = null

/**
 * Get the currently focused pane
 */
export function getFocusedPane(): FocusablePane {
  return focusedPane
}

/**
 * Check if a specific pane is focused
 */
export function isPaneFocused(pane: FocusablePane): boolean {
  return focusedPane === pane
}

/**
 * Check if a specific pane is currently flashing
 */
export function isPaneFlashing(pane: FocusablePane): boolean {
  return flashingPane === pane
}

/**
 * Get the currently flashing pane (for reactive binding)
 */
export function getFlashingPane(): FocusablePane | null {
  return flashingPane
}

/**
 * Trigger flash animation on a pane
 */
function triggerFlash(pane: FocusablePane) {
  // Clear any existing flash timeout
  if (flashTimeoutId) {
    clearTimeout(flashTimeoutId)
  }

  flashingPane = pane

  // Clear flash after animation duration
  flashTimeoutId = setTimeout(() => {
    flashingPane = null
    flashTimeoutId = null
  }, 300) // Match CSS animation duration
}

/**
 * Set the focused pane and trigger flash animation
 */
export function setFocusedPane(pane: FocusablePane) {
  if (focusedPane !== pane) {
    focusedPane = pane
    triggerFlash(pane)
  }
}

/**
 * Focus the previous pane in the cycle: viewer -> messageList -> sidebar -> viewer
 */
export function focusPreviousPane() {
  const currentIndex = PANE_ORDER.indexOf(focusedPane)
  const previousIndex = currentIndex === 0 ? PANE_ORDER.length - 1 : currentIndex - 1
  setFocusedPane(PANE_ORDER[previousIndex])
}

/**
 * Focus the next pane in the cycle: sidebar -> messageList -> viewer -> sidebar
 */
export function focusNextPane() {
  const currentIndex = PANE_ORDER.indexOf(focusedPane)
  const nextIndex = (currentIndex + 1) % PANE_ORDER.length
  setFocusedPane(PANE_ORDER[nextIndex])
}

/**
 * Check if the event target is an input field
 * Used to disable single-key shortcuts when typing
 */
export function isInputElement(target: EventTarget | null): boolean {
  if (!target || !(target instanceof HTMLElement)) {
    return false
  }

  const tagName = target.tagName.toUpperCase()

  // Check for standard input elements
  if (tagName === 'INPUT' || tagName === 'TEXTAREA') {
    return true
  }

  // Check for contenteditable elements (like TipTap editor)
  if (target.isContentEditable) {
    return true
  }

  // Check for elements with role="textbox"
  if (target.getAttribute('role') === 'textbox') {
    return true
  }

  return false
}

/**
 * Create a reactive object for use in components
 * This allows components to reactively respond to focus changes
 */
export function createKeyboardState() {
  return {
    get focusedPane() { return focusedPane },
    get flashingPane() { return flashingPane },
    isPaneFocused,
    isPaneFlashing,
    setFocusedPane,
    focusPreviousPane,
    focusNextPane,
    isInputElement,
  }
}

/**
 * Pane navigation registry — lets the global keyboard handler dispatch
 * Alt+J/K (and similar pane-targeted shortcuts) to whichever component
 * currently owns a given slot. Mail's panes don't use this (they're wired
 * directly via concrete refs in App.svelte); extension kit components
 * DO register on mount so Alt+J/K navigates the kit's SourceSidebar /
 * ListPane uniformly with how Alt+J/K navigates mail's folder list.
 */
export interface PaneNavTarget {
  navigateNext?: () => void
  navigatePrev?: () => void
  activate?: () => void
  /** Move keyboard focus to this pane's search input (Ctrl+S). */
  focusSearch?: () => void
}

const paneNavTargets: Partial<Record<FocusablePane, PaneNavTarget>> = {}

export function registerPaneNav(slot: FocusablePane, target: PaneNavTarget): () => void {
  paneNavTargets[slot] = target
  return () => {
    if (paneNavTargets[slot] === target) {
      delete paneNavTargets[slot]
    }
  }
}

export function getPaneNav(slot: FocusablePane): PaneNavTarget | undefined {
  return paneNavTargets[slot]
}

// Composer open state — used to suppress viewer shortcuts (Delete/Backspace)
// during the composer's mount→focus race, where a keystroke can fire before
// TipTap claims focus and would otherwise trash the focused message.
let composerOpen = $state(false)

export function setComposerOpen(open: boolean): void {
  composerOpen = open
}

export function isComposerOpen(): boolean {
  return composerOpen
}
