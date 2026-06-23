package backend

import (
	"github.com/hkdb/aerion/internal/contact"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// fromLocal converts a core contact.Contact into the API-surface Contact.
//
// Core contacts are keyed by email, so we use the email itself as the ID. The
// Source field (e.g. "aerion", "google", "vcard", "carddav") becomes SourceID
// for search results where the user hasn't picked a specific source. Used by
// the autocomplete-style per-email row paths; multi-field fromRecord is the
// path the Contacts pane uses for its list + detail views.
func fromLocal(c *contact.Contact) coreapi.Contact {
	updated := c.LastUsed
	if updated.IsZero() {
		updated = c.CreatedAt
	}
	return coreapi.Contact{
		ID:        c.Email,
		Name:      c.DisplayName,
		Emails:    []string{c.Email},
		SourceID:  c.Source,
		UpdatedAt: updated,
	}
}

// fromRecord converts a contact.Record (the multi-field record-shape used by
// Phase 2b.2.a's unified schema) into the API-surface coreapi.Contact. One
// Contact per record, with all sub-tables surfaced through the rich
// Emails/Phones/Addresses/URLs/IMPPs/Categories slices.
//
// SourceID semantics:
//   - For local records: returns the legacy-mapped Source value ("aerion") so
//     the ContactDetail.svelte gate `sourceId === 'aerion'` keeps working for
//     Edit/Delete on local contacts.
//   - For CardDAV records: returns the addressbook-or-source id from
//     rec.SourceRef (when available). Phase 2b.2.b refines this so the
//     sidebar-source UUID (rather than the addressbook-id) lands here.
func fromRecord(rec *contact.Record) coreapi.Contact {
	if rec == nil {
		return coreapi.Contact{Emails: []string{}}
	}
	// Initialize Emails as empty slice (not nil) so the JSON payload always has
	// `"emails": []` rather than `"emails": null`. Frontend `{#each contact.emails}`
	// blocks iterate empty arrays fine; iterating null throws.
	out := coreapi.Contact{
		ID:        rec.ID,
		Name:      rec.Fn,
		Emails:    []string{},
		Org:       rec.Org,
		Title:     rec.Title,
		Note:      rec.Note,
		Bday:      rec.Bday,
		Nickname:  rec.Nickname,
		PhotoData:      rec.PhotoData,
		PhotoMediaType: rec.PhotoMediaType,
		PhotoURL:       rec.PhotoURL,
		UpdatedAt: rec.UpdatedAt,
	}

	// Source mapping: 'local' → 'aerion' (legacy compat for the detail-pane
	// gate). 'carddav' stays as 'carddav' or is overridden by the caller when
	// it knows a specific source id.
	out.SourceID = rec.Source
	if rec.Source == "local" {
		out.SourceID = "aerion"
	}

	// Flat email list (legacy autocomplete shape) + structured email items.
	for _, e := range rec.Emails {
		out.Emails = append(out.Emails, e.Email)
		out.EmailItems = append(out.EmailItems, coreapi.ContactEmail{
			Email:     e.Email,
			Type:      e.EmailType,
			IsPrimary: e.IsPrimary,
		})
	}
	for _, p := range rec.Phones {
		out.Phones = append(out.Phones, coreapi.ContactPhone{
			Number:    p.Number,
			Type:      p.PhoneType,
			IsPrimary: p.IsPrimary,
		})
	}
	for _, a := range rec.Addresses {
		out.Addresses = append(out.Addresses, coreapi.ContactAddress{
			Type:     a.AddrType,
			Street:   a.Street,
			City:     a.City,
			Region:   a.Region,
			Postcode: a.Postcode,
			Country:  a.Country,
		})
	}
	for _, u := range rec.URLs {
		out.URLs = append(out.URLs, coreapi.ContactURL{URL: u.URL, Type: u.URLType})
	}
	for _, i := range rec.IMPPs {
		out.IMPPs = append(out.IMPPs, coreapi.ContactIMPP{Handle: i.Handle, Type: i.IMPPType})
	}
	out.Categories = append(out.Categories, rec.Categories...)
	return out
}
