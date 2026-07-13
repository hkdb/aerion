// TipTap/ProseMirror spellcheck extension: underlines misspelled words in the
// compose body. Words + positions are collected from the doc, unique words are
// checked by the worker (off-thread, debounced), and misspelled ranges are
// decorated. Positions are recomputed against the current doc at dispatch time,
// so an edit landing during the async check can never produce a stale range.
import { Extension } from '@tiptap/core'
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import type { EditorView } from '@tiptap/pm/view'
import type { Node as PMNode } from '@tiptap/pm/model'
import { spellcheck } from './client'
import { spellMenu } from './menu.svelte'
import { addCustomWord } from './settings'

const spellcheckKey = new PluginKey<DecorationSet>('spellcheck')

// "Ignore" is per-composer and NOT persisted: it lives in a set keyed by the
// editor view, so a new composer (new view) starts with a clean slate. Unlike
// "Add to dictionary" it never touches the worker dictionary or the backend.
const ignoredWords = new WeakMap<EditorView, Set<string>>()
function ignoredFor(view: EditorView): Set<string> {
  let set = ignoredWords.get(view)
  if (!set) {
    set = new Set<string>()
    ignoredWords.set(view, set)
  }
  return set
}

// A "word" is a run of Latin-script letters/marks with internal apostrophes.
// Digits and short tokens are skipped (identifiers, numbers, single letters).
// Restricted to Latin script on purpose: every shipped dictionary is Latin
// (SPELLCHECK_DICTS in locales.ts = en/cs/de/fr/it/nb), so non-Latin runs
// (CJK, Cyrillic, Greek, …) can never be validated — leave them unflagged
// instead of underlining everything. Revisit if a non-Latin dictionary ships.
const WORD_RE = /[\p{Script=Latin}][\p{Script=Latin}\p{M}'’]*/gu

type WordHit = { word: string; from: number; to: number }

function collectWords(doc: PMNode): WordHit[] {
  const hits: WordHit[] = []
  doc.descendants((node, pos) => {
    if (!node.isText || !node.text) return
    const text = node.text
    WORD_RE.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = WORD_RE.exec(text)) !== null) {
      const word = m[0]
      if (word.length < 2 || /\d/.test(word)) continue
      hits.push({ word, from: pos + m.index, to: pos + m.index + word.length })
    }
  })
  return hits
}

function decorate(view: EditorView, bad: Set<string>): void {
  const doc = view.state.doc
  const ignored = ignoredFor(view)
  const decos = collectWords(doc)
    .filter((h) => bad.has(h.word) && !ignored.has(h.word))
    .map((h) => Decoration.inline(h.from, h.to, { class: 'spellcheck-error' }))
  view.dispatch(view.state.tr.setMeta(spellcheckKey, DecorationSet.create(doc, decos)))
}

function runCheck(view: EditorView): void {
  if (!spellcheck.isActive) return
  const words = [...new Set(collectWords(view.state.doc).map((h) => h.word))]
  spellcheck.check(words).then((bad) => decorate(view, bad))
}

type Range = { from: number; to: number }

// The misspelled-word range under a document position, or null when the
// position isn't inside a spellcheck-error decoration.
function misspellingAt(view: EditorView, pos: number): Range | null {
  const decos = spellcheckKey.getState(view.state)
  const found = decos?.find(pos, pos) ?? []
  if (found.length === 0) return null
  return { from: found[0].from, to: found[0].to }
}

// The misspelled-word range nearest the cursor (F7), or null when there are none.
function nearestMisspelling(view: EditorView): Range | null {
  const decos = spellcheckKey.getState(view.state)
  const all = decos?.find(0, view.state.doc.content.size) ?? []
  if (all.length === 0) return null
  const pos = view.state.selection.head
  let best: Range | null = null
  let bestDist = Infinity
  for (const d of all) {
    const dist = pos < d.from ? d.from - pos : pos > d.to ? pos - d.to : 0
    if (dist < bestDist) {
      bestDist = dist
      best = { from: d.from, to: d.to }
    }
  }
  return best
}

function restoreCursor(view: EditorView, pos: number): void {
  const clamped = Math.min(pos, view.state.doc.content.size)
  view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, clamped)))
  view.focus()
}

// Opens the suggestion menu for a misspelled range. The cursor is captured at
// invocation and restored afterward (mapped through any replacement), so F7
// fixes the nearest typo and returns you to where you were typing.
// keyboard=true enables arrow/Enter navigation in the menu.
function openSpellMenu(view: EditorView, range: Range, anchor: { x: number; y: number }, keyboard: boolean): void {
  const returnPos = view.state.selection.head
  const word = view.state.doc.textBetween(range.from, range.to)
  spellcheck.suggest(word).then((suggestions) => {
    spellMenu.open({
      keyboard,
      x: anchor.x,
      y: anchor.y,
      word,
      suggestions,
      onReplace: (replacement) => {
        const tr = view.state.tr.insertText(replacement, range.from, range.to)
        tr.setSelection(TextSelection.create(tr.doc, tr.mapping.map(returnPos)))
        view.dispatch(tr)
        view.focus()
        runCheck(view)
      },
      onAdd: () => {
        addCustomWord(word)
        restoreCursor(view, returnPos)
        runCheck(view)
      },
      onIgnore: () => {
        ignoredFor(view).add(word)
        restoreCursor(view, returnPos)
        runCheck(view)
      },
    })
  })
}

const DEBOUNCE_MS = 400

export const Spellcheck = Extension.create({
  name: 'spellcheck',
  addKeyboardShortcuts() {
    return {
      // F7 (Word-style): open the suggestion menu for the misspelling nearest
      // the cursor, keyboard-navigable, returning the caret afterward.
      F7: () => {
        const view = this.editor.view
        const range = nearestMisspelling(view)
        if (!range) return true
        const coords = view.coordsAtPos(range.from)
        openSpellMenu(view, range, { x: coords.left, y: coords.bottom }, true)
        return true
      },
    }
  },
  addProseMirrorPlugins() {
    return [
      new Plugin<DecorationSet>({
        key: spellcheckKey,
        state: {
          init: () => DecorationSet.empty,
          apply(tr, old) {
            const meta = tr.getMeta(spellcheckKey) as DecorationSet | undefined
            if (meta) return meta
            return old.map(tr.mapping, tr.doc)
          },
        },
        props: {
          decorations(state) {
            return spellcheckKey.getState(state)
          },
          handleDOMEvents: {
            // Right-click on a misspelled word → our suggestion menu. Anywhere
            // else, return false so the native cut/copy/paste menu shows.
            contextmenu(view, event) {
              const at = view.posAtCoords({ left: event.clientX, top: event.clientY })
              if (!at) return false
              const range = misspellingAt(view, at.pos)
              if (!range) return false
              event.preventDefault()
              openSpellMenu(view, range, { x: event.clientX, y: event.clientY }, false)
              return true
            },
          },
        },
        view(editorView) {
          let timer: ReturnType<typeof setTimeout> | null = null

          const schedule = () => {
            if (timer) clearTimeout(timer)
            timer = setTimeout(() => runCheck(editorView), DEBOUNCE_MS)
          }

          // Re-check when a dictionary finishes loading (the first pass while
          // loading returns nothing), and once on mount for prefilled bodies.
          const offReady = spellcheck.onReady(schedule)
          schedule()

          return {
            update(view, prevState) {
              if (prevState.doc.eq(view.state.doc)) return
              schedule()
            },
            destroy() {
              if (timer) clearTimeout(timer)
              offReady()
            },
          }
        },
      }),
    ]
  },
})
