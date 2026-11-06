// Package smtp provides SMTP client functionality
package smtp

import (
	"bytes"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/message"
)

// MDNDisposition represents the disposition type for an MDN
type MDNDisposition string

const (
	// MDNDisplayed indicates the message was displayed to the user
	MDNDisplayed MDNDisposition = "displayed"
	// MDNDeleted indicates the message was deleted without being displayed
	MDNDeleted MDNDisposition = "deleted"
)

// BuildMDN creates an RFC 3798 compliant Message Disposition Notification
// Parameters:
//   - originalMsg: The original message that requested the receipt
//   - fromName: The name of the sender (current user)
//   - fromEmail: The email of the sender (current user)
//   - disposition: The disposition type (displayed, deleted, etc.)
//
// Returns the complete MDN message as bytes ready to be sent via SMTP
func BuildMDN(originalMsg *message.Message, fromName, fromEmail string, disposition MDNDisposition) ([]byte, error) {
	if originalMsg == nil {
		return nil, fmt.Errorf("original message is required")
	}
	if originalMsg.ReadReceiptTo == "" {
		return nil, fmt.Errorf("original message has no read receipt request")
	}
	if fromEmail == "" {
		return nil, fmt.Errorf("from email is required")
	}

	// Generate a unique message ID
	msgID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), "mdn", domainFromEmail(fromEmail))

	// Build the recipient (who requested the receipt)
	recipientEmail := extractEmailAddress(originalMsg.ReadReceiptTo)

	// Generate boundary for multipart message
	boundary := fmt.Sprintf("=_mdn_%d", time.Now().UnixNano())

	var buf bytes.Buffer

	// Write headers
	buf.WriteString(fmt.Sprintf("From: %s\r\n", formatAddress(fromName, fromEmail)))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", originalMsg.ReadReceiptTo))
	buf.WriteString(fmt.Sprintf("Subject: Read: %s\r\n", originalMsg.Subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: %s\r\n", msgID))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/report; report-type=disposition-notification; boundary=\"%s\"\r\n", boundary))
	buf.WriteString("\r\n")

	// Write human-readable part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Your message\r\n\r\n")
	buf.WriteString(fmt.Sprintf("  To: %s\r\n", fromEmail))
	buf.WriteString(fmt.Sprintf("  Subject: %s\r\n", originalMsg.Subject))
	buf.WriteString(fmt.Sprintf("  Sent: %s\r\n", originalMsg.Date.Format(time.RFC1123Z)))
	buf.WriteString("\r\n")
	buf.WriteString(fmt.Sprintf("was %s on %s.\r\n", disposition, time.Now().Format(time.RFC1123Z)))
	buf.WriteString("\r\n")

	// Write machine-readable disposition notification part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: message/disposition-notification\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Reporting-UA: Aerion/1.0\r\n")
	if originalMsg.MessageID != "" {
		buf.WriteString(fmt.Sprintf("Original-Message-ID: %s\r\n", originalMsg.MessageID))
	}
	buf.WriteString(fmt.Sprintf("Final-Recipient: rfc822; %s\r\n", fromEmail))
	buf.WriteString(fmt.Sprintf("Original-Recipient: rfc822; %s\r\n", recipientEmail))
	buf.WriteString(fmt.Sprintf("Disposition: manual-action/MDN-sent-manually; %s\r\n", disposition))
	buf.WriteString("\r\n")

	// Close multipart
	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return buf.Bytes(), nil
}

// domainFromEmail extracts the domain part from an email address.
// It accepts plain addresses ("user@example.com") as well as formatted ones
// ("Name <user@example.com>") and falls back to "localhost.localdomain" when
// no usable domain is found, so the result is always safe to use in a
// Message-ID right-hand side.
func domainFromEmail(email string) string {
	addr := strings.TrimSpace(email)
	if parsed, err := mail.ParseAddress(addr); err == nil {
		addr = parsed.Address
	}
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		domain := strings.Trim(strings.TrimSpace(addr[i+1:]), "<>")
		if domain != "" && !strings.ContainsAny(domain, " \t<>@") {
			return strings.ToLower(domain)
		}
	}
	return "localhost.localdomain"
}

// extractEmailAddress extracts just the email address from a potentially formatted address
// e.g., "John Doe <john@example.com>" -> "john@example.com"
func extractEmailAddress(addr string) string {
	addr = strings.TrimSpace(addr)

	// Check if it's in "Name <email>" format
	if start := strings.Index(addr, "<"); start != -1 {
		if end := strings.Index(addr, ">"); end > start {
			return addr[start+1 : end]
		}
	}

	// Otherwise, assume it's just an email address
	return addr
}

// formatAddress formats a name and email into a proper address string.
// Delegates to Address.String() so MDN headers get the same RFC 5322
// quoting + RFC 2047 encoding as the send path (#398).
func formatAddress(name, email string) string {
	return Address{Name: name, Address: email}.String()
}