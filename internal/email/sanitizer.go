// Package email provides email content processing utilities
package email

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Sanitizer provides HTML email sanitization
type Sanitizer struct {
	policy *bluemonday.Policy
}

// NewSanitizer creates a new HTML sanitizer configured for email content
func NewSanitizer() *Sanitizer {
	// Create a custom policy for email HTML
	p := bluemonday.NewPolicy()

	// ==========================================================================
	// Document structure
	// ==========================================================================
	p.AllowElements("html", "head", "body", "title", "meta", "link")

	// ==========================================================================
	// Semantic/structural elements
	// ==========================================================================
	p.AllowElements("div", "span", "p", "br", "hr", "wbr")
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowElements("article", "section", "header", "footer", "main", "aside", "nav")
	p.AllowElements("figure", "figcaption")
	p.AllowElements("details", "summary")
	p.AllowElements("address")

	// ==========================================================================
	// Deprecated but commonly used in HTML emails
	// ==========================================================================
	p.AllowElements("center") // Used for centering content (common in email templates)
	p.AllowElements("font")   // Legacy font styling
	p.AllowElements("basefont")
	p.AllowElements("big")
	p.AllowElements("tt") // Teletype/monospace

	// ==========================================================================
	// Text formatting
	// ==========================================================================
	p.AllowElements("b", "i", "u", "s", "strong", "em", "mark", "small", "sub", "sup")
	p.AllowElements("del", "ins", "strike")
	p.AllowElements("abbr", "acronym")
	p.AllowElements("cite", "dfn", "q")
	p.AllowElements("kbd", "samp", "var")
	p.AllowElements("bdi", "bdo")
	p.AllowElements("ruby", "rt", "rp") // Ruby annotations (for East Asian text)
	p.AllowElements("time")
	p.AllowElements("data")

	// ==========================================================================
	// Lists
	// ==========================================================================
	p.AllowElements("ul", "ol", "li", "dl", "dt", "dd")
	p.AllowElements("menu")

	// ==========================================================================
	// Tables (heavily used in HTML emails for layout)
	// ==========================================================================
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td")
	p.AllowElements("caption", "colgroup", "col")

	// ==========================================================================
	// Links
	// ==========================================================================
	p.AllowAttrs("href", "title", "name").OnElements("a")
	p.AllowElements("a")
	p.AllowRelativeURLs(false)
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	// ==========================================================================
	// Images and media
	// ==========================================================================
	p.AllowAttrs("src", "alt", "title", "width", "height", "loading").OnElements("img")
	p.AllowElements("img")
	p.AllowElements("picture", "source")
	p.AllowElements("map", "area")

	// Allow cid: scheme for inline attachments, data: for base64, http/https for remote
	p.AllowURLSchemes("cid", "data", "http", "https", "mailto")

	// ==========================================================================
	// Blockquotes and code (common in email replies)
	// ==========================================================================
	p.AllowElements("blockquote", "pre", "code")

	// ==========================================================================
	// Style elements (safe since emails render in sandboxed iframe)
	// ==========================================================================
	p.AllowElements("style")

	// ==========================================================================
	// Global attributes
	// ==========================================================================
	p.AllowAttrs("style").Globally()
	p.AllowAttrs("class", "id").Globally()
	p.AllowAttrs("title").Globally()
	p.AllowAttrs("lang", "dir").Globally()
	p.AllowAttrs("role", "aria-label", "aria-hidden", "aria-describedby").Globally()

	// ==========================================================================
	// Table-specific attributes (crucial for email layouts)
	// ==========================================================================
	p.AllowAttrs("align", "valign", "width", "height", "border", "cellpadding", "cellspacing").OnElements("table", "td", "th", "tr", "tbody", "thead", "tfoot")
	p.AllowAttrs("colspan", "rowspan", "scope", "headers").OnElements("td", "th")
	p.AllowAttrs("bgcolor", "background").OnElements("table", "td", "th", "tr", "tbody", "thead", "tfoot", "body", "center")

	// ==========================================================================
	// Legacy formatting attributes
	// ==========================================================================
	p.AllowAttrs("color", "size", "face").OnElements("font", "basefont")
	p.AllowAttrs("align").OnElements("div", "p", "h1", "h2", "h3", "h4", "h5", "h6", "center", "img", "table", "td", "th", "tr")
	p.AllowAttrs("noshade", "size", "width").OnElements("hr")

	// ==========================================================================
	// Data attributes (used by email templates for lazy-loading, CSS selectors, etc.)
	// ==========================================================================
	p.AllowDataAttributes()

	return &Sanitizer{policy: p}
}

// Sanitize cleans HTML content for safe display
func (s *Sanitizer) Sanitize(html string) string {
	// Remove script tags for security
	html = removeScriptTags(html)
	// Note: We keep <style> blocks since emails are rendered in a sandboxed iframe
	// and many HTML emails rely on CSS rules for proper layout

	// Apply bluemonday sanitization
	return s.policy.Sanitize(html)
}

// SanitizeWithRemoteImageBlocking sanitizes HTML and replaces remote images with placeholders
func (s *Sanitizer) SanitizeWithRemoteImageBlocking(html string) string {
	// First sanitize
	sanitized := s.Sanitize(html)

	// Then replace remote images with placeholders
	return BlockRemoteImages(sanitized)
}

// removeScriptTags removes all script tags and their content
func removeScriptTags(html string) string {
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	return re.ReplaceAllString(html, "")
}

// removeStyleTags removes all style tags and their content
// Note: This removes <style> blocks but preserves inline style attributes
func removeStyleTags(html string) string {
	re := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	return re.ReplaceAllString(html, "")
}

// BlockRemoteImages replaces remote image sources with a placeholder SVG,
// storing the original URL in a data-original-src attribute for later restoration.
func BlockRemoteImages(html string) string {
	// Match img tags with http/https sources
	re := regexp.MustCompile(`(?i)<img([^>]*)\ssrc=["'](https?://[^"']+)["']([^>]*)>`)

	result := re.ReplaceAllStringFunc(html, func(match string) string {
		// Extract the original src
		srcRe := regexp.MustCompile(`(?i)src=["'](https?://[^"']+)["']`)
		srcMatch := srcRe.FindStringSubmatch(match)
		if len(srcMatch) < 2 {
			return match
		}

		originalSrc := srcMatch[1]

		// Replace with a placeholder, storing original URL in data attribute
		return srcRe.ReplaceAllString(match, `src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='100' height='100'%3E%3Crect fill='%23ddd' width='100' height='100'/%3E%3Ctext x='50%25' y='50%25' text-anchor='middle' dy='.3em' fill='%23666' font-size='12'%3EImage blocked%3C/text%3E%3C/svg%3E" data-original-src="`+escapeHTML(originalSrc)+`"`)
	})

	// Block remote URLs in CSS url() references (background-image, background, etc.)
	// Handles all quote encodings: raw, decimal (&#39;/&#34;), hex (&#x27;/&#x22;), named (&apos;/&quot;)
	cssURLRe := regexp.MustCompile(`(?i)url\(\s*(?:['"]|&#(?:39|x27|34|x22);|&(?:apos|quot);)?\s*https?://[^)]*?(?:['"]|&#(?:39|x27|34|x22);|&(?:apos|quot);)?\s*\)`)
	result = cssURLRe.ReplaceAllString(result, `url()`)

	// Block HTML background attribute with remote URLs
	bgAttrRe := regexp.MustCompile(`(?i)\bbackground\s*=\s*["'](https?://[^"']+)["']`)
	result = bgAttrRe.ReplaceAllString(result, `background=""`)

	return result
}

// escapeHTML escapes HTML special characters for use in attributes
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// ExtractPlainTextFromHTML converts HTML to plain text
func ExtractPlainTextFromHTML(html string) string {
	// Remove style and script content
	html = removeScriptTags(html)
	html = removeStyleTags(html)

	// Convert common block elements to newlines
	blockTags := regexp.MustCompile(`(?i)</(p|div|br|h[1-6]|li|tr)>`)
	html = blockTags.ReplaceAllString(html, "\n")

	// Handle <br> tags
	brTags := regexp.MustCompile(`(?i)<br[^>]*>`)
	html = brTags.ReplaceAllString(html, "\n")

	// Remove all remaining HTML tags
	tagRe := regexp.MustCompile(`<[^>]*>`)
	text := tagRe.ReplaceAllString(html, "")

	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")

	// Collapse multiple whitespace
	wsRe := regexp.MustCompile(`[ \t]+`)
	text = wsRe.ReplaceAllString(text, " ")

	// Collapse multiple newlines
	nlRe := regexp.MustCompile(`\n{3,}`)
	text = nlRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
