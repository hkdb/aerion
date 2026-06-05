package davutil

import (
	"strings"
	"testing"
)

func TestFixDateValue(t *testing.T) {
	prefix := []byte("<D:getlastmodified>")
	suffix := []byte("</D:getlastmodified>")

	tests := []struct {
		name     string
		dateStr  string
		expected string
	}{
		{
			name:     "numeric +0000 converted to GMT",
			dateStr:  "Fri, 10 Oct 2025 13:41:36 +0000",
			expected: "<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>",
		},
		{
			name:     "numeric +0530 converted to UTC then GMT",
			dateStr:  "Fri, 10 Oct 2025 19:11:36 +0530",
			expected: "<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>",
		},
		{
			name:     "negative offset -0500 converted to UTC then GMT",
			dateStr:  "Fri, 10 Oct 2025 08:41:36 -0500",
			expected: "<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>",
		},
		{
			name:     "already GMT unchanged",
			dateStr:  "Fri, 10 Oct 2025 13:41:36 GMT",
			expected: "<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>",
		},
		{
			name:     "non-date string unchanged",
			dateStr:  "not-a-date",
			expected: "<D:getlastmodified>not-a-date</D:getlastmodified>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(fixDateValue(prefix, tt.dateStr, suffix))
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetlastmodifiedRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "replaces numeric offset in XML",
			input:    `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 +0000</D:getlastmodified>`,
			expected: `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>`,
		},
		{
			name:     "preserves GMT dates",
			input:    `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>`,
			expected: `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>`,
		},
		{
			name:     "handles whitespace around date",
			input:    `<D:getlastmodified>  Fri, 10 Oct 2025 13:41:36 +0000  </D:getlastmodified>`,
			expected: `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified>`,
		},
		{
			name:     "no getlastmodified unchanged",
			input:    `<D:displayname>Test</D:displayname>`,
			expected: `<D:displayname>Test</D:displayname>`,
		},
		{
			name:     "multiple getlastmodified elements",
			input:    `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 +0000</D:getlastmodified><D:getlastmodified>Sat, 11 Oct 2025 10:00:00 +0000</D:getlastmodified>`,
			expected: `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified><D:getlastmodified>Sat, 11 Oct 2025 10:00:00 GMT</D:getlastmodified>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getlastmodifiedRe.ReplaceAllFunc([]byte(tt.input), func(match []byte) []byte {
				sub := getlastmodifiedRe.FindSubmatch(match)
				if len(sub) < 4 {
					return match
				}
				dateStr := string(sub[2])
				return fixDateValue(sub[1], dateStr, sub[3])
			})
			if string(result) != tt.expected {
				t.Errorf("got %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestFixETagValue(t *testing.T) {
	prefix := []byte("<D:getetag>")
	suffix := []byte("</D:getetag>")

	tests := []struct {
		name     string
		etagStr  string
		expected string
	}{
		{
			name:     "unquoted ETag gets quoted",
			etagStr:  "abc123",
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "already quoted ETag unchanged",
			etagStr:  `"abc123"`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "empty string gets quoted",
			etagStr:  "",
			expected: `<D:getetag>""</D:getetag>`,
		},
		{
			name:     "ETag with special characters gets quoted",
			etagStr:  "63c2-5b0-5f1e2a3b",
			expected: `<D:getetag>"63c2-5b0-5f1e2a3b"</D:getetag>`,
		},
		{
			name:     "XML-entity-encoded quotes unchanged (murena.io)",
			etagStr:  `&quot;df8b8abeff032a71c6c1d76db352996f&quot;`,
			expected: `<D:getetag>&quot;df8b8abeff032a71c6c1d76db352996f&quot;</D:getetag>`,
		},
		{
			name:     "weak ETag with literal quotes strips W/",
			etagStr:  `W/"abc123"`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "weak ETag unquoted strips W/ and quotes",
			etagStr:  "W/abc123",
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "weak ETag lowercase strips w/",
			etagStr:  `w/"abc123"`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(fixETagValue(prefix, tt.etagStr, suffix))
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetetagRegex(t *testing.T) {
	applyEtagFix := func(input string) string {
		return string(getetagRe.ReplaceAllFunc([]byte(input), func(match []byte) []byte {
			sub := getetagRe.FindSubmatch(match)
			if len(sub) < 4 {
				return match
			}
			etagStr := strings.TrimSpace(string(sub[2]))
			return fixETagValue(sub[1], etagStr, sub[3])
		}))
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unquoted ETag gets quoted",
			input:    `<D:getetag>abc123</D:getetag>`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "already quoted ETag unchanged",
			input:    `<D:getetag>"abc123"</D:getetag>`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "whitespace around value trimmed and quoted",
			input:    `<D:getetag>  abc123  </D:getetag>`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "non-ETag XML unchanged",
			input:    `<D:displayname>Test</D:displayname>`,
			expected: `<D:displayname>Test</D:displayname>`,
		},
		{
			name:     "multiple getetag elements",
			input:    `<D:getetag>abc</D:getetag><D:getetag>def</D:getetag>`,
			expected: `<D:getetag>"abc"</D:getetag><D:getetag>"def"</D:getetag>`,
		},
		{
			name:     "mixed getlastmodified and getetag",
			input:    `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified><D:getetag>abc123</D:getetag>`,
			expected: `<D:getlastmodified>Fri, 10 Oct 2025 13:41:36 GMT</D:getlastmodified><D:getetag>"abc123"</D:getetag>`,
		},
		{
			name:     "XML-entity-encoded quotes unchanged (murena.io)",
			input:    `<d:getetag>&quot;df8b8abeff032a71c6c1d76db352996f&quot;</d:getetag>`,
			expected: `<d:getetag>&quot;df8b8abeff032a71c6c1d76db352996f&quot;</d:getetag>`,
		},
		{
			name:     "weak ETag gets normalized",
			input:    `<D:getetag>W/"abc123"</D:getetag>`,
			expected: `<D:getetag>"abc123"</D:getetag>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyEtagFix(tt.input)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
