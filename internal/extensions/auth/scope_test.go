package auth

import (
	"testing"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

func TestMissingScopes(t *testing.T) {
	tests := []struct {
		name      string
		granted   []string
		requested []coreapi.AuthScope
		wantLen   int
	}{
		{
			name:      "empty request returns nil",
			granted:   []string{"a", "b"},
			requested: nil,
			wantLen:   0,
		},
		{
			name:    "fully covered returns nil",
			granted: []string{"https://example.com/scope1", "https://example.com/scope2"},
			requested: []coreapi.AuthScope{
				{Resource: "https://example.com/scope1"},
				{Resource: "https://example.com/scope2"},
			},
			wantLen: 0,
		},
		{
			name:    "partial coverage returns uncovered",
			granted: []string{"https://example.com/scope1"},
			requested: []coreapi.AuthScope{
				{Resource: "https://example.com/scope1"},
				{Resource: "https://example.com/scope2"},
			},
			wantLen: 1,
		},
		{
			name:    "no coverage returns all",
			granted: []string{},
			requested: []coreapi.AuthScope{
				{Resource: "https://example.com/scope1"},
				{Resource: "https://example.com/scope2"},
			},
			wantLen: 2,
		},
		{
			name:    "extra granted scopes ignored",
			granted: []string{"a", "b", "c"},
			requested: []coreapi.AuthScope{
				{Resource: "a"},
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingScopes(tt.granted, tt.requested)
			if len(got) != tt.wantLen {
				t.Fatalf("missingScopes: got %d missing, want %d (got: %v)", len(got), tt.wantLen, got)
			}
		})
	}
}

func TestResolveClientConfigID(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		extPresent  bool
		want        coreapi.ClientConfigID
	}{
		{"google with ext config", "google", true, "google-extensions"},
		{"google without ext config falls back to mail", "google", false, "google-mail"},
		{"microsoft with ext config", "microsoft", true, "microsoft-extensions"},
		{"microsoft without ext config", "microsoft", false, "microsoft-mail"},
		{"unknown provider returns empty", "yahoo", true, ""},
		{"empty provider returns empty", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveClientConfigID(tt.provider, tt.extPresent)
			if got != tt.want {
				t.Fatalf("resolveClientConfigID(%q, %v) = %q, want %q", tt.provider, tt.extPresent, got, tt.want)
			}
		})
	}
}
