package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/pgp"
	"github.com/hkdb/aerion/internal/smime"
)

// ============================================================================
// Message API - Exposed to frontend via Wails bindings
// ============================================================================

// GetMessages returns messages for a folder with pagination
func (a *App) GetMessages(accountID, folderID string, offset, limit int) ([]*message.MessageHeader, error) {
	return a.messageStore.ListByFolder(folderID, offset, limit)
}

// GetMessageCount returns the total message count for a folder
func (a *App) GetMessageCount(accountID, folderID string) (int, error) {
	return a.messageStore.CountByFolder(folderID)
}

// GetMessage returns a full message by ID
func (a *App) GetMessage(id string) (*message.Message, error) {
	return a.messageStore.Get(id)
}

// GetMessageSource fetches the raw RFC822 source of a message from the IMAP server
func (a *App) GetMessageSource(messageID string) (string, error) {
	log := logging.WithComponent("app")
	log.Debug().Str("messageID", messageID).Msg("Fetching message source")

	// Get the message to find the account, folder, and UID
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return "", fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return "", fmt.Errorf("message not found: %s", messageID)
	}

	// Fetch raw message from IMAP
	rawBytes, err := a.syncEngine.FetchRawMessage(a.ctx, msg.AccountID, msg.FolderID, msg.UID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch message source: %w", err)
	}

	return string(rawBytes), nil
}

// FetchMessageBody fetches the body for a message on-demand.
// This is called when a message's body hasn't been fetched yet (BodyFetched = false).
// It fetches the body from IMAP, updates the database, and returns the updated message.
func (a *App) FetchMessageBody(messageID string) (*message.Message, error) {
	log := logging.WithComponent("app")
	log.Debug().Str("messageID", messageID).Msg("Fetching message body on-demand")

	// Get the message first to get the account ID
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	// If body is already fetched, just return it
	if msg.BodyFetched {
		return msg, nil
	}

	// Fetch the body from IMAP
	updatedMsg, err := a.syncEngine.FetchMessageBody(a.ctx, msg.AccountID, messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch message body: %w", err)
	}

	return updatedMsg, nil
}

// GetConversations returns conversations (threaded messages) for a folder with pagination
// sortOrder can be "newest" (default) or "oldest"
// filter can be "" (all), "unread", "starred", or "attachments"
func (a *App) GetConversations(accountID, folderID string, offset, limit int, sortOrder, filter string) ([]*message.Conversation, error) {
	start := time.Now()
	convs, err := a.messageStore.ListConversationsByFolder(folderID, offset, limit, sortOrder, filter)
	// List-query latency early vs. late in a session is the "it gets slow"
	// signal; limit growth here is the reload-size ratchet.
	log := logging.WithComponent("app")
	log.Debug().
		Str("folder", folderID).
		Int("offset", offset).
		Int("limit", limit).
		Int("returned", len(convs)).
		Dur("elapsed", time.Since(start)).
		Msg("GetConversations")
	return convs, err
}

// GetConversationCount returns the total conversation count for a folder
func (a *App) GetConversationCount(accountID, folderID, filter string) (int, error) {
	return a.messageStore.CountConversationsByFolder(folderID, filter)
}

// GetUnifiedInboxConversations returns conversations from all inbox folders across all accounts
func (a *App) GetUnifiedInboxConversations(offset, limit int, sortOrder, filter string) ([]*message.Conversation, error) {
	start := time.Now()
	convs, err := a.messageStore.ListConversationsUnifiedInbox(offset, limit, sortOrder, filter)
	log := logging.WithComponent("app")
	log.Debug().
		Int("offset", offset).
		Int("limit", limit).
		Int("returned", len(convs)).
		Dur("elapsed", time.Since(start)).
		Msg("GetUnifiedInboxConversations")
	return convs, err
}

// GetUnifiedInboxCount returns the total conversation count across all inbox folders
func (a *App) GetUnifiedInboxCount(filter string) (int, error) {
	return a.messageStore.CountConversationsUnifiedInbox(filter)
}

// GetUnifiedInboxUnreadCount returns the total unread count across all inbox folders
func (a *App) GetUnifiedInboxUnreadCount() (int, error) {
	return a.messageStore.GetUnifiedInboxUnreadCount()
}

// GetConversation returns all messages in a conversation/thread
func (a *App) GetConversation(threadID, folderID string) (*message.Conversation, error) {
	log := logging.WithComponent("app")
	log.Debug().
		Str("threadID", threadID).
		Str("folderID", folderID).
		Msg("GetConversation called")

	conv, err := a.messageStore.GetConversation(threadID, folderID)
	if err != nil {
		log.Error().Err(err).Msg("GetConversation failed")
		return nil, err
	}

	if conv != nil && conv.Messages != nil {
		for i, m := range conv.Messages {
			log.Debug().
				Int("index", i).
				Str("messageID", m.ID).
				Str("subject", m.Subject).
				Int("bodyTextLen", len(m.BodyText)).
				Int("bodyHTMLLen", len(m.BodyHTML)).
				Str("threadID", m.ThreadID).
				Msg("GetConversation message")
		}
	} else {
		log.Debug().Msg("GetConversation returned nil or no messages")
	}

	return conv, nil
}

// DecryptedAttachment holds metadata for an attachment extracted from an encrypted message.
// Content is never stored in DB — only returned in-memory for frontend display.
type DecryptedAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	IsInline    bool   `json:"isInline"`
	ContentID   string `json:"contentId"`
}

// SMIMEViewResult holds the on-view S/MIME processing result for the frontend
type SMIMEViewResult struct {
	BodyHTML           string                `json:"bodyHtml"`
	BodyText           string                `json:"bodyText"`
	SMIMEStatus        string                `json:"smimeStatus"`
	SMIMESignerEmail   string                `json:"smimeSignerEmail"`
	SMIMESignerSubject string                `json:"smimeSignerSubject"`
	SMIMEEncrypted     bool                  `json:"smimeEncrypted"`
	InlineAttachments  map[string]string     `json:"inlineAttachments,omitempty"` // contentID → dataURL
	Attachments        []DecryptedAttachment `json:"attachments,omitempty"`       // metadata for attachment list
}

// ProcessSMIMEMessage decrypts and/or verifies an S/MIME message on-view.
// Returns the plaintext body and signature status, computed fresh each time.
func (a *App) ProcessSMIMEMessage(messageID string) (*SMIMEViewResult, error) {
	log := logging.WithComponent("app")

	// Load message metadata to get accountID and encryption flag
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	// Determine the recipient identity email for targeted decryption
	recipientEmail := a.findRecipientIdentityEmail(msg)

	// Load raw S/MIME body
	rawBody, err := a.messageStore.GetSMIMERawBody(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get S/MIME raw body: %w", err)
	}
	if rawBody == nil {
		return nil, fmt.Errorf("no S/MIME raw body for message: %s", messageID)
	}

	result := &SMIMEViewResult{}
	innerBytes := rawBody

	// Step 1: Decrypt if encrypted
	if msg.SMIMEEncrypted {
		decrypted, isEncrypted, decErr := a.smimeDecryptor.DecryptMessage(msg.AccountID, recipientEmail, rawBody)
		if decErr != nil {
			log.Warn().Err(decErr).Str("messageID", messageID).Msg("S/MIME decryption failed")
			return &SMIMEViewResult{
				SMIMEEncrypted: true,
				SMIMEStatus:    "decrypt_failed",
			}, nil
		}
		if isEncrypted {
			result.SMIMEEncrypted = true
			innerBytes = decrypted
		}
	}

	// Step 2: Verify if the inner content is signed
	var sigResult *smime.SignatureResult
	ct := extractContentType(innerBytes)
	if smime.IsSMIMESigned(ct) {
		sigResult, innerBytes = a.smimeVerifier.VerifyAndUnwrap(innerBytes)
		if innerBytes == nil {
			// Verification unwrap failed, use the encrypted content as-is
			innerBytes = rawBody
		}
	}

	// Step 3: Set signature status
	if sigResult != nil {
		result.SMIMEStatus = string(sigResult.Status)
		result.SMIMESignerEmail = sigResult.SignerEmail
		result.SMIMESignerSubject = sigResult.SignerName
	}

	// Step 4: Parse the final body using the sync engine's parser (includes attachments)
	parsed := a.syncEngine.ParseDecryptedBody(innerBytes, messageID)
	result.BodyHTML = parsed.BodyHTML
	result.BodyText = parsed.BodyText

	// Step 5: Build inline attachment map and attachment list from decrypted content
	result.InlineAttachments = buildInlineAttachmentMap(parsed.Attachments)
	result.Attachments = buildDecryptedAttachmentList(parsed.Attachments)

	return result, nil
}

// extractContentType extracts the Content-Type header value from raw message bytes.
// Handles multi-line (folded) headers.
func extractContentType(raw []byte) string {
	headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	if headerEnd == -1 {
		headerEnd = bytes.Index(raw, []byte("\n\n"))
	}
	if headerEnd == -1 {
		return ""
	}

	headers := string(raw[:headerEnd])
	lines := strings.Split(headers, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(strings.ToLower(line), "content-type:") {
			continue
		}
		value := strings.TrimSpace(line[len("content-type:"):])
		// Collect continuation lines
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimRight(lines[j], "\r")
			if len(next) == 0 || (next[0] != ' ' && next[0] != '\t') {
				break
			}
			value += " " + strings.TrimSpace(next)
		}
		return value
	}
	return ""
}

// PGPViewResult holds the on-view PGP processing result for the frontend
type PGPViewResult struct {
	BodyHTML          string                `json:"bodyHtml"`
	BodyText          string                `json:"bodyText"`
	PGPStatus         string                `json:"pgpStatus"`
	PGPSignerEmail    string                `json:"pgpSignerEmail"`
	PGPSignerKeyID    string                `json:"pgpSignerKeyId"`
	PGPEncrypted      bool                  `json:"pgpEncrypted"`
	InlineAttachments map[string]string     `json:"inlineAttachments,omitempty"` // contentID → dataURL
	Attachments       []DecryptedAttachment `json:"attachments,omitempty"`       // metadata for attachment list
}

// ProcessPGPMessage decrypts and/or verifies a PGP message on-view.
// Returns the plaintext body and signature status, computed fresh each time.
func (a *App) ProcessPGPMessage(messageID string) (*PGPViewResult, error) {
	log := logging.WithComponent("app")

	// Load message metadata to get accountID and encryption flag
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	// Determine the recipient identity email for targeted decryption
	recipientEmail := a.findRecipientIdentityEmail(msg)

	// Load raw PGP body
	rawBody, err := a.messageStore.GetPGPRawBody(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PGP raw body: %w", err)
	}
	if rawBody == nil {
		return nil, fmt.Errorf("no PGP raw body for message: %s", messageID)
	}

	result := &PGPViewResult{}
	innerBytes := rawBody

	// Step 1: Decrypt if encrypted
	if msg.PGPEncrypted {
		decrypted, isEncrypted, decErr := a.pgpDecryptor.DecryptMessage(msg.AccountID, recipientEmail, rawBody)
		if decErr != nil {
			log.Warn().Err(decErr).Str("messageID", messageID).Msg("PGP decryption failed")
			return &PGPViewResult{
				PGPEncrypted: true,
				PGPStatus:    "decrypt_failed",
			}, nil
		}
		if isEncrypted {
			result.PGPEncrypted = true
			innerBytes = decrypted
		}
	}

	// Step 2: Verify if the inner content is signed
	var sigResult *pgp.SignatureResult
	ct := extractContentType(innerBytes)
	if pgp.IsPGPSigned(ct) {
		sigResult, innerBytes = a.pgpVerifier.VerifyAndUnwrap(innerBytes)
		if innerBytes == nil {
			// Verification unwrap failed, use the encrypted content as-is
			innerBytes = rawBody
		}
	}

	// Step 3: Set signature status
	if sigResult != nil {
		result.PGPStatus = string(sigResult.Status)
		result.PGPSignerEmail = sigResult.SignerEmail
		result.PGPSignerKeyID = sigResult.SignerKeyID
	}

	// Step 4: Parse the final body using the sync engine's parser (includes attachments)
	parsed := a.syncEngine.ParseDecryptedBody(innerBytes, messageID)
	result.BodyHTML = parsed.BodyHTML
	result.BodyText = parsed.BodyText

	// Step 5: Build inline attachment map and attachment list from decrypted content
	result.InlineAttachments = buildInlineAttachmentMap(parsed.Attachments)
	result.Attachments = buildDecryptedAttachmentList(parsed.Attachments)

	return result, nil
}

// buildInlineAttachmentMap builds a map of contentID → dataURL for inline attachments.
// Used to resolve cid: references in HTML bodies of encrypted messages.
func buildInlineAttachmentMap(atts []*message.Attachment) map[string]string {
	result := make(map[string]string)
	for _, att := range atts {
		if !att.IsInline || att.ContentID == "" || len(att.Content) == 0 {
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(att.Content)
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		result[att.ContentID] = "data:" + ct + ";base64," + b64
	}
	return result
}

// findRecipientIdentityEmail returns the identity email that was a recipient of the message.
// Used for targeted decryption — matches the message's To/Cc against the account's identities.
func (a *App) findRecipientIdentityEmail(msg *message.Message) string {
	identities, err := a.accountStore.GetIdentities(msg.AccountID)
	if err != nil || len(identities) == 0 {
		return ""
	}

	// Build a set of identity emails (lowercased)
	identityEmails := make(map[string]string) // lowercase -> original
	for _, id := range identities {
		identityEmails[strings.ToLower(strings.TrimSpace(id.Email))] = id.Email
	}

	// Check To, Cc, and Bcc lists for a matching identity
	for _, list := range []string{msg.ToList, msg.CcList, msg.BccList} {
		addrs := parseAddressList(list)
		for _, addr := range addrs {
			if email, ok := identityEmails[strings.ToLower(strings.TrimSpace(addr.Address))]; ok {
				return email
			}
		}
	}

	// Fall back to account email
	acc, err := a.accountStore.Get(msg.AccountID)
	if err == nil && acc != nil {
		return acc.Email
	}

	return ""
}

// buildDecryptedAttachmentList builds metadata for the frontend attachment list.
// Only includes non-inline attachments (regular file attachments).
func buildDecryptedAttachmentList(atts []*message.Attachment) []DecryptedAttachment {
	var result []DecryptedAttachment
	for _, att := range atts {
		if att.IsInline {
			continue
		}
		result = append(result, DecryptedAttachment{
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
			IsInline:    att.IsInline,
			ContentID:   att.ContentID,
		})
	}
	return result
}
