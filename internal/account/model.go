// Package account provides account management functionality
package account

import (
	"time"
)

// SecurityType represents the connection security method
type SecurityType string

const (
	SecurityNone     SecurityType = "none"
	SecurityTLS      SecurityType = "tls"
	SecurityStartTLS SecurityType = "starttls"
)

// AuthType represents the authentication method
type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthOAuth2   AuthType = "oauth2"
)

// Account represents an email account configuration
type Account struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`

	// Shared mailbox support
	SharedMailboxParentID string `json:"sharedMailboxParentId,omitempty"` // Parent account ID (set for Microsoft shared mailboxes)

	// IMAP settings
	IMAPHost     string       `json:"imapHost"`
	IMAPPort     int          `json:"imapPort"`
	IMAPSecurity SecurityType `json:"imapSecurity"`

	// SMTP settings
	SMTPHost     string       `json:"smtpHost"`
	SMTPPort     int          `json:"smtpPort"`
	SMTPSecurity SecurityType `json:"smtpSecurity"`

	// NoOutgoingServer disables SMTP for this account. When true, the
	// SMTP host/port/security fields are ignored, no SMTP client is
	// started, and the composer hides this account (and its identities)
	// from the From dropdown. Used for receive-only accounts.
	NoOutgoingServer bool `json:"noOutgoingServer"`

	// SMTPUsername is the SMTP-specific username when the server requires
	// different credentials than IMAP. Empty (the zero-value default)
	// means SMTP reuses Username + the IMAP keyring password — identical
	// to pre-v0.3.0 behavior. Non-empty means SMTP authenticates with
	// this username + a separately-stored password keyed at
	// "<accountID>:smtp" in the keyring.
	SMTPUsername string `json:"smtpUsername"`

	// ReplyForwardIdentityID is the identity to pre-select in the composer
	// when replying to or forwarding a message received via this account.
	// Used only when NoOutgoingServer is true (receive-only accounts have
	// no sendable identity of their own). Empty falls back to the user's
	// default sending account, then to the first available identity.
	ReplyForwardIdentityID string `json:"replyForwardIdentityId"`

	// Authentication
	AuthType AuthType `json:"authType"`
	Username string   `json:"username"`

	// State
	Enabled    bool   `json:"enabled"`
	OrderIndex int    `json:"orderIndex"`
	Color      string `json:"color"` // Hex color for account identification in unified inbox

	// Sync settings
	SyncPeriodDays int  `json:"syncPeriodDays"`
	SyncInterval   int  `json:"syncInterval"`   // Minutes between polls (0 = manual only)
	SyncAllFolders     bool `json:"syncAllFolders"`     // Sync all folders instead of just subscribed ones
	SyncFoldersEnabled bool `json:"syncFoldersEnabled"` // User opted into folder sync management

	// Read receipt settings
	// Controls whether to request read receipts when sending emails
	// Values: "never" (default), "ask", "always"
	ReadReceiptRequestPolicy string `json:"readReceiptRequestPolicy"`

	// Folder mappings (empty = auto-detect)
	SentFolderPath    string `json:"sentFolderPath,omitempty"`
	DraftsFolderPath  string `json:"draftsFolderPath,omitempty"`
	TrashFolderPath   string `json:"trashFolderPath,omitempty"`
	SpamFolderPath    string `json:"spamFolderPath,omitempty"`
	ArchiveFolderPath string `json:"archiveFolderPath,omitempty"`
	AllMailFolderPath string `json:"allMailFolderPath,omitempty"`
	StarredFolderPath string `json:"starredFolderPath,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// GetFolderMapping returns the mapped folder path for a folder type, or empty string if not mapped
func (a *Account) GetFolderMapping(folderType string) string {
	switch folderType {
	case "sent":
		return a.SentFolderPath
	case "drafts":
		return a.DraftsFolderPath
	case "trash":
		return a.TrashFolderPath
	case "spam":
		return a.SpamFolderPath
	case "archive":
		return a.ArchiveFolderPath
	case "all":
		return a.AllMailFolderPath
	case "starred":
		return a.StarredFolderPath
	}
	return ""
}

// Identity represents a sender identity (alias) for an account
type Identity struct {
	ID            string `json:"id"`
	AccountID     string `json:"accountId"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	IsDefault     bool   `json:"isDefault"`
	SignatureHTML string `json:"signatureHtml,omitempty"`
	SignatureText string `json:"signatureText,omitempty"`

	// Signature behavior settings
	SignatureEnabled    bool   `json:"signatureEnabled"`    // Master toggle (default: true)
	SignatureForNew     bool   `json:"signatureForNew"`     // Append to new messages (default: true)
	SignatureForReply   bool   `json:"signatureForReply"`   // Append to replies (default: true)
	SignatureForForward bool   `json:"signatureForForward"` // Append to forwards (default: true)
	SignaturePlacement  string `json:"signaturePlacement"`  // "above" or "below" quoted text (default: "above")
	SignatureSeparator  bool   `json:"signatureSeparator"`  // Add "-- " before signature (default: false)

	OrderIndex int       `json:"orderIndex"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// IdentityConfig is used for creating/updating identities
type IdentityConfig struct {
	Email               string `json:"email"`
	Name                string `json:"name"`
	SignatureHTML       string `json:"signatureHtml,omitempty"`
	SignatureText       string `json:"signatureText,omitempty"`
	SignatureEnabled    bool   `json:"signatureEnabled"`
	SignatureForNew     bool   `json:"signatureForNew"`
	SignatureForReply   bool   `json:"signatureForReply"`
	SignatureForForward bool   `json:"signatureForForward"`
	SignaturePlacement  string `json:"signaturePlacement"`
	SignatureSeparator  bool   `json:"signatureSeparator"`
}

// Validate validates the identity configuration
func (c *IdentityConfig) Validate() error {
	if c.Email == "" {
		return ErrEmailRequired
	}
	if c.Name == "" {
		return ErrDisplayNameRequired
	}
	// Set defaults for placement
	if c.SignaturePlacement == "" {
		c.SignaturePlacement = "above"
	}
	if c.SignaturePlacement != "above" && c.SignaturePlacement != "below" {
		c.SignaturePlacement = "above"
	}
	return nil
}

// AccountConfig is used for creating/updating accounts
type AccountConfig struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"` // Name shown to email recipients
	Email       string `json:"email"`

	SharedMailboxParentID string `json:"sharedMailboxParentId,omitempty"`

	IMAPHost     string       `json:"imapHost"`
	IMAPPort     int          `json:"imapPort"`
	IMAPSecurity SecurityType `json:"imapSecurity"`

	SMTPHost     string       `json:"smtpHost"`
	SMTPPort     int          `json:"smtpPort"`
	SMTPSecurity SecurityType `json:"smtpSecurity"`

	// NoOutgoingServer marks this account as receive-only. SMTP fields
	// are ignored when true. See Account.NoOutgoingServer.
	NoOutgoingServer bool `json:"noOutgoingServer"`

	// SMTPUsername / SMTPPassword override IMAP credentials for SMTP AUTH
	// when SMTPUsername is non-empty. SMTPPassword is not persisted to
	// the DB — it follows the same keyring handoff pattern as Password,
	// stored at key "<accountID>:smtp". See Account.SMTPUsername.
	SMTPUsername string `json:"smtpUsername"`
	SMTPPassword string `json:"smtpPassword"` // Not stored in DB, goes to keyring under "<accountID>:smtp"

	// ReplyForwardIdentityID — see Account.ReplyForwardIdentityID. Only
	// meaningful when NoOutgoingServer is true.
	ReplyForwardIdentityID string `json:"replyForwardIdentityId"`

	AuthType AuthType `json:"authType"`
	Username string   `json:"username"`
	Password string   `json:"password"` // Not stored in DB, goes to keyring

	Color string `json:"color"` // Hex color for account identification

	SyncPeriodDays int  `json:"syncPeriodDays"`
	SyncInterval   int  `json:"syncInterval"`   // Minutes between polls (0 = manual only)
	SyncAllFolders     bool `json:"syncAllFolders"`     // Sync all folders instead of just subscribed ones
	SyncFoldersEnabled bool `json:"syncFoldersEnabled"` // User opted into folder sync management

	// Read receipt settings
	ReadReceiptRequestPolicy string `json:"readReceiptRequestPolicy"`

	// Folder mappings (empty = auto-detect)
	SentFolderPath    string `json:"sentFolderPath,omitempty"`
	DraftsFolderPath  string `json:"draftsFolderPath,omitempty"`
	TrashFolderPath   string `json:"trashFolderPath,omitempty"`
	SpamFolderPath    string `json:"spamFolderPath,omitempty"`
	ArchiveFolderPath string `json:"archiveFolderPath,omitempty"`
	AllMailFolderPath string `json:"allMailFolderPath,omitempty"`
	StarredFolderPath string `json:"starredFolderPath,omitempty"`
}

// Validate validates the account configuration
func (c *AccountConfig) Validate() error {
	if c.Name == "" {
		return ErrNameRequired
	}
	if c.DisplayName == "" {
		return ErrDisplayNameRequired
	}
	if c.Email == "" {
		return ErrEmailRequired
	}
	if c.IMAPHost == "" {
		return ErrIMAPHostRequired
	}
	if !c.NoOutgoingServer && c.SMTPHost == "" {
		return ErrSMTPHostRequired
	}
	if c.Username == "" {
		return ErrUsernameRequired
	}
	if c.IMAPPort <= 0 {
		c.IMAPPort = 993
	}
	if c.SMTPPort <= 0 {
		c.SMTPPort = 587
	}
	if c.IMAPSecurity == "" {
		c.IMAPSecurity = SecurityTLS
	}
	if c.SMTPSecurity == "" {
		c.SMTPSecurity = SecurityStartTLS
	}
	if c.AuthType == "" {
		c.AuthType = AuthPassword
	}
	if c.SyncPeriodDays < 0 {
		c.SyncPeriodDays = 30
	}
	// SyncInterval: 0 is valid (manual only), negative means use default
	if c.SyncInterval < 0 {
		c.SyncInterval = 30 // Default: 30 minutes
	}
	if c.ReadReceiptRequestPolicy == "" {
		c.ReadReceiptRequestPolicy = "never"
	}
	return nil
}
