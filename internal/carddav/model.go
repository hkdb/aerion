// Package carddav provides CardDAV contact sync functionality
package carddav

import "time"

// SourceType represents the type of contact source
type SourceType string

const (
	SourceTypeCardDAV   SourceType = "carddav"
	SourceTypeGoogle    SourceType = "google"
	SourceTypeMicrosoft SourceType = "microsoft"
)

// Source represents a CardDAV server/account configuration
type Source struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Type         SourceType `json:"type"`
	URL          string     `json:"url"`          // CardDAV server URL (empty for OAuth sources)
	Username     string     `json:"username"`     // CardDAV username (empty for OAuth sources)
	AccountID    *string    `json:"account_id,omitempty"` // Linked email account ID (for OAuth sources using account's token)
	Enabled      bool       `json:"enabled"`
	Writable     bool       `json:"writable"` // Phase 2b: write capability flag (opt-in per source)
	SyncInterval int        `json:"sync_interval"` // Minutes (0 = manual only)
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// Addressbooks associated with this source (populated by GetSourceWithAddressbooks)
	Addressbooks []*Addressbook `json:"addressbooks,omitempty"`
}

// SourceConfig is used for creating/updating a source
type SourceConfig struct {
	Name         string     `json:"name"`
	Type         SourceType `json:"type"`
	URL          string     `json:"url"`      // CardDAV server URL (empty for OAuth sources)
	Username     string     `json:"username"` // CardDAV username (empty for OAuth sources)
	Password     string     `json:"password"` // CardDAV password, only used for create/update, not stored in DB
	AccountID    string     `json:"account_id,omitempty"` // Linked email account ID (for OAuth sources)
	Enabled      bool       `json:"enabled"`
	Writable     bool       `json:"writable"`
	SyncInterval int        `json:"sync_interval"`

	// Addressbooks to enable (paths) - only used for CardDAV sources
	EnabledAddressbooks []string `json:"enabled_addressbooks,omitempty"`
}

// Addressbook represents a single addressbook within a CardDAV source
type Addressbook struct {
	ID           string     `json:"id"`
	SourceID     string     `json:"source_id"`
	Path         string     `json:"path"`
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	SyncToken    string     `json:"sync_token,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

// AddressbookInfo is returned by discovery (before addressbook is saved to DB)
type AddressbookInfo struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Contact represents a contact synced from CardDAV
type Contact struct {
	ID            string    `json:"id"`
	AddressbookID string    `json:"addressbook_id"`
	Email         string    `json:"email"`
	DisplayName   string    `json:"display_name"`
	Href          string    `json:"href"` // CardDAV resource path
	ETag          string    `json:"etag"` // For change detection
	SyncedAt      time.Time `json:"synced_at"`
}

// SourceError represents an error that occurred during sync
type SourceError struct {
	SourceID   string    `json:"source_id"`
	SourceName string    `json:"source_name"`
	Error      string    `json:"error"`
	ErrorAt    time.Time `json:"error_at"`
}

// SyncStatus represents the current sync status of a source
type SyncStatus struct {
	SourceID     string     `json:"source_id"`
	IsSyncing    bool       `json:"is_syncing"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
}
