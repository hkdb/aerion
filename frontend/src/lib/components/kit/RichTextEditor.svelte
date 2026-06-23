<script lang="ts">
  // RichTextEditor — generic kit primitive: a small TipTap-backed rich-text
  // editor with a minimal formatting toolbar (bold / italic / underline /
  // lists / link). Greenfield SDK surface, peer of ColorPicker — NOT coupled
  // to the mail composer's editor (composerEditor.ts stays independent).
  //
  // Emits both HTML and plain text on every change so callers can persist a
  // rich body alongside a plaintext fallback. The host sanitizes untrusted
  // HTML before it ever reaches a viewer; this editor only authors content.

  import { onMount } from 'svelte'
  import { Editor } from '@tiptap/core'
  import StarterKit from '@tiptap/starter-kit'
  import Underline from '@tiptap/extension-underline'
  import Link from '@tiptap/extension-link'
  import Placeholder from '@tiptap/extension-placeholder'

  interface Props {
    /** Initial HTML used at mount time. Reseeding mid-life goes through setContent(). */
    value?: string
    /** Fired on every edit with the current HTML and its plaintext rendering. */
    onChange?: (html: string, text: string) => void
    /** When true the editor is read-only and the toolbar is disabled. */
    disabled?: boolean
    /** Placeholder shown while empty. */
    placeholder?: string
  }

  let { value = '', onChange, disabled = false, placeholder = '' }: Props = $props()

  let element: HTMLDivElement
  // $state.raw, NOT $state: a TipTap Editor must never be wrapped in Svelte's
  // deep reactive proxy — it proxies ProseMirror's internal state, corrupts the
  // editor, and leaks proxied objects into the reactive graph across mount/
  // destroy cycles (symptom: detail pane reactivity freezing after repeated
  // open/cancel). Reassignment (editor = …) is still reactive with .raw.
  let editor = $state.raw<Editor | null>(null)
  // Drives toolbar active-state highlighting; bumped on every transaction.
  let tick = $state(0)

  onMount(() => {
    editor = new Editor({
      element,
      editable: !disabled,
      extensions: [
        StarterKit,
        Underline,
        Link.configure({ openOnClick: false }),
        Placeholder.configure({ placeholder }),
      ],
      content: value ?? '',
      editorProps: {
        attributes: {
          class: 'rte-content focus:outline-none',
        },
      },
      onTransaction: () => {
        tick++
      },
      onUpdate: ({ editor: ed }) => {
        onChange?.(ed.getHTML(), ed.getText())
      },
    })

    return () => {
      editor?.destroy()
      editor = null
    }
  })

  // No reactive content-syncing: the dialog remounts this editor each time it
  // opens, so onMount's `content: value` seeds it fresh. `value` must be ready
  // before mount — the parent passes a $derived for that. (A reactive seed that
  // calls setContent on every value/editor change churned the scheduler and
  // froze the dialog.)
  $effect(() => {
    editor?.setEditable(!disabled)
  })

  function isActive(name: string, attrs?: Record<string, unknown>): boolean {
    void tick
    return !!editor?.isActive(name, attrs)
  }

  function toggleBold() {
    editor?.chain().focus().toggleBold().run()
  }
  function toggleItalic() {
    editor?.chain().focus().toggleItalic().run()
  }
  function toggleUnderline() {
    editor?.chain().focus().toggleUnderline().run()
  }
  function toggleBullet() {
    editor?.chain().focus().toggleBulletList().run()
  }
  function toggleOrdered() {
    editor?.chain().focus().toggleOrderedList().run()
  }
  function toggleLink() {
    if (!editor) return
    if (editor.isActive('link')) {
      editor.chain().focus().unsetLink().run()
      return
    }
    const url = window.prompt('URL')
    if (!url) return
    editor.chain().focus().setLink({ href: url }).run()
  }
</script>

<div class="rte border border-border rounded bg-background focus-within:ring-2 focus-within:ring-primary/50">
  <div class="rte-toolbar flex items-center gap-0.5 border-b border-border px-1 py-0.5" class:opacity-50={disabled}>
    <button type="button" class="rte-btn" class:active={isActive('bold')} {disabled} onclick={toggleBold} aria-label="Bold"><b>B</b></button>
    <button type="button" class="rte-btn" class:active={isActive('italic')} {disabled} onclick={toggleItalic} aria-label="Italic"><i>I</i></button>
    <button type="button" class="rte-btn" class:active={isActive('underline')} {disabled} onclick={toggleUnderline} aria-label="Underline"><u>U</u></button>
    <span class="rte-sep"></span>
    <button type="button" class="rte-btn" class:active={isActive('bulletList')} {disabled} onclick={toggleBullet} aria-label="Bullet list">&bull;</button>
    <button type="button" class="rte-btn" class:active={isActive('orderedList')} {disabled} onclick={toggleOrdered} aria-label="Numbered list">1.</button>
    <span class="rte-sep"></span>
    <button type="button" class="rte-btn" class:active={isActive('link')} {disabled} onclick={toggleLink} aria-label="Link">&#128279;</button>
  </div>
  <div bind:this={element} class="rte-editor text-sm text-foreground max-h-60 overflow-y-auto"></div>
</div>


<style>
  .rte-btn {
    min-width: 1.5rem;
    height: 1.5rem;
    padding: 0 0.25rem;
    border-radius: 0.25rem;
    font-size: 0.8rem;
    line-height: 1;
    color: hsl(var(--muted-foreground));
  }
  .rte-btn:hover:not(:disabled) {
    background: hsl(var(--muted));
    color: hsl(var(--foreground));
  }
  .rte-btn.active {
    background: hsl(var(--muted));
    color: hsl(var(--foreground));
  }
  .rte-btn:disabled {
    cursor: default;
  }
  .rte-sep {
    width: 1px;
    height: 1rem;
    margin: 0 0.25rem;
    background: hsl(var(--border));
  }
  /* Guarantee a visible, clickable editing area even when empty. min-height
     on the wrapper covers the brief window before ProseMirror mounts; the
     ProseMirror node itself fills the wrapper so a click anywhere focuses it. */
  .rte-editor {
    min-height: 6rem;
    cursor: text;
  }
  .rte-editor :global(.ProseMirror) {
    min-height: 6rem;
    padding: 0.375rem 0.5rem;
    outline: none;
  }
  .rte-editor :global(p) {
    margin: 0 0 0.5rem;
  }
  .rte-editor :global(p:last-child) {
    margin-bottom: 0;
  }
  .rte-editor :global(ul),
  .rte-editor :global(ol) {
    margin: 0 0 0.5rem;
    padding-left: 1.25rem;
  }
  .rte-editor :global(ul) {
    list-style: disc;
  }
  .rte-editor :global(ol) {
    list-style: decimal;
  }
  .rte-editor :global(a) {
    color: hsl(var(--primary));
    text-decoration: underline;
  }
  .rte-editor :global(.is-editor-empty:first-child::before) {
    content: attr(data-placeholder);
    float: left;
    height: 0;
    pointer-events: none;
    color: hsl(var(--muted-foreground));
  }
</style>
