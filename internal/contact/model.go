// Package contact provides contact management for email autocomplete
package contact

import "time"

// Contact represents a contact for email autocomplete. One *Contact corresponds
// to a (record, email) pair in the unified schema — autocomplete is per-email.
// Multi-field data (phones/addresses/etc.) is exposed via Record.
type Contact struct {
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Source      string    `json:"source"` // legacy: "aerion", "carddav", "vcard"; new unified mapping: "local"→"aerion", "carddav"→"carddav"
	Kind        string    `json:"kind,omitempty"` // "manual" | "collected" — only set on local contacts
	AvatarURL   string    `json:"avatar_url,omitempty"`
	SendCount   int       `json:"send_count"`
	LastUsed    time.Time `json:"last_used"`
	CreatedAt   time.Time `json:"created_at"`
}

// LocalContact is a thin shape historically used in a couple of places. Kept
// for back-compat. Use Record for new multi-field needs.
type LocalContact struct {
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	SendCount   int       `json:"send_count"`
	LastUsed    time.Time `json:"last_used"`
	CreatedAt   time.Time `json:"created_at"`
}

// ContactSource describes a contact source (mostly used by older code paths).
type ContactSource struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // "aerion", "google", "vcard"
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
}

// SearchResult represents a contact search result with ranking info.
type SearchResult struct {
	Contact
	Score float64 `json:"score"`
}

// Record is the rich, multi-field contact_records row + its sub-table data.
// Returned by GetRecord/ListRecords, consumed by the Contacts extension.
type Record struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`              // 'local' | 'carddav'
	Kind       string    `json:"kind,omitempty"`      // local: 'manual' | 'collected'; empty for carddav
	SourceRef  string    `json:"source_ref,omitempty"` // carddav: addressbook_id
	Fn         string    `json:"fn"`                  // display name
	NGiven     string    `json:"n_given,omitempty"`
	NFamily    string    `json:"n_family,omitempty"`
	Org        string    `json:"org,omitempty"`
	Title      string    `json:"title,omitempty"`
	Note       string    `json:"note,omitempty"`
	Bday       string    `json:"bday,omitempty"`
	Nickname   string    `json:"nickname,omitempty"`
	// Photo fields (Phase 2b.2.b.2). Flat-scalar pattern matching Org/Title/Note.
	// At most one of {PhotoData + PhotoMediaType} OR PhotoURL is populated:
	//   - PhotoData (base64) + PhotoMediaType (e.g. "image/jpeg") = inline embed
	//     (vCard PHOTO;ENCODING=b;TYPE=...). Common CardDAV shape.
	//   - PhotoURL = vCard PHOTO URL-ref (PHOTO;VALUE=URI:...). Parsed but not
	//     fetched in this phase; Avatar falls back to initials.
	// All-empty = no photo.
	PhotoData      string    `json:"photo_data,omitempty"`
	PhotoMediaType string    `json:"photo_media_type,omitempty"`
	PhotoURL       string    `json:"photo_url,omitempty"`
	VCardRaw   string    `json:"-"` // preserved for round-trip; not exposed to JSON consumers
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Sub-table data — populated by GetRecord and (optionally) ListRecords.
	Emails     []RecordEmail   `json:"emails,omitempty"`
	Phones     []RecordPhone   `json:"phones,omitempty"`
	Addresses  []RecordAddress `json:"addresses,omitempty"`
	URLs       []RecordURL     `json:"urls,omitempty"`
	IMPPs      []RecordIMPP    `json:"impps,omitempty"`
	Categories []string        `json:"categories,omitempty"`
}

// RecordEmail is a single email belonging to a Record. Carries the per-email
// autocomplete metadata that lived on the legacy `contacts` table.
type RecordEmail struct {
	Email          string    `json:"email"`
	EmailType      string    `json:"email_type,omitempty"`
	IsPrimary      bool      `json:"is_primary,omitempty"`
	SendCount      int       `json:"send_count"`
	LastUsed       time.Time `json:"last_used"`
	NameOverridden bool      `json:"name_overridden,omitempty"`
}

// RecordPhone is a phone number belonging to a Record.
type RecordPhone struct {
	Number    string `json:"number"`
	PhoneType string `json:"phone_type,omitempty"`
	IsPrimary bool   `json:"is_primary,omitempty"`
}

// RecordAddress is a structured address belonging to a Record.
type RecordAddress struct {
	AddrType string `json:"addr_type,omitempty"`
	Street   string `json:"street,omitempty"`
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Postcode string `json:"postcode,omitempty"`
	Country  string `json:"country,omitempty"`
}

// RecordURL is a URL belonging to a Record.
type RecordURL struct {
	URL     string `json:"url"`
	URLType string `json:"url_type,omitempty"`
}

// RecordIMPP is an instant-messaging handle belonging to a Record (vCard IMPP).
type RecordIMPP struct {
	Handle   string `json:"handle"`
	IMPPType string `json:"impp_type,omitempty"`
}

// RecordFilter parameterizes ListRecords queries.
type RecordFilter struct {
	Source    string // 'local' | 'carddav' | '' for both
	Kind      string // for local: 'manual' | 'collected' | ''
	SourceRef string // optional: addressbook_id for carddav scope
	Query     string // optional case-insensitive fn/email substring
	Limit     int
	Offset    int
}
