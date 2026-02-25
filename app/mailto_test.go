package app

import (
	"reflect"
	"testing"
)

func TestParseMailtoURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		expected *MailtoData
	}{
		{
			name:   "Simple address",
			rawURL: "mailto:test@example.com",
			expected: &MailtoData{
				To: []string{"test@example.com"},
			},
		},
		{
			name:   "Multiple addresses",
			rawURL: "mailto:test1@example.com,test2@example.com",
			expected: &MailtoData{
				To: []string{"test1@example.com", "test2@example.com"},
			},
		},
		{
			name:   "With subject and body",
			rawURL: "mailto:test@example.com?subject=Hello&body=World",
			expected: &MailtoData{
				To:      []string{"test@example.com"},
				Subject: "Hello",
				Body:    "World",
			},
		},
		{
			name:   "Full features",
			rawURL: "mailto:to@example.com?cc=cc@example.com&bcc=bcc@example.com&subject=Test&body=Message",
			expected: &MailtoData{
				To:      []string{"to@example.com"},
				Cc:      []string{"cc@example.com"},
				Bcc:     []string{"bcc@example.com"},
				Subject: "Test",
				Body:    "Message",
			},
		},
		{
			name:   "Encoded characters",
			rawURL: "mailto:test%40example.com?subject=Hello%20World",
			expected: &MailtoData{
				To:      []string{"test@example.com"},
				Subject: "Hello World",
			},
		},
		{
			name:     "Invalid prefix",
			rawURL:   "http://example.com",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMailtoURL(tt.rawURL)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ParseMailtoURL() = %v, want %v", got, tt.expected)
			}
		})
	}
}
