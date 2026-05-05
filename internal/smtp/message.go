// Package smtp provides SMTP client functionality for Aerion
package smtp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Address represents an email address with optional display name
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// String returns the RFC 5322 formatted address
func (a Address) String() string {
	if a.Name == "" {
		return a.Address
	}
	// Encode the name if it contains non-ASCII characters
	encodedName := mime.QEncoding.Encode("utf-8", a.Name)
	return fmt.Sprintf("%s <%s>", encodedName, a.Address)
}

// Attachment represents a file attachment
type Attachment struct {
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type"`
	Content       []byte `json:"content"`
	ContentBase64 string `json:"content_base64,omitempty"` // Base64 string for efficient Wails RPC transfer
	ContentID     string `json:"content_id"`               // For inline attachments
	Inline        bool   `json:"inline"`
}

// ResolveContent returns the attachment's binary content.
// It prefers the Content field; if empty, decodes ContentBase64.
func (a *Attachment) ResolveContent() ([]byte, error) {
	if len(a.Content) > 0 {
		return a.Content, nil
	}
	if a.ContentBase64 != "" {
		return base64.StdEncoding.DecodeString(a.ContentBase64)
	}
	return nil, nil
}

// ComposeMessage represents an email message to be composed and sent
type ComposeMessage struct {
	// Envelope
	From    Address   `json:"from"`
	To      []Address `json:"to"`
	Cc      []Address `json:"cc"`
	Bcc     []Address `json:"bcc"`
	ReplyTo *Address  `json:"reply_to,omitempty"`
	Subject string    `json:"subject"`

	// Content
	TextBody string `json:"text_body"` // Plain text version
	HTMLBody string `json:"html_body"` // HTML version

	// Attachments
	Attachments []Attachment `json:"attachments"`

	// Headers
	InReplyTo  string   `json:"in_reply_to,omitempty"` // Message-ID of the message being replied to
	References []string `json:"references,omitempty"`  // Thread references

	// Options
	RequestReadReceipt bool `json:"request_read_receipt"`
	SignMessage        bool `json:"sign_message"`        // S/MIME sign this message
	EncryptMessage     bool `json:"encrypt_message"`     // S/MIME encrypt this message
	PGPSignMessage     bool `json:"pgp_sign_message"`    // PGP sign this message
	PGPEncryptMessage  bool `json:"pgp_encrypt_message"` // PGP encrypt this message
}

// AllRecipients returns all recipients (To + Cc + Bcc)
func (m *ComposeMessage) AllRecipients() []string {
	var recipients []string
	for _, addr := range m.To {
		recipients = append(recipients, addr.Address)
	}
	for _, addr := range m.Cc {
		recipients = append(recipients, addr.Address)
	}
	for _, addr := range m.Bcc {
		recipients = append(recipients, addr.Address)
	}
	return recipients
}

// ToRFC822 converts the message to RFC 822 format for sending
func (m *ComposeMessage) ToRFC822() ([]byte, error) {
	var buf bytes.Buffer

	// Generate Message-ID
	messageID := fmt.Sprintf("<%s@aerion>", uuid.New().String())

	// Write headers
	writeHeader(&buf, "From", m.From.String())
	writeHeader(&buf, "To", formatAddresses(m.To))
	if len(m.Cc) > 0 {
		writeHeader(&buf, "Cc", formatAddresses(m.Cc))
	}
	// Note: BCC is not written to headers (handled by SMTP)
	if m.ReplyTo != nil {
		writeHeader(&buf, "Reply-To", m.ReplyTo.String())
	}
	writeHeader(&buf, "Subject", encodeSubject(m.Subject))
	writeHeader(&buf, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", messageID)
	writeHeader(&buf, "MIME-Version", "1.0")
	writeHeader(&buf, "User-Agent", "Aerion Email Client")

	// Threading headers
	if m.InReplyTo != "" {
		writeHeader(&buf, "In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		writeHeader(&buf, "References", strings.Join(m.References, " "))
	}

	// Read receipt
	if m.RequestReadReceipt {
		writeHeader(&buf, "Disposition-Notification-To", m.From.String())
	}

	// Determine message structure
	hasHTML := m.HTMLBody != ""
	hasText := m.TextBody != ""
	hasAttachments := len(m.Attachments) > 0

	// Separate inline and regular attachments
	var inlineAttachments, regularAttachments []Attachment
	for _, att := range m.Attachments {
		if att.Inline {
			inlineAttachments = append(inlineAttachments, att)
		} else {
			regularAttachments = append(regularAttachments, att)
		}
	}

	// Choose message structure based on content
	switch {
	case hasAttachments && (hasHTML || hasText):
		// multipart/mixed with multipart/alternative or just text
		if err := writeMultipartMixed(&buf, m, regularAttachments, inlineAttachments); err != nil {
			return nil, err
		}
	case hasHTML && hasText:
		// multipart/alternative (HTML + plain text)
		if err := writeMultipartAlternative(&buf, m.TextBody, m.HTMLBody); err != nil {
			return nil, err
		}
	case hasHTML:
		// HTML only
		writeHeader(&buf, "Content-Type", "text/html; charset=utf-8")
		writeHeader(&buf, "Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		writeQuotedPrintable(&buf, m.HTMLBody)
	case hasText:
		// Plain text only
		writeHeader(&buf, "Content-Type", "text/plain; charset=utf-8")
		writeHeader(&buf, "Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		writeQuotedPrintable(&buf, m.TextBody)
	default:
		// Empty message
		writeHeader(&buf, "Content-Type", "text/plain; charset=utf-8")
		buf.WriteString("\r\n")
	}

	return buf.Bytes(), nil
}

// writeHeader writes a single header line.
// CRLF characters are stripped from the value to prevent header injection.
func writeHeader(w io.Writer, name, value string) {
	value = strings.NewReplacer("\r\n", "", "\r", "", "\n", "").Replace(value)
	fmt.Fprintf(w, "%s: %s\r\n", name, value)
}

// formatAddresses formats a list of addresses for headers
func formatAddresses(addrs []Address) string {
	var parts []string
	for _, addr := range addrs {
		parts = append(parts, addr.String())
	}
	return strings.Join(parts, ", ")
}

// encodeSubject encodes the subject line if needed
func encodeSubject(subject string) string {
	// Check if encoding is needed
	needsEncoding := false
	for _, r := range subject {
		if r > 127 {
			needsEncoding = true
			break
		}
	}
	if needsEncoding {
		return mime.QEncoding.Encode("utf-8", subject)
	}
	return subject
}

// writeQuotedPrintable writes content using quoted-printable encoding
func writeQuotedPrintable(w io.Writer, content string) {
	qpWriter := quotedprintable.NewWriter(w)
	qpWriter.Write([]byte(content))
	qpWriter.Close()
}

// writeMultipartAlternative writes a multipart/alternative message
func writeMultipartAlternative(w *bytes.Buffer, textBody, htmlBody string) error {
	mpWriter := multipart.NewWriter(w)
	boundary := mpWriter.Boundary()

	writeHeader(w, "Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	w.WriteString("\r\n")

	// Write plain text part
	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	textHeader.Set("Content-Transfer-Encoding", "quoted-printable")

	textPart, err := mpWriter.CreatePart(textHeader)
	if err != nil {
		return err
	}
	writeQuotedPrintable(textPart, textBody)

	// Write HTML part
	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")

	htmlPart, err := mpWriter.CreatePart(htmlHeader)
	if err != nil {
		return err
	}
	writeQuotedPrintable(htmlPart, htmlBody)

	return mpWriter.Close()
}

// writeMultipartMixed writes a multipart/mixed message with attachments
func writeMultipartMixed(w *bytes.Buffer, m *ComposeMessage, attachments, inlineAttachments []Attachment) error {
	mpWriter := multipart.NewWriter(w)
	boundary := mpWriter.Boundary()

	writeHeader(w, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", boundary))
	w.WriteString("\r\n")

	hasHTML := m.HTMLBody != ""
	hasText := m.TextBody != ""

	if hasHTML && hasText {
		// Create multipart/alternative nested inside the mixed section.
		// The altWriter MUST write to bodyPart (not w) so its boundaries
		// are properly nested inside the mixed boundary.
		altBoundary := uuid.New().String()
		altHeader := textproto.MIMEHeader{}
		altHeader.Set("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", altBoundary))

		bodyPart, err := mpWriter.CreatePart(altHeader)
		if err != nil {
			return err
		}

		altWriter := multipart.NewWriter(bodyPart)
		if err := altWriter.SetBoundary(altBoundary); err != nil {
			return err
		}

		// Plain text alternative
		textHeader := textproto.MIMEHeader{}
		textHeader.Set("Content-Type", "text/plain; charset=utf-8")
		textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		textPart, err := altWriter.CreatePart(textHeader)
		if err != nil {
			return err
		}
		writeQuotedPrintable(textPart, m.TextBody)

		// HTML alternative (with optional inline attachments)
		if len(inlineAttachments) > 0 {
			if err := writeRelatedPart(altWriter, m.HTMLBody, inlineAttachments); err != nil {
				return err
			}
		} else {
			htmlHeader := textproto.MIMEHeader{}
			htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
			htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
			htmlPart, err := altWriter.CreatePart(htmlHeader)
			if err != nil {
				return err
			}
			writeQuotedPrintable(htmlPart, m.HTMLBody)
		}

		if err := altWriter.Close(); err != nil {
			return err
		}
	} else if hasHTML {
		if len(inlineAttachments) > 0 {
			if err := writeRelatedPart(mpWriter, m.HTMLBody, inlineAttachments); err != nil {
				return err
			}
		} else {
			htmlHeader := textproto.MIMEHeader{}
			htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
			htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
			bodyPart, err := mpWriter.CreatePart(htmlHeader)
			if err != nil {
				return err
			}
			writeQuotedPrintable(bodyPart, m.HTMLBody)
		}
	} else if hasText {
		textHeader := textproto.MIMEHeader{}
		textHeader.Set("Content-Type", "text/plain; charset=utf-8")
		textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		bodyPart, err := mpWriter.CreatePart(textHeader)
		if err != nil {
			return err
		}
		writeQuotedPrintable(bodyPart, m.TextBody)
	}

	// Write regular attachments
	for _, att := range attachments {
		if err := writeAttachment(mpWriter, att); err != nil {
			return err
		}
	}

	return mpWriter.Close()
}

// writeRelatedPart creates a multipart/related part inside a parent multipart writer,
// containing HTML and inline attachments with proper MIME headers.
func writeRelatedPart(parentWriter *multipart.Writer, htmlBody string, inlineAttachments []Attachment) error {
	relBoundary := uuid.New().String()
	relHeader := textproto.MIMEHeader{}
	relHeader.Set("Content-Type", fmt.Sprintf("multipart/related; boundary=%q", relBoundary))

	relPart, err := parentWriter.CreatePart(relHeader)
	if err != nil {
		return err
	}

	relWriter := multipart.NewWriter(relPart)
	if err := relWriter.SetBoundary(relBoundary); err != nil {
		return err
	}

	// HTML sub-part
	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := relWriter.CreatePart(htmlHeader)
	if err != nil {
		return err
	}
	writeQuotedPrintable(htmlPart, htmlBody)

	// Inline attachments
	for _, att := range inlineAttachments {
		if err := writeInlineAttachment(relWriter, att); err != nil {
			return err
		}
	}

	return relWriter.Close()
}

// writeAttachment writes a single attachment
func writeAttachment(w *multipart.Writer, att Attachment) error {
	contentType := att.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))

	part, err := w.CreatePart(header)
	if err != nil {
		return err
	}

	content, err := att.ResolveContent()
	if err != nil {
		return fmt.Errorf("failed to resolve attachment content: %w", err)
	}

	// Write base64 encoded content
	encoder := base64.NewEncoder(base64.StdEncoding, &base64LineWrapper{Writer: part})
	_, err = encoder.Write(content)
	if err != nil {
		return err
	}
	return encoder.Close()
}

// writeInlineAttachment writes an inline attachment (for HTML images)
func writeInlineAttachment(w *multipart.Writer, att Attachment) error {
	contentType := att.ContentType
	if contentType == "" {
		// Try to guess from filename
		ext := strings.ToLower(filepath.Ext(att.Filename))
		switch ext {
		case ".png":
			contentType = "image/png"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		default:
			contentType = "application/octet-stream"
		}
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", att.Filename))
	if att.ContentID != "" {
		header.Set("Content-ID", fmt.Sprintf("<%s>", att.ContentID))
	}

	part, err := w.CreatePart(header)
	if err != nil {
		return err
	}

	content, err := att.ResolveContent()
	if err != nil {
		return fmt.Errorf("failed to resolve inline attachment content: %w", err)
	}

	// Write base64 encoded content
	encoder := base64.NewEncoder(base64.StdEncoding, &base64LineWrapper{Writer: part})
	_, err = encoder.Write(content)
	if err != nil {
		return err
	}
	return encoder.Close()
}

// base64LineWrapper wraps base64 output at 76 characters per line
type base64LineWrapper struct {
	Writer  io.Writer
	lineLen int
}

func (w *base64LineWrapper) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		// Calculate how much we can write before needing a line break
		remaining := 76 - w.lineLen
		if remaining <= 0 {
			if _, err := w.Writer.Write([]byte("\r\n")); err != nil {
				return n, err
			}
			w.lineLen = 0
			remaining = 76
		}

		toWrite := len(p)
		if toWrite > remaining {
			toWrite = remaining
		}

		written, err := w.Writer.Write(p[:toWrite])
		n += written
		w.lineLen += written
		if err != nil {
			return n, err
		}

		p = p[toWrite:]
	}
	return n, nil
}
