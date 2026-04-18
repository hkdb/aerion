/**
 * TipTap editor configuration for the email composer
 */
import { Editor, Extension } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Underline from '@tiptap/extension-underline'
import Placeholder from '@tiptap/extension-placeholder'
import Image from '@tiptap/extension-image'
import TextStyle from '@tiptap/extension-text-style'
import Color from '@tiptap/extension-color'
import TextAlign from '@tiptap/extension-text-align'
import Table from '@tiptap/extension-table'
import TableRow from '@tiptap/extension-table-row'
import TableCell from '@tiptap/extension-table-cell'
import TableHeader from '@tiptap/extension-table-header'
import FontSize from 'tiptap-extension-font-size'
import { parseFileUris } from './composerUtils'

/**
 * Extended TextStyle to handle legacy <font> tags from signatures/pasted content
 */
export const ExtendedTextStyle = TextStyle.extend({
  parseHTML() {
    return [
      { tag: 'span' },
      { tag: 'font' },
    ]
  },
})

/**
 * Extended Color to handle legacy <font color="..."> tags
 */
export const ExtendedColor = Color.extend({
  addGlobalAttributes() {
    return [
      {
        types: this.options.types,
        attributes: {
          color: {
            default: null,
            parseHTML: (element: HTMLElement) => {
              const styleColor = element.style.color?.replace(/['"]+/g, '')
              if (styleColor) return styleColor
              if (element.tagName === 'FONT') {
                return element.getAttribute('color')
              }
              return null
            },
            renderHTML: (attributes: Record<string, string>) => {
              if (!attributes.color) {
                return {}
              }
              return {
                style: `color: ${attributes.color}`,
              }
            },
          },
        },
      },
    ]
  },
})

/**
 * Extended Image that preserves the data-original-src attribute.
 * Used for blocked remote images: the placeholder SVG is in src,
 * the original URL is in data-original-src for restoration on send.
 */
const ComposerImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      'data-original-src': {
        default: null,
        parseHTML: (element: HTMLElement) => element.getAttribute('data-original-src'),
        renderHTML: (attributes: Record<string, string>) => {
          if (!attributes['data-original-src']) return {}
          return { 'data-original-src': attributes['data-original-src'] }
        },
      },
    }
  },
})

/**
 * Extended Table extensions to preserve inline style attributes
 */
const ExtendedTable = Table.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      style: {
        default: null,
        parseHTML: (element: HTMLElement) => element.getAttribute('style'),
        renderHTML: (attributes: Record<string, string>) => {
          if (!attributes.style) return {}
          return { style: attributes.style }
        },
      },
    }
  },
})

const ExtendedTableCell = TableCell.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      style: {
        default: null,
        parseHTML: (element: HTMLElement) => element.getAttribute('style'),
        renderHTML: (attributes: Record<string, string>) => {
          if (!attributes.style) return {}
          return { style: attributes.style }
        },
      },
    }
  },
})

const ExtendedTableHeader = TableHeader.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      style: {
        default: null,
        parseHTML: (element: HTMLElement) => element.getAttribute('style'),
        renderHTML: (attributes: Record<string, string>) => {
          if (!attributes.style) return {}
          return { style: attributes.style }
        },
      },
    }
  },
})

export interface ComposerEditorHandlers {
  onUpdate?: () => void
  onPasteImage?: (file: File) => void
  onDropImage?: (file: File) => void
  onDropFile?: (file: File) => void
  onDropFilePaths?: (paths: string[]) => void
  onShiftTab?: () => void
}

/**
 * Create a configured TipTap editor for the composer
 */
export function createComposerEditor(
  element: HTMLElement,
  handlers: ComposerEditorHandlers = {}
): Editor {
  return new Editor({
    element,
    extensions: [
      StarterKit,
      Underline,
      ExtendedTextStyle,
      ExtendedColor,
      FontSize,
      TextAlign.configure({
        types: ['paragraph'],
      }),
      ExtendedTable.configure({
        resizable: false,
      }),
      TableRow,
      ExtendedTableCell,
      ExtendedTableHeader,
      Link.configure({
        openOnClick: false,
      }),
      ComposerImage.configure({
        inline: true,
        allowBase64: true,
      }),
      Placeholder.configure({
        placeholder: 'Write your message...',
      }),
      Extension.create({
        name: 'shiftTabHandler',
        addKeyboardShortcuts() {
          return {
            'Shift-Tab': () => {
              handlers.onShiftTab?.()
              return true
            },
            'Mod-Enter': () => true,
          }
        },
      }),
    ],
    content: '',
    editorProps: {
      attributes: {
        class: 'composer-editor focus:outline-none min-h-[200px] p-3',
      },
      // Handle paste events for images
      handlePaste: (view, event) => {
        // Try clipboardData.items first
        const items = event.clipboardData?.items
        if (items) {
          for (const item of items) {
            if (item.type.startsWith('image/')) {
              event.preventDefault()
              const file = item.getAsFile()
              if (file && handlers.onPasteImage) {
                handlers.onPasteImage(file)
              }
              return true
            }
          }
        }

        // Fallback: try clipboardData.files (WebKitGTK may populate this instead of items)
        const files = event.clipboardData?.files
        if (files && files.length > 0) {
          for (const file of Array.from(files)) {
            if (file.type.startsWith('image/')) {
              event.preventDefault()
              if (handlers.onPasteImage) {
                handlers.onPasteImage(file)
              }
              return true
            }
          }
        }

        return false
      },
      // Handle drop events for files (images inline, others as attachments)
      //
      // Three-tier approach for cross-platform compatibility:
      //  1. File objects via dataTransfer.files (macOS/Windows webviews)
      //  2. File URIs via getData('text/uri-list') (standard browsers)
      //  3. ProseMirror state cleanup (WebKitGTK fallback — neither #1 nor #2
      //     work because WebKitGTK provides empty files/getData and instead
      //     inserts file:/// URIs as plain text at the native GTK layer)
      handleDrop: (view, event, _slice, moved) => {
        if (moved) return false

        // Snapshot pre-existing file:/// URIs so we only clean up NEW ones
        // from the drop (preserves URIs the user intentionally typed)
        const existingUris = new Set<string>()
        view.state.doc.descendants((node) => {
          if (!node.isText || !node.text || !node.text.includes('file:///')) return
          const re = /file:\/\/\/.+/g
          let m
          while ((m = re.exec(node.text)) !== null) {
            existingUris.add(m[0].trim())
          }
        })

        // WebKitGTK fallback: after ProseMirror syncs the native text
        // insertion into state, delete only the NEW file:/// URIs and
        // extract paths for file processing via the Wails Go backend.
        setTimeout(() => {
          const { doc } = view.state
          const fileUriRegex = /file:\/\/\/.+/g
          const deletions: { from: number; to: number }[] = []
          const uris: string[] = []

          doc.descendants((node, pos) => {
            if (!node.isText || !node.text || !node.text.includes('file:///')) return
            let match
            while ((match = fileUriRegex.exec(node.text)) !== null) {
              const uri = match[0].trim()
              if (existingUris.has(uri)) continue
              deletions.push({
                from: pos + match.index,
                to: pos + match.index + match[0].length,
              })
              uris.push(uri)
            }
          })

          if (deletions.length === 0) return

          let tr = view.state.tr
          for (let i = deletions.length - 1; i >= 0; i--) {
            tr = tr.delete(deletions[i].from, deletions[i].to)
          }
          view.dispatch(tr)

          const paths = uris.map(uri => decodeURIComponent(uri.slice(7)))
          if (paths.length > 0) {
            handlers.onDropFilePaths?.(paths)
          }
        }, 200)

        // Case 1: File objects (macOS/Windows webviews)
        const files = event.dataTransfer?.files
        if (files?.length) {
          event.preventDefault()
          for (const file of Array.from(files)) {
            if (file.type.startsWith('image/')) {
              handlers.onDropImage?.(file)
              continue
            }
            handlers.onDropFile?.(file)
          }
          return true
        }

        // Case 2: File URIs via getData (standard browsers)
        const uriList = event.dataTransfer?.getData('text/uri-list')
        const textData = event.dataTransfer?.getData('text/plain')
        const pathData = uriList || textData
        if (pathData) {
          const paths = parseFileUris(pathData)
          if (paths.length > 0) {
            event.preventDefault()
            handlers.onDropFilePaths?.(paths)
            return true
          }
        }

        return false
      },
    },
    onUpdate: handlers.onUpdate,
  })
}
