package v1

// HTML exposes the host's HTML sanitizer to extensions so they can render
// untrusted HTML (e.g. event descriptions synced from Outlook/Graph) without
// importing internal/email or carrying their own bluemonday policy. The
// implementation reuses the same sanitizer mail uses, so extensions inherit
// script/handler stripping and remote-image blocking for free.
type HTML interface {
	// Sanitize returns a safe subset of the input HTML: scripts, event
	// handlers, and dangerous tags are removed and remote images are blocked.
	Sanitize(html string) string
}
