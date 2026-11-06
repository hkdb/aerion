package unsubscribe

import (
	"context"
	"testing"
)

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		post       string
		wantHTTP   []string
		wantMailto string
		wantOne    bool
	}{
		{
			name:       "mailto and https with one-click",
			header:     "<mailto:unsub@example.com?subject=unsubscribe>, <https://example.com/u/123>",
			post:       "List-Unsubscribe=One-Click",
			wantHTTP:   []string{"https://example.com/u/123"},
			wantMailto: "mailto:unsub@example.com?subject=unsubscribe",
			wantOne:    true,
		},
		{
			name:     "folded header with whitespace inside brackets",
			header:   "<https://example.com/\r\n u/123>,\r\n <http://example.com/x>",
			wantHTTP: []string{"https://example.com/u/123", "http://example.com/x"},
		},
		{
			name:       "mailto only",
			header:     "<mailto:leave-123@lists.example.org>",
			wantMailto: "mailto:leave-123@lists.example.org",
		},
		{
			name:   "unsupported schemes ignored",
			header: "<javascript:alert(1)>, <file:///etc/passwd>",
		},
		{
			name:     "post header case-insensitive",
			header:   "<https://a.example/u>",
			post:     " list-unsubscribe=one-click ",
			wantOne:  true,
			wantHTTP: []string{"https://a.example/u"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHTTP, gotMailto, gotOne := ParseHeaders(tt.header, tt.post)
			if len(gotHTTP) != len(tt.wantHTTP) {
				t.Fatalf("http urls = %v, want %v", gotHTTP, tt.wantHTTP)
			}
			for i := range gotHTTP {
				if gotHTTP[i] != tt.wantHTTP[i] {
					t.Errorf("http[%d] = %q, want %q", i, gotHTTP[i], tt.wantHTTP[i])
				}
			}
			if gotMailto != tt.wantMailto {
				t.Errorf("mailto = %q, want %q", gotMailto, tt.wantMailto)
			}
			if gotOne != tt.wantOne {
				t.Errorf("oneClick = %v, want %v", gotOne, tt.wantOne)
			}
		})
	}
}

func TestDetectPreferenceOrder(t *testing.T) {
	// One-click wins over mailto
	info := Detect("<mailto:u@example.com>, <https://example.com/u>", "List-Unsubscribe=One-Click", "")
	if info.Method != MethodOneClick || info.Target != "example.com" {
		t.Errorf("got %s/%s, want one-click/example.com", info.Method, info.Target)
	}

	// One-click without https URI is not one-click
	info = Detect("<mailto:u@example.com>, <http://example.com/u>", "List-Unsubscribe=One-Click", "")
	if info.Method != MethodMailto || info.Target != "u@example.com" {
		t.Errorf("got %s/%s, want mailto/u@example.com", info.Method, info.Target)
	}

	// http only -> browser
	info = Detect("<http://news.example.com/unsub?id=1>", "", "")
	if info.Method != MethodBrowser || info.Target != "news.example.com" || info.FromBody {
		t.Errorf("got %+v, want browser from header", info)
	}

	// Nothing at all
	info = Detect("", "", "<p>hello</p>")
	if info.Available {
		t.Errorf("expected no unsubscribe, got %+v", info)
	}
}

func TestFindBodyLink(t *testing.T) {
	body := `<html><body>
		<a href="https://example.com/product">Our product</a>
		<a href="https://tracker.example.com/c?u=unsubscribe">click</a>
		<p>Non vuoi più ricevere queste email? <a href="https://example.com/x/42"><span>Annulla l'iscrizione</span></a></p>
		</body></html>`
	if got := FindBodyLink(body); got != "https://example.com/x/42" {
		t.Errorf("text match: got %q", got)
	}

	// Href-only match used when no text match exists
	body = `<a href="https://example.com/unsubscribe/abc">here</a>`
	if got := FindBodyLink(body); got != "https://example.com/unsubscribe/abc" {
		t.Errorf("href match: got %q", got)
	}

	// Non-http links ignored
	body = `<a href="mailto:x@example.com">Unsubscribe</a><a href="javascript:void(0)">Unsubscribe</a>`
	if got := FindBodyLink(body); got != "" {
		t.Errorf("expected no link, got %q", got)
	}

	info := Detect("", "", `<a href="https://shop.example.com/optout">Unsubscribe</a>`)
	if !info.Available || !info.FromBody || info.BodyURL != "https://shop.example.com/optout" {
		t.Errorf("body fallback: got %+v", info)
	}
}

func TestPostOneClickRejectsNonHTTPS(t *testing.T) {
	if err := PostOneClick(context.Background(), "http://example.com/u", ""); err == nil {
		t.Error("expected error for http URL")
	}
}

func TestPostOneClickBlocksLoopback(t *testing.T) {
	if err := PostOneClick(context.Background(), "https://127.0.0.1:1/u", ""); err == nil {
		t.Error("expected error for loopback URL")
	}
}
