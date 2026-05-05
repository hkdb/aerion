// Package sync provides IMAP synchronization functionality
package sync

import (
	"context"
	"io"

	gomessage "github.com/emersion/go-message"
	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/email"
	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/pgp"
	"github.com/hkdb/aerion/internal/smime"
	"github.com/rs/zerolog"
)

func init() {
	// Don't let go-message decode charset - return raw bytes and we'll decode ourselves
	// This gives us full control over charset detection and handling of mislabeled encodings
	gomessage.CharsetReader = func(charsetName string, r io.Reader) (io.Reader, error) {
		// Just return the original reader - we'll handle charset conversion in decodeCharset()
		return r, nil
	}
}

// Batch sizes for incremental sync
const (
	headerBatchSize = 50 // Messages per batch for header fetch
)

// Body fetch batch limits (hybrid byte + count based, like Geary)
const (
	bodyBatchMaxBytes    = 512 * 1024 // 512KB max per batch (memory safety)
	bodyBatchMaxMessages = 50         // Never more than 50 messages per batch
	bodyBatchMinMessages = 1          // At least 1 message per batch (for oversized emails)
	bodyBatchQueryLimit  = 200        // Query more candidates to allow byte-based batching
)

// Size limits for reading to prevent memory exhaustion
const (
	maxPartSize          = 10 * 1024 * 1024 // 10MB max for a single MIME part
	maxMessageSize       = 50 * 1024 * 1024 // 50MB max for entire raw message
	maxInlineContentSize = 5 * 1024 * 1024  // 5MB max for inline image content (stored in DB)
)

// ParsedBody holds the result of parsing a message body, including attachments
type ParsedBody struct {
	BodyText       string
	BodyHTML       string
	HasAttachments bool
	Attachments    []*message.Attachment  // Extracted attachment metadata (content only for inline)
	SMIMEResult    *smime.SignatureResult // S/MIME verification result (nil if not S/MIME)
	SMIMERawBody   []byte                 // Raw S/MIME body for on-view processing
	SMIMEEncrypted bool                   // Whether the message is encrypted
	PGPRawBody     []byte                 // Raw PGP body for on-view processing
	PGPEncrypted   bool                   // Whether the message is PGP encrypted
	UnsafeContent  bool                   // True if message has non-compliant encoding
}

// Retry limits for error recovery
const (
	maxMessageRetries    = 3 // Max retries per message before giving up
	maxConnectionRetries = 3 // Max connection recovery attempts before aborting
)

// SyncProgress holds progress information for sync operations
type SyncProgress struct {
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`
	Fetched   int    `json:"fetched"`
	Total     int    `json:"total"`
	Phase     string `json:"phase"` // "headers" or "bodies"
}

// ProgressCallback is called with sync progress updates
type ProgressCallback func(progress SyncProgress)

// Engine handles synchronization between IMAP server and local storage
type Engine struct {
	pool             *imapPkg.Pool
	accountStore     *account.Store
	folderStore      *folder.Store
	messageStore     *message.Store
	attachmentStore  *message.AttachmentStore
	attachExtractor  *email.AttachmentExtractor
	sanitizer        *email.Sanitizer
	log              zerolog.Logger
	progressCallback ProgressCallback
	smimeVerifier    *smime.Verifier
	pgpVerifier      *pgp.Verifier
}

// NewEngine creates a new sync engine
func NewEngine(pool *imapPkg.Pool, accountStore *account.Store, folderStore *folder.Store, messageStore *message.Store, attachmentStore *message.AttachmentStore) *Engine {
	return &Engine{
		pool:            pool,
		accountStore:    accountStore,
		folderStore:     folderStore,
		messageStore:    messageStore,
		attachmentStore: attachmentStore,
		attachExtractor: email.NewAttachmentExtractor(),
		sanitizer:       email.NewSanitizer(),
		log:             logging.WithComponent("sync"),
	}
}

// GetPoolConnection acquires a connection from the IMAP connection pool.
// Caller must release with ReleasePoolConnection when done.
func (e *Engine) GetPoolConnection(ctx context.Context, accountID string) (*imapPkg.PooledConnection, error) {
	return e.pool.GetConnection(ctx, accountID)
}

// ReleasePoolConnection returns a connection to the pool.
func (e *Engine) ReleasePoolConnection(conn *imapPkg.PooledConnection) {
	e.pool.Release(conn)
}

// SetProgressCallback sets the callback function for progress updates
func (e *Engine) SetProgressCallback(callback ProgressCallback) {
	e.progressCallback = callback
}

// SetSMIMEVerifier sets the S/MIME verifier for signature verification during body parsing
func (e *Engine) SetSMIMEVerifier(verifier *smime.Verifier) {
	e.smimeVerifier = verifier
}

// SetPGPVerifier sets the PGP verifier for signature verification during body parsing
func (e *Engine) SetPGPVerifier(verifier *pgp.Verifier) {
	e.pgpVerifier = verifier
}

// ParseRawBody parses raw message bytes into body text/HTML.
// This is a convenience wrapper around ParseDecryptedBody for callers that only need text.
func (e *Engine) ParseRawBody(raw []byte) (bodyHTML, bodyText string) {
	parsed := e.ParseDecryptedBody(raw, "")
	return parsed.BodyHTML, parsed.BodyText
}

// ParseDecryptedBody parses raw message bytes (e.g. from a decrypted S/MIME or PGP envelope)
// and returns the full ParsedBody including attachments.
// This is used by the app layer for on-view processing of encrypted messages.
func (e *Engine) ParseDecryptedBody(raw []byte, messageID string) *ParsedBody {
	parsed := e.parseMessageBodyInternal(raw, messageID)

	if parsed.BodyHTML != "" && e.sanitizer != nil {
		parsed.BodyHTML = e.sanitizer.Sanitize(parsed.BodyHTML)
	}

	return parsed
}

// emitProgress sends progress updates if a callback is set
func (e *Engine) emitProgress(accountID, folderID string, fetched, total int, phase string) {
	if e.progressCallback != nil {
		e.progressCallback(SyncProgress{
			AccountID: accountID,
			FolderID:  folderID,
			Fetched:   fetched,
			Total:     total,
			Phase:     phase,
		})
	}
}
