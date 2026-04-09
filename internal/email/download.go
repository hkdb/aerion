// Package email provides email content processing utilities
package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	gomessage "github.com/emersion/go-message"
	msgcharset "github.com/emersion/go-message/charset"
	"github.com/hkdb/aerion/internal/message"
	"golang.org/x/text/encoding/htmlindex"
)

// AttachmentDownloader handles downloading and saving attachments
type AttachmentDownloader struct {
	attachmentsDir string
}

// NewAttachmentDownloader creates a new attachment downloader
func NewAttachmentDownloader(attachmentsDir string) *AttachmentDownloader {
	return &AttachmentDownloader{
		attachmentsDir: attachmentsDir,
	}
}

// ExtractAttachmentContent extracts the content of a specific attachment from raw email bytes
func (d *AttachmentDownloader) ExtractAttachmentContent(raw []byte, targetFilename string) ([]byte, error) {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	// We need to find the attachment by matching properties
	if mr := entity.MultipartReader(); mr != nil {
		return d.findAttachmentInMultipart(mr, targetFilename)
	}

	return nil, fmt.Errorf("attachment not found: %s", targetFilename)
}

// InlineAttachmentResult holds content-id to data URL mapping
type InlineAttachmentResult struct {
	ContentID   string
	ContentType string
	Content     []byte
}

// ExtractInlineAttachments extracts all inline attachments from raw email bytes
// Returns a map of content-id to base64 data URL
func (d *AttachmentDownloader) ExtractInlineAttachments(raw []byte) (map[string]string, error) {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	result := make(map[string]string)

	if mr := entity.MultipartReader(); mr != nil {
		d.findInlineAttachmentsInMultipart(mr, result)
	}

	return result, nil
}

// findInlineAttachmentsInMultipart searches for inline attachments and builds data URLs
func (d *AttachmentDownloader) findInlineAttachmentsInMultipart(mr gomessage.MultipartReader, result map[string]string) {
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		// Handle nested multipart
		if nestedMr := part.MultipartReader(); nestedMr != nil {
			d.findInlineAttachmentsInMultipart(nestedMr, result)
			continue
		}

		// Check for Content-ID header (indicates inline attachment)
		contentID := strings.Trim(part.Header.Get("Content-ID"), "<>")
		if contentID == "" {
			continue
		}

		// Get content type
		contentType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// Read content
		content, err := io.ReadAll(part.Body)
		if err != nil {
			continue
		}

		// Decode content if transfer-encoded
		transferEncoding := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
		decodedContent := decodeContent(content, transferEncoding)

		// Build data URL
		dataURL := buildDataURL(contentType, decodedContent)
		result[contentID] = dataURL
	}
}

// buildDataURL creates a data URL from content type and binary content
func buildDataURL(contentType string, content []byte) string {
	encoded := base64.StdEncoding.EncodeToString(content)
	return fmt.Sprintf("data:%s;base64,%s", contentType, encoded)
}

// findAttachmentInMultipart searches for an attachment by filename in a multipart message
func (d *AttachmentDownloader) findAttachmentInMultipart(mr gomessage.MultipartReader, targetFilename string) ([]byte, error) {
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		// Handle nested multipart
		if nestedMr := part.MultipartReader(); nestedMr != nil {
			if content, err := d.findAttachmentInMultipart(nestedMr, targetFilename); err == nil {
				return content, nil
			}
			continue
		}

		// Check filename
		filename := getFilename(part)
		if filename == targetFilename {
			content, err := io.ReadAll(part.Body)
			if err != nil {
				return nil, err
			}

			// Decode content if transfer-encoded
			transferEncoding := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
			return decodeContent(content, transferEncoding), nil
		}
	}

	return nil, fmt.Errorf("attachment not found: %s", targetFilename)
}

// decodeMIMEFilename decodes a MIME-encoded filename with full charset support.
// Mirrors the sync code's decodeMIMEWord() to ensure filenames match between
// sync (when stored to DB) and download (when extracting from raw message).
func decodeMIMEFilename(s string) string {
	if s == "" {
		return s
	}
	dec := &mime.WordDecoder{
		CharsetReader: func(charsetName string, r io.Reader) (io.Reader, error) {
			if reader, err := msgcharset.Reader(charsetName, r); err == nil {
				return reader, nil
			}
			enc, err := htmlindex.Get(charsetName)
			if err != nil {
				return nil, fmt.Errorf("unknown charset: %s", charsetName)
			}
			return enc.NewDecoder().Reader(r), nil
		},
	}
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}

// getFilename extracts the filename from a message part
func getFilename(part *gomessage.Entity) string {
	// Try Content-Disposition first
	if disp := part.Header.Get("Content-Disposition"); disp != "" {
		_, params, _ := mime.ParseMediaType(disp)
		if filename := params["filename"]; filename != "" {
			return decodeMIMEFilename(filename)
		}
	}

	// Try Content-Type name parameter
	if ct := part.Header.Get("Content-Type"); ct != "" {
		_, params, _ := mime.ParseMediaType(ct)
		if name := params["name"]; name != "" {
			return decodeMIMEFilename(name)
		}
	}

	// Synthetic fallback: match sync/parse.go extractAttachmentMetadata logic
	contentType := "application/octet-stream"
	if ct := part.Header.Get("Content-Type"); ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		if mt != "" {
			contentType = mt
		}
	}

	ext := ".bin"
	if strings.HasPrefix(contentType, "image/") {
		parts := strings.SplitN(contentType, "/", 2)
		if len(parts) == 2 {
			ext = "." + parts[1]
		}
	}
	return "attachment" + ext
}

// SaveAttachment saves attachment content to disk
func (d *AttachmentDownloader) SaveAttachment(att *message.Attachment, content []byte, customPath string) (string, error) {
	var savePath string

	if customPath != "" {
		// Use custom path provided by user
		savePath = customPath
	} else {
		// Save to default attachments directory
		// Create subdirectory based on message ID for organization
		subDir := filepath.Join(d.attachmentsDir, att.MessageID[:8])
		if err := os.MkdirAll(subDir, 0700); err != nil {
			return "", fmt.Errorf("failed to create attachment directory: %w", err)
		}

		// Generate unique filename to avoid conflicts
		safeName := filepath.Base(att.Filename)
		savePath = filepath.Join(subDir, safeName)

		// If file exists, append a number
		if _, err := os.Stat(savePath); err == nil {
			ext := filepath.Ext(safeName)
			base := safeName[:len(safeName)-len(ext)]
			for i := 1; ; i++ {
				savePath = filepath.Join(subDir, fmt.Sprintf("%s_%d%s", base, i, ext))
				if _, err := os.Stat(savePath); os.IsNotExist(err) {
					break
				}
			}
		}
	}

	// Write content to file
	if err := os.WriteFile(savePath, content, 0600); err != nil {
		return "", fmt.Errorf("failed to write attachment: %w", err)
	}

	return savePath, nil
}
