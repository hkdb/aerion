package v1

// ContactEventType identifies a kind of contact event extensions can subscribe to.
type ContactEventType string

const (
	ContactEventAdded   ContactEventType = "added"
	ContactEventUpdated ContactEventType = "updated"
	ContactEventDeleted ContactEventType = "deleted"
)

// ContactEvent is delivered to subscribers of Contacts.SubscribeToContactEvents.
type ContactEvent struct {
	Type      ContactEventType `json:"type"`
	ContactID string           `json:"contactId"`
}

// ContactPatch is the optional-fields shape passed to Contacts.UpdateContact.
// Pointer fields distinguish "leave unchanged" (nil) from "set to empty"
// (non-nil pointer to zero value). Phase 2b.2.b.2 expanded this to the full
// multi-field surface so the Edit dialog can patch any subset of a contact's
// data in a single call.
//
// For multi-value fields (Emails, Phones, Addresses, etc.) the pointer-to-slice
// preserves three states: nil = leave unchanged; non-nil empty slice = clear all
// rows; non-nil populated slice = replace existing rows with the new set. The
// backend writes the whole record via UpsertRecordTx, so partial-row updates
// are NOT supported — callers send the full desired list.
//
// For Photo (single-value but structured), the same convention via
// pointer-to-struct: nil = unchanged; non-nil with empty Data + URL = remove
// the photo; non-nil with populated Data + MediaType = set inline.
type ContactPatch struct {
	Name       *string           `json:"name,omitempty"`
	Nickname   *string           `json:"nickname,omitempty"`
	Org        *string           `json:"org,omitempty"`
	Title      *string           `json:"title,omitempty"`
	Note       *string           `json:"note,omitempty"`
	Bday       *string           `json:"bday,omitempty"`
	Emails     *[]ContactEmail   `json:"emails,omitempty"`
	Phones     *[]ContactPhone   `json:"phones,omitempty"`
	Addresses  *[]ContactAddress `json:"addresses,omitempty"`
	URLs       *[]ContactURL     `json:"urls,omitempty"`
	IMPPs      *[]ContactIMPP    `json:"impps,omitempty"`
	Categories *[]string         `json:"categories,omitempty"`
	Photo      *ContactPhoto     `json:"photo,omitempty"`
}

// ContactPhoto is the PATCH-side grouping for photo edits. Pointer-to-struct
// on the patch matches the "nil = unchanged, non-nil = set" semantics that
// *[]ContactEmail uses for collections. This grouping shows up ONLY here —
// the read surface (Contact) keeps the existing flat-scalar pattern with
// PhotoData / PhotoMediaType / PhotoURL fields.
//
// URL is read-only on patch: the backend sets it when the parser sees a
// vCard URL-ref PHOTO. Write path always emits inline base64; callers that
// want to remove a photo send Data + URL both empty.
type ContactPhoto struct {
	Data      string `json:"data,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ContactCreateInput is the shape passed to Contacts.CreateContact.
//
// SourceID selects where the new contact lives:
//   - "" or "local" or "local:manual" → local manual contact (Aerion's
//     own SQLite store). The kind='manual' designation is set automatically.
//   - "local:collected"               → REJECTED. The 'collected' kind is
//     reserved for the sent-mail collection process to assign; users adding
//     via the Add dialog get kind='manual' regardless of which local sub-view
//     they came from.
//   - <CardDAV source UUID>           → Phase 2b.2.c (Track B) creates a new
//     vCard via WebDAV PUT to the addressbook identified by AddressbookID
//     (or the source's first writable addressbook when AddressbookID is "").
//   - Future provider routing         → returns ErrUnimplemented; filled in by
//     2b.3 (Google People / MS Graph).
//
// AddressbookID applies only when SourceID is a CardDAV source UUID. Empty
// AddressbookID means "the source's first writable addressbook."
//
// Rich-field support: when any of the optional rich fields below is set,
// the create dispatchers route through recordFromCreateInput which mirrors
// ContactPatch's shape onto a new contact.Record. Email + Name remain the
// legacy minimum; when only those are set the create paths take a thin
// "single primary email" shortcut for backward compatibility with the
// sent-mail collection path that still uses email+name.
//
// Slice semantics differ from ContactPatch on purpose: Patch uses *[]T to
// distinguish "unchanged" (nil) from "empty/cleared" ([]); Create has no
// such ambiguity (omitting means empty, providing means set), so plain
// slices are used.
type ContactCreateInput struct {
	SourceID      string `json:"sourceId,omitempty"`
	AddressbookID string `json:"addressbookId,omitempty"`
	Email         string `json:"email"`
	Name          string `json:"name,omitempty"`

	// Optional rich fields, mirroring ContactPatch's field set. When
	// Emails is supplied (non-empty), it REPLACES the implicit single
	// primary email built from the legacy Email field; otherwise the
	// legacy single-email shortcut applies.
	Nickname   string           `json:"nickname,omitempty"`
	Org        string           `json:"org,omitempty"`
	Title      string           `json:"title,omitempty"`
	Note       string           `json:"note,omitempty"`
	Bday       string           `json:"bday,omitempty"`
	Categories []string         `json:"categories,omitempty"`
	Emails     []ContactEmail   `json:"emails,omitempty"`
	Phones     []ContactPhone   `json:"phones,omitempty"`
	Addresses  []ContactAddress `json:"addresses,omitempty"`
	URLs       []ContactURL     `json:"urls,omitempty"`
	IMPPs      []ContactIMPP    `json:"impps,omitempty"`
	Photo      *ContactPhoto    `json:"photo,omitempty"`
}

// Addressbook is the API-surface descriptor for a CardDAV addressbook hosted
// by a contact source. Listed via Contacts.ListAddressbooks so the Add Contact
// UI can pick a target addressbook when a source has more than one.
type Addressbook struct {
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"` // server-relative path; mainly diagnostic
}

// ContactSource is the API-surface descriptor for a configured contact source
// (CardDAV server, or an OAuth-linked Google/Microsoft account). Listed via
// Contacts.ListSources so the extension's UI can display sources, gate
// edits/deletes on the per-source `Writable` flag, and route Add Contact
// creates by source id.
//
// Intentionally narrower than the host's internal Source type: only fields
// the extension UI actually consumes are exposed here. Last-sync timestamps,
// error messages, and OAuth account linkage details remain internal — the
// extension queries higher-level Wails methods (or surfaces ContactSource
// rows in a list-only role) rather than reading those fields directly.
type ContactSource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // "carddav" | "google" | "microsoft"
	Writable bool   `json:"writable"`
	// AccountID is the email account this source is linked to, when the source
	// was created via LinkAccountSource. Standalone CardDAV / contacts-only
	// OAuth sources have AccountID == "" (no linked email account).
	//
	// Surfaced so consent flows (Phase 2b.3) can find the source corresponding
	// to a given account after running incremental consent, without exposing
	// the host's full Source struct.
	AccountID string `json:"accountId,omitempty"`
}

// Contacts is the read/write/subscribe surface for contacts.
//
// All methods scoped to data the extension manages — local contact store +
// per-source mirror tables. Write methods dispatch by source under the hood:
// local (sent-recipient) contacts mutate through the host's contact.Store;
// CardDAV writes hit the server's WebDAV endpoint; Google / Microsoft writes
// go through their per-extension OAuth slot (granted via the write-access
// account picker flow).
type Contacts interface {
	SearchContacts(query string, limit int) ([]Contact, error)
	GetContact(emailOrID string) (*Contact, error)
	ListContacts(filter ContactFilter) ([]Contact, error)
	ListAddressbooks(sourceID string) ([]Addressbook, error)

	// ListSources returns all configured contact sources (CardDAV servers
	// and any OAuth-linked Google/Microsoft accounts). The extension's UI
	// consumes this for the sidebar listing, the Add Contact source picker,
	// and the per-source writable gate on Edit/Delete.
	ListSources() ([]ContactSource, error)

	// LinkAccountSource creates a new contact source backed by an existing
	// email account's OAuth tokens (used by the AccountContactsHookPanel
	// after a user adds a Google or Microsoft account). Returns the new
	// source's id. syncInterval is in minutes; 60 is the conventional
	// default. Errors with ErrAccountNotFound when the account doesn't exist.
	LinkAccountSource(accountID, name string, syncInterval int) (string, error)

	// SyncSource triggers an immediate sync against the given source.
	// Returns when the sync finishes; per-source failures are reported
	// via the returned error. Used by the contacts extension's sidebar
	// footer Ctrl+Shift+S handler.
	SyncSource(sourceID string) error

	// SyncAllSources triggers an immediate sync against every configured
	// contact source. Per-source failures don't abort the loop; the
	// returned error wraps any individual failures. Used by the contacts
	// extension's Ctrl+Shift+A shortcut.
	SyncAllSources() error

	// SetSourceWritable flips the writable flag on a contact source. Used
	// by the incremental-consent flow (Phase 2b.3) after the user grants
	// write scopes for an OAuth source. CardDAV sources also use it via
	// the "Enable write access" toggle in the source-settings dialog (where
	// it's a pure flag flip — no consent needed because basic-auth grants
	// full access).
	SetSourceWritable(sourceID string, writable bool) error

	CreateContact(input ContactCreateInput) (id string, err error)
	UpdateContact(id string, patch ContactPatch) error
	DeleteContact(id string) error
	SubscribeToContactEvents(types []ContactEventType) (ch <-chan ContactEvent, cancel Unsubscribe, err error)
}
