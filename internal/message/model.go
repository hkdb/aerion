// Package message provides message management functionality
package message

import (
	"time"
)

// Message represents an email message
type Message struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`

	// IMAP identifiers
	UID       uint32 `json:"uid"`
	MessageID string `json:"messageId,omitempty"`

	// Threading
	InReplyTo  string `json:"inReplyTo,omitempty"`
	References string `json:"references,omitempty"` // JSON array of Message-IDs
	ThreadID   string `json:"threadId,omitempty"`

	// Envelope data
	Subject   string    `json:"subject"`
	FromName  string    `json:"fromName"`
	FromEmail string    `json:"fromEmail"`
	ToList    string    `json:"toList,omitempty"`  // JSON array
	CcList    string    `json:"ccList,omitempty"`  // JSON array
	BccList   string    `json:"bccList,omitempty"` // JSON array
	ReplyTo   string    `json:"replyTo,omitempty"`
	Date      time.Time `json:"date"`

	// Preview
	Snippet string `json:"snippet,omitempty"`

	// Flags
	IsRead      bool `json:"isRead"`
	IsStarred   bool `json:"isStarred"`
	IsAnswered  bool `json:"isAnswered"`
	IsForwarded bool `json:"isForwarded"`
	IsDraft     bool `json:"isDraft"`
	IsDeleted   bool `json:"isDeleted"`

	// Size and attachments
	Size           int  `json:"size"`
	HasAttachments bool `json:"hasAttachments"`

	// Body (may be empty until fetched)
	BodyText    string `json:"bodyText,omitempty"`
	BodyHTML    string `json:"bodyHtml,omitempty"`
	BodyFetched bool   `json:"bodyFetched"` // Whether full body has been downloaded

	// Read receipt
	ReadReceiptTo      string `json:"readReceiptTo,omitempty"` // Email requesting receipt (from Disposition-Notification-To header)
	ReadReceiptHandled bool   `json:"readReceiptHandled"`      // Whether user has responded (sent or ignored)

	// S/MIME status (empty = not S/MIME)
	SMIMEStatus        string `json:"smimeStatus,omitempty"`
	SMIMESignerEmail   string `json:"smimeSignerEmail,omitempty"`
	SMIMESignerSubject string `json:"smimeSignerSubject,omitempty"`

	// S/MIME encryption
	SMIMEEncrypted bool `json:"smimeEncrypted,omitempty"` // Whether the message is encrypted
	HasSMIME       bool `json:"hasSMIME,omitempty"`       // Computed: smime_raw_body IS NOT NULL

	// PGP status (empty = not PGP)
	PGPStatus      string `json:"pgpStatus,omitempty"`
	PGPSignerEmail string `json:"pgpSignerEmail,omitempty"`
	PGPSignerKeyID string `json:"pgpSignerKeyId,omitempty"`

	// PGP encryption
	PGPEncrypted bool `json:"pgpEncrypted,omitempty"` // Whether the message is PGP encrypted
	HasPGP       bool `json:"hasPGP,omitempty"`       // Computed: pgp_raw_body IS NOT NULL

	// Timestamps
	ReceivedAt time.Time `json:"receivedAt"`
}

// Address represents an email address
type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// MessageHeader contains just the header/envelope data (for list views)
type MessageHeader struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`
	UID       uint32 `json:"uid"`

	Subject   string    `json:"subject"`
	FromName  string    `json:"fromName"`
	FromEmail string    `json:"fromEmail"`
	Date      time.Time `json:"date"`
	Snippet   string    `json:"snippet"`

	IsRead         bool `json:"isRead"`
	IsStarred      bool `json:"isStarred"`
	HasAttachments bool `json:"hasAttachments"`
}

// ToHeader returns a MessageHeader from a Message
func (m *Message) ToHeader() *MessageHeader {
	return &MessageHeader{
		ID:             m.ID,
		AccountID:      m.AccountID,
		FolderID:       m.FolderID,
		UID:            m.UID,
		Subject:        m.Subject,
		FromName:       m.FromName,
		FromEmail:      m.FromEmail,
		Date:           m.Date,
		Snippet:        m.Snippet,
		IsRead:         m.IsRead,
		IsStarred:      m.IsStarred,
		HasAttachments: m.HasAttachments,
	}
}

// Conversation represents a group of related messages (thread)
type Conversation struct {
	ThreadID       string     `json:"threadId"`
	Subject        string     `json:"subject"`
	Snippet        string     `json:"snippet"`
	MessageCount   int        `json:"messageCount"`
	UnreadCount    int        `json:"unreadCount"`
	HasAttachments bool       `json:"hasAttachments"`
	IsStarred      bool       `json:"isStarred"`
	LatestDate     time.Time  `json:"latestDate"`
	Participants   []Address  `json:"participants"`
	MessageIDs     []string   `json:"messageIds"`         // Message IDs for context menu actions
	IsEncrypted    bool       `json:"isEncrypted"`        // Any message in thread is encrypted (S/MIME or PGP)
	Messages       []*Message `json:"messages,omitempty"` // Only populated when fetching full conversation

	// For unified inbox view - populated when querying across accounts
	AccountID    string `json:"accountId,omitempty"`
	AccountName  string `json:"accountName,omitempty"`
	AccountColor string `json:"accountColor,omitempty"`
	FolderID     string `json:"folderId,omitempty"` // The inbox folder ID for this conversation
}

// Attachment represents an email attachment
type Attachment struct {
	ID          string `json:"id"`
	MessageID   string `json:"messageId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	ContentID   string `json:"contentId,omitempty"` // For inline attachments
	IsInline    bool   `json:"isInline"`
	LocalPath   string `json:"localPath,omitempty"` // Path to downloaded file
	Content     []byte `json:"-"`                   // Raw content for inline attachments (not serialized to JSON)
}

// FetchOptions specifies which parts of a message to fetch
type FetchOptions struct {
	Envelope    bool
	Flags       bool
	BodyText    bool
	BodyHTML    bool
	Attachments bool
}

// DefaultFetchOptions returns options for fetching message headers only
func DefaultFetchOptions() FetchOptions {
	return FetchOptions{
		Envelope: true,
		Flags:    true,
	}
}

// FullFetchOptions returns options for fetching the complete message
func FullFetchOptions() FetchOptions {
	return FetchOptions{
		Envelope:    true,
		Flags:       true,
		BodyText:    true,
		BodyHTML:    true,
		Attachments: true,
	}
}

// ConversationSearchResult extends Conversation with search-specific fields
// including highlighted text and folder information for search results display
type ConversationSearchResult struct {
	Conversation               // Embed the base Conversation
	HighlightedSubject  string `json:"highlightedSubject"`  // Subject with <mark> tags around matches
	HighlightedSnippet  string `json:"highlightedSnippet"`  // Snippet with <mark> tags around matches
	HighlightedFromName string `json:"highlightedFromName"` // From name with <mark> tags around matches
	FolderName          string `json:"folderName"`          // Folder name for display in search results
	FolderType          string `json:"folderType"`          // Folder type for icon selection
}

// ParticipantSuggestion is a person inferred from message history for account-scoped search suggestions.
type ParticipantSuggestion struct {
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

// FTSIndexStatus represents the indexing status for a folder
type FTSIndexStatus struct {
	FolderID      string `json:"folderId"`
	IndexedCount  int    `json:"indexedCount"`
	TotalCount    int    `json:"totalCount"`
	IsComplete    bool   `json:"isComplete"`
	LastIndexedAt string `json:"lastIndexedAt,omitempty"` // ISO 8601 timestamp
}
