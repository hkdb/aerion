// Package email provides email content processing utilities
package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"path/filepath"
	"strings"

	gomessage "github.com/emersion/go-message"
	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/message"
	"github.com/teamwork/tnef"
)

// AttachmentExtractor extracts attachment metadata and content from emails
type AttachmentExtractor struct{}

// NewAttachmentExtractor creates a new attachment extractor
func NewAttachmentExtractor() *AttachmentExtractor {
	return &AttachmentExtractor{}
}

// FallbackFilename generates a filename for a MIME part that declared none,
// derived from its content type. Every site that names or re-locates
// nameless parts (sync-time extraction in sync/parse.go, this extractor,
// and the downloader's part matching in download.go) MUST use this one
// function — the downloader finds parts by comparing the STORED filename
// against the same synthesis, so divergent fallbacks break Save/Open of
// nameless attachments (#370).
func FallbackFilename(contentType string) string {
	// Outlook/Teams meeting invites: a recognizable, openable name
	// instead of attachment.bin (#370)
	if contentType == "text/calendar" {
		return "invite.ics"
	}
	// Keep the historical image/<subtype> naming exactly (ExtensionsByType
	// could pick a different-but-valid extension per platform)
	if strings.HasPrefix(contentType, "image/") {
		parts := strings.SplitN(contentType, "/", 2)
		if len(parts) == 2 && parts[1] != "" {
			return "attachment." + parts[1]
		}
	}
	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		return "attachment" + exts[0]
	}
	return "attachment.bin"
}

// AttachmentData holds both metadata and content for an attachment
type AttachmentData struct {
	Attachment *message.Attachment
	Content    []byte
}

// ExtractAttachments extracts all attachments from a raw email message
func (e *AttachmentExtractor) ExtractAttachments(messageID string, raw []byte) ([]*AttachmentData, error) {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	var attachments []*AttachmentData

	// Check if it's a multipart message
	if mr := entity.MultipartReader(); mr != nil {
		attachments = e.extractFromMultipart(messageID, mr)
	}

	return attachments, nil
}

// extractFromMultipart extracts attachments from a multipart message
func (e *AttachmentExtractor) extractFromMultipart(messageID string, mr gomessage.MultipartReader) []*AttachmentData {
	var attachments []*AttachmentData

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		contentType, params, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		disposition, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		contentID := strings.Trim(part.Header.Get("Content-ID"), "<>")

		// Handle nested multipart
		if strings.HasPrefix(contentType, "multipart/") {
			if nestedMr := part.MultipartReader(); nestedMr != nil {
				nested := e.extractFromMultipart(messageID, nestedMr)
				attachments = append(attachments, nested...)
			}
			continue
		}

		// Check for TNEF (winmail.dat)
		if contentType == "application/ms-tnef" ||
			(disposition == "attachment" && strings.EqualFold(dispParams["filename"], "winmail.dat")) {
			tnefAttachments := e.extractFromTNEF(messageID, part.Body)
			attachments = append(attachments, tnefAttachments...)
			continue
		}

		// Determine if this is an attachment
		isAttachment := disposition == "attachment"
		isInline := disposition == "inline" || contentID != ""

		// Skip text/plain and text/html unless they're explicit attachments
		if !isAttachment && (contentType == "text/plain" || contentType == "text/html") {
			continue
		}

		// If it's not text and has content, treat it as an attachment
		if isAttachment || isInline || (!strings.HasPrefix(contentType, "text/") && contentType != "") {
			// Get filename
			filename := dispParams["filename"]
			if filename == "" {
				filename = params["name"]
			}
			if filename == "" {
				filename = FallbackFilename(contentType)
			}

			// Decode filename if encoded
			decodedFilename, err := decodeRFC2047(filename)
			if err == nil {
				filename = decodedFilename
			}

			// Read content
			content, err := io.ReadAll(part.Body)
			if err != nil {
				continue
			}

			// Decode content if transfer-encoded
			transferEncoding := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
			decodedContent := decodeContent(content, transferEncoding)

			att := &message.Attachment{
				ID:          uuid.New().String(),
				MessageID:   messageID,
				Filename:    filename,
				ContentType: contentType,
				Size:        len(decodedContent),
				ContentID:   contentID,
				IsInline:    isInline && contentID != "",
			}

			attachments = append(attachments, &AttachmentData{
				Attachment: att,
				Content:    decodedContent,
			})
		}
	}

	return attachments
}

// TNEFAttachment is a single attachment decoded from a TNEF (winmail.dat) container.
type TNEFAttachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// DecodeTNEFAttachments decodes a TNEF (winmail.dat) container into its inner
// attachments, returning nil if the bytes are not valid TNEF. This is the single
// source of TNEF decoding shared by the sync extractor, the on-demand extractor,
// and the downloader, so the three paths can never diverge on filename/type — the
// invariant that lets sync store a name the downloader can later resolve.
func DecodeTNEFAttachments(data []byte) []TNEFAttachment {
	tnefData, err := tnef.Decode(data)
	if err != nil {
		return nil
	}

	var out []TNEFAttachment
	for _, att := range tnefData.Attachments {
		filename := att.Title
		if filename == "" {
			filename = "attachment"
		}

		// Guess content type from the filename extension.
		contentType := "application/octet-stream"
		if guessed := mime.TypeByExtension(filepath.Ext(filename)); guessed != "" {
			contentType = guessed
		}

		out = append(out, TNEFAttachment{
			Filename:    filename,
			ContentType: contentType,
			Content:     att.Data,
		})
	}

	return out
}

// extractFromTNEF extracts attachments from a TNEF (winmail.dat) file
func (e *AttachmentExtractor) extractFromTNEF(messageID string, reader io.Reader) []*AttachmentData {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}

	var attachments []*AttachmentData
	for _, tnefAtt := range DecodeTNEFAttachments(data) {
		attachments = append(attachments, &AttachmentData{
			Attachment: &message.Attachment{
				ID:          uuid.New().String(),
				MessageID:   messageID,
				Filename:    tnefAtt.Filename,
				ContentType: tnefAtt.ContentType,
				Size:        len(tnefAtt.Content),
				IsInline:    false,
			},
			Content: tnefAtt.Content,
		})
	}

	return attachments
}

// decodeContent decodes content based on transfer encoding
func decodeContent(content []byte, encoding string) []byte {
	switch encoding {
	case "base64":
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(content)))
		n, err := base64.StdEncoding.Decode(decoded, content)
		if err != nil {
			return content
		}
		return decoded[:n]
	case "quoted-printable":
		reader := quotedprintable.NewReader(bytes.NewReader(content))
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return content
		}
		return decoded
	default:
		return content
	}
}

// decodeRFC2047 decodes RFC 2047 encoded strings (like filenames)
func decodeRFC2047(s string) (string, error) {
	dec := new(mime.WordDecoder)
	return dec.DecodeHeader(s)
}

