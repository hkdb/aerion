import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import path from 'path'

// Extensions live outside frontend/ at the repo root (../extensions/<name>/frontend/...).
// $extensions aliases the extensions dir so App.svelte and other host files
// can import extension Svelte components and stores cleanly. $wailsjs aliases
// the generated Wails bindings so deep extension files don't need ../ chains.
//
// Because extension Svelte/TS files live OUTSIDE frontend/, Rollup's default
// resolution doesn't find frontend/node_modules. The npm deps used by
// extensions are aliased explicitly below to the host's node_modules so a
// single dependency tree is shared.
//
// CONVENTION — when an extension pulls in a new npm dep, three steps:
//   1. `npm install <pkg>` (or add to frontend/package.json) — installs
//      under frontend/node_modules, which extensions can't reach by
//      default.
//   2. Add an alias entry below tagged with the owning extension's
//      manifest ID so we know which extension to move the dep to when we
//      eventually split deps per-extension (npm workspaces / per-extension
//      package.json — tracked as future architectural work).
//   3. Run `python3 build/flatpak/flathub/gen-node-sources.py` so Flatpak
//      CI picks up the new tarball (per project memory).
//
// Aliases are grouped as:
//   - SHARED — used by kit primitives or multiple extensions
//   - PER-EXTENSION — used by exactly one extension today; comment names it
const EXTENSIONS_DIR = path.resolve(__dirname, '../extensions')
const WAILSJS_DIR = path.resolve(__dirname, './wailsjs')
const NODE_MODULES_DIR = path.resolve(__dirname, './node_modules')

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      '$lib': path.resolve('./src/lib'),
      '$': path.resolve('./src'),
      '$extensions': EXTENSIONS_DIR,
      '$wailsjs': WAILSJS_DIR,
      // ── SHARED (kit primitives + most/all extensions) ───────────────────
      '@iconify/svelte': path.resolve(NODE_MODULES_DIR, '@iconify/svelte'),
      'svelte-i18n':     path.resolve(NODE_MODULES_DIR, 'svelte-i18n'),

      // ── PER-EXTENSION (move to per-ext deps when the SDK supports it) ───
      // extension: calendar — tz-aware date math (toZonedTime / fromZonedTime)
      'date-fns-tz':     path.resolve(NODE_MODULES_DIR, 'date-fns-tz'),
    },
  },
  optimizeDeps: {
    include: ['@iconify-json/mdi', '@iconify-json/lucide', '@iconify-json/heroicons', '@iconify-json/logos', '@iconify-json/simple-icons'],
  },
  build: {
    target: 'esnext',
    minify: 'esbuild',
    sourcemap: false,
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, 'index.html'),
        composer: path.resolve(__dirname, 'composer.html'),
      },
    },
  },
  server: {
    strictPort: true,
    fs: {
      // Vite blocks file reads outside its root by default. Extensions live
      // at <repo>/extensions/, one level above the frontend root, so allow it.
      allow: ['..', EXTENSIONS_DIR],
    },
  },
})
