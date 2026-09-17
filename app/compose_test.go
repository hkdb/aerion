package app

import (
	"strings"
	"testing"
)

func TestSanitizeAttachmentFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean name passes", "image001.png", "image001.png"},
		{"path separators stripped", "../..\\evil.png", "....evil.png"},
		{"control chars stripped", "inv\r\nite.ics", "invite.ics"},
		{"empty becomes generic", "", "attachment.bin"},
		{"whitespace-only becomes generic", "  \t ", "attachment.bin"},
	}
	for _, c := range cases {
		if got := sanitizeAttachmentFilename(c.in); got != c.want {
			t.Errorf("%s: sanitizeAttachmentFilename(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}

	long := strings.Repeat("a", 300) + ".pdf"
	if got := sanitizeAttachmentFilename(long); len([]rune(got)) != 180 {
		t.Errorf("length cap: got %d runes, want 180", len([]rune(got)))
	}
}

func TestQuotedHTMLReferencesCID(t *testing.T) {
	tests := []struct {
		name string
		html string
		cid  string
		want bool
	}{
		{
			name: "double-quoted src reference",
			html: `<p>hi</p><img src="cid:abc123@mailer">`,
			cid:  "abc123@mailer",
			want: true,
		},
		{
			name: "single-quoted src reference",
			html: `<img src='cid:img1@x'>`,
			cid:  "img1@x",
			want: true,
		},
		{
			name: "cid absent (misclassified document, #381)",
			html: `<p>just text, no embeds</p>`,
			cid:  "aljdusoh@mailer",
			want: false,
		},
		{
			name: "empty html",
			html: "",
			cid:  "abc@x",
			want: false,
		},
		{
			// Substring matching deliberately errs toward keeping: a cid that
			// is a prefix of another referenced cid still counts as present.
			name: "prefix of another cid counts as referenced (err-safe)",
			html: `<img src="cid:abc2@x">`,
			cid:  "abc",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quotedHTMLReferencesCID(tt.html, tt.cid)
			if got != tt.want {
				t.Fatalf("quotedHTMLReferencesCID(%q, %q) = %v, want %v", tt.html, tt.cid, got, tt.want)
			}
		})
	}
}
