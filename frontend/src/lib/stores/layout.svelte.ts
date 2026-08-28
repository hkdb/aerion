// Responsive layout store for tiled/narrow windows
// Three modes: full (3 columns), medium (2 columns), narrow (1 column)
// Modes are selected by viewport width and aspect ratio.
// Tall viewports switch to fewer columns at wider width thresholds.

export type LayoutMode = 'full' | 'medium' | 'narrow'
export type ResponsiveView = 'default' | 'viewer' | 'sidebar'

let layoutMode = $state<LayoutMode>('full')
let responsiveView = $state<ResponsiveView>('default')

let narrowMql: MediaQueryList | null = null
let mediumMql: MediaQueryList | null = null

function updateMode() {
  if (!narrowMql || !mediumMql) return

  if (narrowMql.matches) {
    layoutMode = 'narrow'
  } else if (mediumMql.matches) {
    layoutMode = 'medium'
  } else {
    layoutMode = 'full'
  }

  // Sidebar overlays only exist in narrow mode. Keep an open viewer when
  // switching between responsive modes, but reset navigation in full mode.
  if (layoutMode === 'full' || (layoutMode !== 'narrow' && responsiveView === 'sidebar')) {
    responsiveView = 'default'
  }
}

export function initLayout() {
  // Upper width bounds let sufficiently wide viewports retain more columns.
  // A 1080×1920 viewport uses one column; 1080×1600 and 1280×1920 use two.
  narrowMql = window.matchMedia('(max-width: 767px), (max-aspect-ratio: 9/16) and (max-width: 1200px)')
  mediumMql = window.matchMedia('(max-width: 1024px), (max-aspect-ratio: 1/1) and (max-width: 1400px)')

  updateMode()

  narrowMql.addEventListener('change', updateMode)
  mediumMql.addEventListener('change', updateMode)
}

export function getLayoutMode(): LayoutMode {
  return layoutMode
}

export function getResponsiveView(): ResponsiveView {
  return responsiveView
}

export function isResponsive(): boolean {
  return layoutMode !== 'full'
}

export function showViewer() {
  if (layoutMode === 'full') return
  responsiveView = 'viewer'
}

export function hideViewer() {
  if (layoutMode === 'full') return
  responsiveView = 'default'
}

export function showSidebar() {
  if (layoutMode !== 'narrow') return
  responsiveView = 'sidebar'
}

export function hideSidebar() {
  if (layoutMode !== 'narrow') return
  if (responsiveView === 'sidebar') {
    responsiveView = 'default'
  }
}
