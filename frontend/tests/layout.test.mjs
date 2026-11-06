import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { test, after } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileModule } from 'svelte/compiler'
import ts from 'typescript'

// Compile the actual rune-based store using existing build dependencies.
// Keep the temporary module inside frontend so Svelte imports resolve normally.
const directory = await mkdtemp(fileURLToPath(new URL('./.layout-', import.meta.url)))
after(() => rm(directory, { recursive: true, force: true }))
const source = await readFile(new URL('../src/lib/stores/layout.svelte.ts', import.meta.url), 'utf8')
const javascript = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext },
}).outputText
const compiled = compileModule(javascript, { filename: 'layout.svelte.js', generate: 'client' })
const modulePath = `${directory}/layout.mjs`
await writeFile(modulePath, compiled.js.code)
let instance = 0

// Evaluate the store's real media-query strings, rather than duplicating its
// breakpoint constants. Fail on unsupported syntax instead of silently matching.
function matches(query, width, height) {
  return query.split(',').some(alternative => alternative.trim().split(/\s+and\s+/).every(condition => {
    const widthMatch = /^\(max-width: (\d+)px\)$/.exec(condition)
    if (widthMatch) return width <= Number(widthMatch[1])
    const ratioMatch = /^\(max-aspect-ratio: (\d+)\/(\d+)\)$/.exec(condition)
    assert.ok(ratioMatch, `Unsupported media condition: ${condition}`)
    return width * Number(ratioMatch[2]) <= height * Number(ratioMatch[1])
  }))
}

async function createLayout(width, height, reverseEvents = false) {
  const queries = []
  const previousWindow = globalThis.window
  globalThis.window = {
    matchMedia(media) {
      const listeners = []
      const query = {
        media,
        matches: matches(media, width, height),
        addEventListener(type, listener) {
          assert.equal(type, 'change')
          listeners.push(listener)
        },
        listeners,
      }
      queries.push(query)
      return query
    },
  }
  let store
  try {
    store = await import(`${pathToFileURL(modulePath).href}?instance=${instance++}`)
    store.initLayout()
  } finally {
    if (previousWindow === undefined) delete globalThis.window
    else globalThis.window = previousWindow
  }
  return {
    ...store,
    resize(nextWidth, nextHeight) {
      const changed = []
      // Browsers update query results before delivering change notifications.
      for (const query of queries) {
        const next = matches(query.media, nextWidth, nextHeight)
        if (next !== query.matches) changed.push(query)
        query.matches = next
      }
      if (reverseEvents) changed.reverse()
      for (const query of changed) {
        for (const listener of query.listeners) listener()
      }
    },
  }
}

for (const [width, height, expected] of [
  [1920, 1080, 'full'],
  [1280, 1920, 'medium'],
  [1080, 1920, 'narrow'],
  [1080, 1600, 'medium'],
  [700, 1000, 'narrow'],
  [767, 700, 'narrow'],
  [768, 700, 'medium'],
  [1024, 700, 'medium'],
  [1025, 700, 'full'],
  [1081, 1920, 'medium'],
  [1200, 2400, 'narrow'],
  [1201, 2400, 'medium'],
  [1400, 2400, 'medium'],
  [1401, 2400, 'full'],
  [1200, 1200, 'medium'],
  [1200, 1199, 'full'],
]) {
  test(`${width}×${height} selects ${expected}`, async () => {
    const layout = await createLayout(width, height)
    assert.equal(layout.getLayoutMode(), expected)
    assert.equal(layout.getResponsiveView(), 'default')
    assert.equal(layout.isResponsive(), expected !== 'full')
  })
}

for (const reverseEvents of [false, true]) {
  test(`resize transitions preserve valid navigation (reverse events: ${reverseEvents})`, async () => {
    const layout = await createLayout(700, 1000, reverseEvents)
    layout.showSidebar()
    assert.equal(layout.getResponsiveView(), 'sidebar')
    layout.resize(1080, 1600)
    assert.equal(layout.getLayoutMode(), 'medium')
    assert.equal(layout.getResponsiveView(), 'default')
    layout.showSidebar()
    assert.equal(layout.getResponsiveView(), 'default')

    layout.resize(700, 1000)
    assert.equal(layout.getResponsiveView(), 'default')
    layout.showViewer()
    layout.resize(1080, 1600)
    assert.equal(layout.getLayoutMode(), 'medium')
    assert.equal(layout.getResponsiveView(), 'viewer')
    layout.resize(700, 1000)
    assert.equal(layout.getResponsiveView(), 'viewer')
    layout.hideViewer()
    assert.equal(layout.getResponsiveView(), 'default')
    layout.showSidebar()
    layout.hideSidebar()
    assert.equal(layout.getResponsiveView(), 'default')

    layout.resize(1080, 1600)
    layout.showViewer()
    layout.resize(1920, 1080)
    assert.equal(layout.getLayoutMode(), 'full')
    assert.equal(layout.getResponsiveView(), 'default')
    layout.showViewer()
    layout.showSidebar()
    assert.equal(layout.getResponsiveView(), 'default')
    layout.resize(700, 1000)
    assert.equal(layout.getResponsiveView(), 'default')
    layout.showSidebar()
    layout.resize(1920, 1080)
    assert.equal(layout.getLayoutMode(), 'full')
    assert.equal(layout.getResponsiveView(), 'default')
    layout.resize(1080, 1600)
    assert.equal(layout.getResponsiveView(), 'default')
  })
}
