import './app.css'
import { initI18n } from './lib/i18n'
import App from './App.svelte'
import { mount } from 'svelte'
// @ts-ignore - wailsjs path
import { IsReady } from '../wailsjs/go/app/App'
// @ts-ignore - wailsjs path
import { EventsOn, WindowShow } from '../wailsjs/runtime/runtime'

// Linear bootstrap. Each step waits for the previous to complete; no
// concurrent retries woven between them. The inline splash markup in
// index.html is visible from the moment the WebView paints, so the user
// sees feedback for the entire bootstrap window (runtime inject → window
// show → i18n load → backend ready → app mount).
async function bootstrap(): Promise<void> {
  await waitForRuntime()
  WindowShow()
  await initI18n()
  await waitForBackendReady()
  mount(App, { target: document.getElementById('app')! })
}

// waitForRuntime resolves when Wails has injected `window.runtime` into the
// page. In dev mode (Vite-served bundle), injection happens AFTER the bundle
// script begins executing, so a naive `window.runtime.X()` call at module
// top throws. We rAF-poll for ~2 seconds; if the runtime never appears,
// resolve anyway and let downstream calls fail loudly with their own errors
// rather than wedging here forever.
function waitForRuntime(): Promise<void> {
  return new Promise<void>((resolve) => {
    const startedAt = performance.now()
    function check() {
      const w = window as unknown as { runtime?: { WindowShow?: unknown } }
      if (w.runtime?.WindowShow) {
        resolve()
        return
      }
      if (performance.now() - startedAt > 2000) {
        console.warn('[boot] Wails runtime never injected after 2s; proceeding anyway.')
        resolve()
        return
      }
      requestAnimationFrame(check)
    }
    check()
  })
}

// waitForBackendReady resolves when the Go-side Startup hook has reached the
// point where the frontend can safely mount: stores constructed, migrations
// applied, extensions registered, background sync started. (The D-Bus
// desktop-integration inits that run later in Startup can hang for many
// seconds on systems where xdg-desktop-portal isn't running — we
// deliberately do NOT wait for those.)
//
// App.svelte's onMount fires Wails method calls that touch stores
// (settingsStore, accountStore, etc.); if those calls land before stores
// exist, the methods hit nil pointers and crash — on Linux/webkit2gtk the
// crash manifests as the "non-Go code set up signal handler without
// SA_ONSTACK flag" runtime panic.
//
// Event-driven: Go emits "app:ready" once stores are ready. We register a
// single EventsOn listener and call IsReady() once as a safety net for the
// race where Go emits before this code runs (Startup finishes faster than
// the bundle parses). No polling — the Wails IPC bridge saturates under
// repeated calls on Linux/webkit2gtk (especially Flatpak).
//
// Precondition: waitForRuntime() has resolved, so window.runtime is present
// and EventsOn + IsReady are safe to call directly.
function waitForBackendReady(): Promise<void> {
  return new Promise<void>((resolve) => {
    let done = false
    const fire = () => {
      if (done) return
      done = true
      resolve()
    }

    EventsOn('app:ready', fire)

    // One-shot fallback for the race where Go already emitted before we
    // registered. Single IPC call; .catch swallows any binding error so
    // the event path still has a chance.
    IsReady()
      .then((ready: boolean) => {
        if (ready) fire()
      })
      .catch(() => {
        /* event path will resolve us */
      })
  })
}

bootstrap()
