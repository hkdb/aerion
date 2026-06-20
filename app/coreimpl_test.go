package app

import "testing"

// htmlCoreImpl is the coreapi.HTML surface extensions use to render untrusted
// HTML. It must strip scripts/handlers and block remote images via the shared
// mail sanitizer — extensions rely on this so they never carry their own policy.
func TestHTMLCoreImpl_Sanitize(t *testing.T) {
	h := htmlCoreImpl{sanitizer: sharedSanitizer}

	out := h.Sanitize(`<p onclick="evil()">hi</p><script>alert(1)</script>`)
	if contains(out, "<script") {
		t.Errorf("script tag survived sanitize: %q", out)
	}
	if contains(out, "onclick") {
		t.Errorf("event handler survived sanitize: %q", out)
	}
	if !contains(out, "hi") {
		t.Errorf("expected text content to survive: %q", out)
	}

	// Remote images are blocked: the live <img src> is swapped for a
	// placeholder (the original URL is parked in data-original-src for later
	// opt-in restoration).
	img := h.Sanitize(`<img src="http://tracker.example.com/x.png">`)
	if contains(img, `<img src="http`) {
		t.Errorf("remote image src should be blocked, got: %q", img)
	}
	if !contains(img, "data-original-src") {
		t.Errorf("blocked image should record data-original-src, got: %q", img)
	}

	// Nil sanitizer must not panic.
	if got := (htmlCoreImpl{}).Sanitize("<p>x</p>"); got != "" {
		t.Errorf("nil sanitizer should return empty, got %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
