package smtp

import (
	"regexp"
	"strings"
	"testing"
)

func TestAddressString_WithName(t *testing.T) {
	addr := Address{Name: "John Doe", Address: "john@example.com"}
	result := addr.String()

	if !strings.Contains(result, "John Doe") {
		t.Errorf("expected result to contain 'John Doe', got %q", result)
	}
	if !strings.Contains(result, "john@example.com") {
		t.Errorf("expected result to contain 'john@example.com', got %q", result)
	}
}

func TestAddressString_WithoutName(t *testing.T) {
	addr := Address{Address: "john@example.com"}
	result := addr.String()

	if result != "john@example.com" {
		t.Errorf("expected 'john@example.com', got %q", result)
	}
}

func TestAddressString_CommaName(t *testing.T) {
	// "Last, First" display names must be quoted — unquoted, the comma
	// splits the header into two addresses and sending fails (#398)
	addr := Address{Name: "Thomas, Annette", Address: "anna@example.com"}
	result := addr.String()

	expected := `"Thomas, Annette" <anna@example.com>`
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestAddressString_QuoteInName(t *testing.T) {
	addr := Address{Name: `An "odd" name`, Address: "odd@example.com"}
	result := addr.String()

	expected := `"An \"odd\" name" <odd@example.com>`
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestAddressString_HeaderInjection(t *testing.T) {
	// A display name carrying CR/LF must never reach the header block raw —
	// that would let a crafted sender name inject arbitrary headers into a
	// reply. net/mail forces non-printable characters into an encoded-word.
	addr := Address{Name: "x\r\nBcc: evil@example.com", Address: "victim@example.com"}
	result := addr.String()

	if strings.ContainsAny(result, "\r\n") {
		t.Errorf("serialized address contains raw CR/LF (header injection): %q", result)
	}
	if !strings.Contains(result, "victim@example.com") {
		t.Errorf("expected result to contain the address, got %q", result)
	}
}

func TestAddressString_Unicode(t *testing.T) {
	addr := Address{Name: "\u65e5\u672c\u8a9e", Address: "test@example.com"}
	result := addr.String()

	if !strings.Contains(result, "test@example.com") {
		t.Errorf("expected result to contain 'test@example.com', got %q", result)
	}
	// The name should be Q-encoded for non-ASCII characters
	if !strings.Contains(result, "=?utf-8?") {
		t.Errorf("expected result to contain encoded name, got %q", result)
	}
}

func TestAllRecipients(t *testing.T) {
	msg := &ComposeMessage{
		To:  []Address{{Address: "to1@example.com"}, {Address: "to2@example.com"}},
		Cc:  []Address{{Address: "cc1@example.com"}},
		Bcc: []Address{{Address: "bcc1@example.com"}},
	}

	recipients := msg.AllRecipients()
	if len(recipients) != 4 {
		t.Fatalf("AllRecipients() returned %d recipients, want 4", len(recipients))
	}

	expected := []string{"to1@example.com", "to2@example.com", "cc1@example.com", "bcc1@example.com"}
	for i, want := range expected {
		if recipients[i] != want {
			t.Errorf("AllRecipients()[%d] = %q, want %q", i, recipients[i], want)
		}
	}
}

func TestAllRecipients_Empty(t *testing.T) {
	msg := &ComposeMessage{}
	recipients := msg.AllRecipients()

	if recipients != nil {
		t.Errorf("AllRecipients() = %v, want nil", recipients)
	}
}

func TestToRFC822_Basic(t *testing.T) {
	msg := &ComposeMessage{
		From:     Address{Name: "Sender", Address: "sender@example.com"},
		To:       []Address{{Name: "Recipient", Address: "recipient@example.com"}},
		Subject:  "Test Subject",
		TextBody: "Hello, this is a test.",
	}

	data, err := msg.ToRFC822()
	if err != nil {
		t.Fatalf("ToRFC822() returned error: %v", err)
	}

	output := string(data)

	checks := []struct {
		label    string
		contains string
	}{
		{"From header", "From:"},
		{"To header", "To:"},
		{"Subject header", "Subject:"},
		{"MIME-Version", "MIME-Version: 1.0"},
		{"text body", "Hello, this is a test"},
	}

	for _, check := range checks {
		if !strings.Contains(output, check.contains) {
			t.Errorf("expected RFC822 output to contain %s (%q), but it was missing", check.label, check.contains)
		}
	}
}

func TestToRFC822MessageIDUsesSenderDomain(t *testing.T) {
	m := &ComposeMessage{
		From:     Address{Name: "Test", Address: "user@Example.com"},
		To:       []Address{{Address: "to@example.org"}},
		Subject:  "hi",
		TextBody: "body",
	}
	raw, err := m.ToRFC822()
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^Message-ID: <[0-9a-f-]{36}@example\.com>\r?$`)
	if !re.Match(raw) {
		t.Errorf("Message-ID does not use sender domain:\n%s", raw)
	}
}

func TestDomainFromEmail(t *testing.T) {
	cases := map[string]string{
		"user@example.com":        "example.com",
		"Name <user@Example.com>": "example.com",
		"":                        "localhost.localdomain",
		"nodomain":                "localhost.localdomain",
		"user@":                   "localhost.localdomain",
	}
	for in, want := range cases {
		if got := domainFromEmail(in); got != want {
			t.Errorf("domainFromEmail(%q) = %q, want %q", in, got, want)
		}
	}
}