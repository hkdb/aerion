package sync

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/message"
)

// applyFlagsToMessage sets boolean flag fields on a Message from IMAP flags
func applyFlagsToMessage(m *message.Message, flags []imap.Flag) {
	for _, flag := range flags {
		switch flag {
		case imap.FlagSeen:
			m.IsRead = true
		case imap.FlagFlagged:
			m.IsStarred = true
		case imap.FlagAnswered:
			m.IsAnswered = true
		case imap.FlagDraft:
			m.IsDraft = true
		case imap.FlagDeleted:
			m.IsDeleted = true
		case "$Forwarded", "\\Forwarded":
			m.IsForwarded = true
		}
	}
}

// applyEnvelopeToMessage sets envelope fields on a Message from an IMAP envelope
func applyEnvelopeToMessage(m *message.Message, envelope *imap.Envelope) {
	if envelope == nil {
		return
	}
	m.Subject = decodeMIMEWord(envelope.Subject)
	m.MessageID = envelope.MessageID
	if len(envelope.InReplyTo) > 0 {
		m.InReplyTo = envelope.InReplyTo[0]
	}
	m.Date = envelope.Date.UTC()

	// From
	if len(envelope.From) > 0 {
		m.FromName = decodeMIMEWord(envelope.From[0].Name)
		m.FromEmail = envelope.From[0].Addr()
	}

	// To
	if len(envelope.To) > 0 {
		m.ToList = addressListToJSON(envelope.To)
	}

	// Cc
	if len(envelope.Cc) > 0 {
		m.CcList = addressListToJSON(envelope.Cc)
	}

	// Reply-To
	if len(envelope.ReplyTo) > 0 {
		m.ReplyTo = envelope.ReplyTo[0].Addr()
	}
}

// addressListToJSON converts an address list to JSON
func addressListToJSON(addrs []imap.Address) string {
	type addr struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	list := make([]addr, len(addrs))
	for i, a := range addrs {
		list[i] = addr{
			Name:  decodeMIMEWord(a.Name),
			Email: a.Addr(),
		}
	}

	data, _ := json.Marshal(list)
	return string(data)
}

// generateSnippet creates a preview snippet from message body
func generateSnippet(body string, maxLen int) string {
	// Remove excessive whitespace
	lines := strings.Split(body, "\n")
	var parts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, ">") { // Skip quoted lines
			parts = append(parts, line)
		}
	}
	text := strings.Join(parts, " ")

	// Truncate
	if len(text) > maxLen {
		text = text[:maxLen] + "..."
	}

	return text
}

// stripHTMLTags removes HTML tags for generating snippets
func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
		} else if r == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// convertFolderType converts IMAP folder type to our folder type
func convertFolderType(t imapPkg.FolderType) folder.Type {
	switch t {
	case imapPkg.FolderTypeInbox:
		return folder.TypeInbox
	case imapPkg.FolderTypeSent:
		return folder.TypeSent
	case imapPkg.FolderTypeDrafts:
		return folder.TypeDrafts
	case imapPkg.FolderTypeTrash:
		return folder.TypeTrash
	case imapPkg.FolderTypeSpam:
		return folder.TypeSpam
	case imapPkg.FolderTypeArchive:
		return folder.TypeArchive
	case imapPkg.FolderTypeAll:
		return folder.TypeAll
	case imapPkg.FolderTypeStarred:
		return folder.TypeStarred
	default:
		return folder.TypeFolder
	}
}

// extractFolderName extracts the folder name from the full path
func extractFolderName(path, delimiter string) string {
	if delimiter == "" {
		return path
	}
	parts := strings.Split(path, delimiter)
	return parts[len(parts)-1]
}

// generateID generates a unique ID for attachments
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
