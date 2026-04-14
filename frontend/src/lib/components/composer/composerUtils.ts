/**
 * Utility functions for the email composer
 * Pure functions with no dependencies on component state
 */

/**
 * Convert base64 string to byte array for attachment content
 */
export function base64ToBytes(base64: string): number[] {
  const binaryString = atob(base64)
  const bytes = new Array(binaryString.length)
  for (let i = 0; i < binaryString.length; i++) {
    bytes[i] = binaryString.charCodeAt(i)
  }
  return bytes
}

/**
 * Format file size for display
 */
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * Get icon name for file type based on MIME type
 */
export function getFileIcon(contentType: string): string {
  if (contentType.startsWith('image/')) return 'mdi:file-image'
  if (contentType.startsWith('video/')) return 'mdi:file-video'
  if (contentType.startsWith('audio/')) return 'mdi:file-music'
  if (contentType === 'application/pdf') return 'mdi:file-pdf-box'
  if (contentType.includes('spreadsheet') || contentType.includes('excel')) return 'mdi:file-excel'
  if (contentType.includes('document') || contentType.includes('word')) return 'mdi:file-word'
  if (contentType.includes('presentation') || contentType.includes('powerpoint')) return 'mdi:file-powerpoint'
  if (contentType.includes('zip') || contentType.includes('compressed') || contentType.includes('archive')) return 'mdi:folder-zip'
  if (contentType.startsWith('text/')) return 'mdi:file-document'
  return 'mdi:file'
}

/**
 * Convert HTML to plain text (basic conversion)
 */
export function htmlToPlainText(html: string): string {
  // Create a temporary element to parse HTML
  const temp = document.createElement('div')
  temp.innerHTML = html

  // Replace <br> and block elements with newlines
  const blockElements = temp.querySelectorAll('p, div, br, h1, h2, h3, h4, h5, h6, li')
  blockElements.forEach(el => {
    if (el.tagName === 'BR') {
      el.replaceWith('\n')
    } else if (el.tagName === 'LI') {
      el.prepend(document.createTextNode('• '))
      el.append(document.createTextNode('\n'))
    } else {
      el.append(document.createTextNode('\n'))
    }
  })

  // Get text content and clean up excessive newlines
  let text = temp.textContent || ''
  text = text.replace(/\n{3,}/g, '\n\n')  // Max 2 consecutive newlines
  return text.trim()
}

/**
 * Convert plain text to basic HTML
 */
export function plainTextToHtml(text: string): string {
  // Escape HTML entities
  const escaped = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')

  // Convert newlines to paragraphs
  const paragraphs = escaped.split(/\n\n+/)
  if (paragraphs.length > 1) {
    return paragraphs.map(p => `<p>${p.replace(/\n/g, '<br>')}</p>`).join('')
  }

  // Single block - just convert newlines to <br>
  return `<p>${escaped.replace(/\n/g, '<br>')}</p>`
}

/**
 * Read file as base64 string (without data URL prefix)
 */
export function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      // Remove the data URL prefix (e.g., "data:image/png;base64,")
      const base64 = result.split(',')[1]
      resolve(base64)
    }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

/**
 * Read file as data URL (for inline images)
 */
export function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as string)
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

/**
 * Add inline margin:0 to <p> tags so recipients see single-spaced paragraphs.
 * The composer uses paragraph-based Enter (splitBlock) for performance, and CSS
 * handles zero margins during editing. This function ensures the sent HTML also
 * renders single-spaced in all email clients.
 */
export function addParagraphStyles(html: string): string {
  const result = html
    // Convert empty paragraphs to <br> — TipTap uses <p></p> internally for
    // Enter key performance, but <br> produces tighter spacing in email clients
    .replace(/<p><\/p>/g, '<br>')
    // Add inline margin:0 to remaining (content) paragraphs for consistent
    // rendering across email clients
    .replace(/<p([ >])/g, (_, after) => `<p style="margin:0"${after}`)
    .replace(/style="margin:0" style="/g, 'style="margin:0;')
  // Wrap in div with line-height:1.2 so <br> elements and paragraphs
  // render with tighter spacing matching Google/Outlook style
  return `<div style="line-height:1.25">${result}</div>`
}

/**
 * Reverse addParagraphStyles for loading HTML back into TipTap editor.
 * Converts <br> back to <p></p> (TipTap's internal paragraph model)
 * and strips inline margin:0 styles.
 */
export function stripParagraphStyles(html: string): string {
  return html
    // Remove line-height wrapper div added by addParagraphStyles
    .replace(/^<div style="line-height:[^"]*">([\s\S]*)<\/div>$/, '$1')
    // Convert standalone <br> back to empty paragraphs for TipTap
    .replace(/<br\s*\/?>\s*(?=<p|<br|$)/g, '<p></p>')
    // Strip inline margin:0 from paragraphs
    .replace(/<p([^>]*) style="margin:\s*0;?"([^>]*)>/g, '<p$1$2>')
    .replace(/<p style="margin:\s*0;?">/g, '<p>')
}

/**
 * Keywords that suggest attachments should be present
 */
export const ATTACHMENT_KEYWORDS = [
  'attach', 'attached', 'attaching', 'attachment', 'attachments',
  'enclosed', 'enclosing', 'enclose',
  'see attached', 'please find attached', 'i have attached', "i've attached"
]

/**
 * Check if text contains keywords that suggest an attachment should be present.
 * Uses word-boundary matching to avoid false positives from substrings.
 */
export function textMentionsAttachment(text: string): boolean {
  const lowerText = text.toLowerCase()
  return ATTACHMENT_KEYWORDS.some(keyword => {
    // Handle curly apostrophes so "i've" matches both i've and i've
    const escaped = keyword.replace(/'/g, "['\u2019]")
    const pattern = new RegExp(`\\b${escaped}\\b`)
    return pattern.test(lowerText)
  })
}

/**
 * Parse file URIs from drag-and-drop data (text/uri-list or text/plain).
 * Handles file:// URIs and absolute paths. Skips non-file URIs (http://, etc.)
 * and RFC 2483 comment lines (starting with #).
 */
export function parseFileUris(data: string): string[] {
  return data
    .split(/[\r\n]+/)
    .map(line => line.trim())
    .filter(line => line && !line.startsWith('#'))
    .map(line => {
      if (line.startsWith('file://')) {
        return decodeURIComponent(line.slice(7))
      }
      if (line.startsWith('/')) return line
      return ''
    })
    .filter(Boolean)
}
