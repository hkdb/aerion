package sync

import (
	"bytes"
	"encoding/json"
	"strings"

	gomessage "github.com/emersion/go-message"
	"github.com/hkdb/aerion/internal/message"
)

// computeThreadID determines the thread ID for a message
func (e *Engine) computeThreadID(accountID string, m *message.Message) string {
	// Parse references from JSON
	var references []string
	if m.References != "" {
		_ = json.Unmarshal([]byte(m.References), &references)
	}

	// Try to find existing thread
	threadID, err := e.messageStore.FindThreadID(accountID, m.MessageID, m.InReplyTo, references)
	if err != nil {
		e.log.Debug().Err(err).Msg("Error finding thread ID, using message ID")
		return m.MessageID
	}

	return threadID
}

// extractReferences extracts the References header from raw message bytes
func (e *Engine) extractReferences(raw []byte) []string {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return nil
	}

	refsHeader := entity.Header.Get("References")
	if refsHeader == "" {
		return nil
	}

	// References header contains space or newline-separated Message-IDs
	// Format: <msgid1> <msgid2> <msgid3>
	var refs []string
	// Split by whitespace and filter for valid message-ids
	parts := strings.Fields(refsHeader)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			refs = append(refs, part)
		}
	}

	return refs
}

// extractDispositionNotificationTo extracts the Disposition-Notification-To header
// This header indicates the sender is requesting a read receipt
func (e *Engine) extractDispositionNotificationTo(raw []byte) string {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return ""
	}

	dntHeader := entity.Header.Get("Disposition-Notification-To")
	if dntHeader == "" {
		return ""
	}

	// The header value is typically an email address, possibly with a name
	// e.g., "John Doe <john@example.com>" or just "john@example.com"
	return strings.TrimSpace(dntHeader)
}

// extractListUnsubscribe extracts the List-Unsubscribe (RFC 2369) and
// List-Unsubscribe-Post (RFC 8058) headers. Both are returned as raw,
// unfolded values; empty when absent.
func (e *Engine) extractListUnsubscribe(raw []byte) (header, post string) {
	entity, err := gomessage.Read(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(entity.Header.Get("List-Unsubscribe")),
		strings.TrimSpace(entity.Header.Get("List-Unsubscribe-Post"))
}
