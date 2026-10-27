package sync

import "testing"

func TestHasNoSelectAttr(t *testing.T) {
	cases := []struct {
		name  string
		attrs []string
		want  bool
	}{
		{"gmail container", []string{`\HasChildren`, `\Noselect`}, true},
		{"case-insensitive", []string{`\NOSELECT`}, true},
		{"selectable folder", []string{`\HasNoChildren`}, false},
		{"special-use only", []string{`\Sent`, `\HasNoChildren`}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		if got := hasNoSelectAttr(tc.attrs); got != tc.want {
			t.Errorf("%s: hasNoSelectAttr(%v) = %v, want %v", tc.name, tc.attrs, got, tc.want)
		}
	}
}
