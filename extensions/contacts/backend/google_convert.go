package backend

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hkdb/aerion/internal/contact"
	"github.com/rs/zerolog"
)

// google_convert.go: maps between contact.Record (the unified Aerion shape) and
// googlePerson (the People API request/response shape). Re-implemented inside
// the extension package on purpose — the host-side READ parser in
// internal/contact/google_sync.go is search-only (just resourceName + names +
// emails) and intentionally not extended for write CRUD. This file owns the
// full bidirectional mapping for every field surfaced by ContactPatch.

// recordToGooglePerson builds a googlePerson request body from rec. Singleton-
// field violations (Google rejects multiple names/birthdays/biographies/genders
// with HTTP 400) are resolved by taking the first non-empty value and warning
// via the provided logger if there were others.
//
// rec is read-only here. Caller is responsible for building rec from the patch
// before calling (UpdateContact) or from CreateContactInput (CreateContact).
func recordToGooglePerson(rec *contact.Record, log zerolog.Logger) *googlePerson {
	if rec == nil {
		return &googlePerson{}
	}
	p := &googlePerson{}

	// Names — singleton. Take the first non-empty FN.
	if rec.Fn != "" || rec.NGiven != "" || rec.NFamily != "" {
		n := googleName{
			GivenName:   rec.NGiven,
			FamilyName:  rec.NFamily,
			DisplayName: rec.Fn,
		}
		p.Names = []googleName{n}
	}

	// Nicknames — repeated, but Aerion stores a single string.
	if rec.Nickname != "" {
		p.Nicknames = []googleNickname{{Value: rec.Nickname}}
	}

	// Organizations + titles — Aerion has a single Org/Title pair.
	if rec.Org != "" || rec.Title != "" {
		p.Organizations = []googleOrganization{{Name: rec.Org, Title: rec.Title}}
	}

	// Biographies (notes) — singleton, plain text.
	if rec.Note != "" {
		p.Biographies = []googleBiography{{Value: rec.Note, ContentType: "TEXT_PLAIN"}}
	}

	// Birthdays — singleton. Aerion stores as "YYYY-MM-DD" or "--MM-DD" (vCard
	// shorthand for no year). Best-effort parse; fall back to text on failure.
	if rec.Bday != "" {
		if bd := parseGoogleBirthday(rec.Bday); bd != nil {
			p.Birthdays = []googleBirthday{*bd}
		}
	}

	for _, e := range rec.Emails {
		if e.Email == "" {
			continue
		}
		p.EmailAddresses = append(p.EmailAddresses, googleEmail{
			Value: e.Email,
			Type:  mapTypeToGoogle(e.EmailType),
		})
	}

	for _, ph := range rec.Phones {
		if ph.Number == "" {
			continue
		}
		p.PhoneNumbers = append(p.PhoneNumbers, googlePhone{
			Value: ph.Number,
			Type:  mapTypeToGoogle(ph.PhoneType),
		})
	}

	for _, a := range rec.Addresses {
		if a.Street == "" && a.City == "" && a.Region == "" && a.Postcode == "" && a.Country == "" {
			continue
		}
		p.Addresses = append(p.Addresses, googleAddress{
			StreetAddress: a.Street,
			City:          a.City,
			Region:        a.Region,
			PostalCode:    a.Postcode,
			Country:       a.Country,
			Type:          mapTypeToGoogle(a.AddrType),
		})
	}

	for _, u := range rec.URLs {
		if u.URL == "" {
			continue
		}
		p.URLs = append(p.URLs, googleURL{
			Value: u.URL,
			Type:  mapTypeToGoogle(u.URLType),
		})
	}

	for _, im := range rec.IMPPs {
		if im.Handle == "" {
			continue
		}
		username, protocol := splitIMPP(im.Handle)
		p.IMClients = append(p.IMClients, googleIMClient{
			Username: username,
			Protocol: protocol,
			Type:     mapTypeToGoogle(im.IMPPType),
		})
	}

	return p
}

// googlePersonToRecord maps a Person from a response back into a partial
// contact.Record. Used by the API layer after CreateContact/UpdateContact to
// reconcile the local row with the server's accepted state. Caller is
// responsible for stitching id/source/source_ref onto the result.
//
// IMPORTANT: this is NOT round-trip lossless. Google strips unknown fields,
// re-normalizes phone/address formatting, and may add default types — so the
// returned Record reflects the server's view, not the original request.
func googlePersonToRecord(p *googlePerson) *contact.Record {
	if p == nil {
		return nil
	}
	rec := &contact.Record{}

	if len(p.Names) > 0 {
		n := p.Names[0]
		rec.Fn = n.DisplayName
		rec.NGiven = n.GivenName
		rec.NFamily = n.FamilyName
	}
	if len(p.Nicknames) > 0 {
		rec.Nickname = p.Nicknames[0].Value
	}
	if len(p.Organizations) > 0 {
		rec.Org = p.Organizations[0].Name
		rec.Title = p.Organizations[0].Title
	}
	if len(p.Biographies) > 0 {
		rec.Note = p.Biographies[0].Value
	}
	if len(p.Birthdays) > 0 {
		rec.Bday = formatGoogleBirthday(p.Birthdays[0])
	}
	for _, e := range p.EmailAddresses {
		if e.Value == "" {
			continue
		}
		rec.Emails = append(rec.Emails, contact.RecordEmail{
			Email:     strings.ToLower(strings.TrimSpace(e.Value)),
			EmailType: mapTypeFromGoogle(e.Type),
		})
	}
	for _, ph := range p.PhoneNumbers {
		if ph.Value == "" {
			continue
		}
		rec.Phones = append(rec.Phones, contact.RecordPhone{
			Number:    ph.Value,
			PhoneType: mapTypeFromGoogle(ph.Type),
		})
	}
	for _, a := range p.Addresses {
		rec.Addresses = append(rec.Addresses, contact.RecordAddress{
			AddrType: mapTypeFromGoogle(a.Type),
			Street:   a.StreetAddress,
			City:     a.City,
			Region:   a.Region,
			Postcode: a.PostalCode,
			Country:  a.Country,
		})
	}
	for _, u := range p.URLs {
		if u.Value == "" {
			continue
		}
		rec.URLs = append(rec.URLs, contact.RecordURL{URL: u.Value, URLType: mapTypeFromGoogle(u.Type)})
	}
	for _, im := range p.IMClients {
		if im.Username == "" {
			continue
		}
		handle := im.Username
		if im.Protocol != "" {
			handle = im.Protocol + ":" + im.Username
		}
		rec.IMPPs = append(rec.IMPPs, contact.RecordIMPP{Handle: handle, IMPPType: mapTypeFromGoogle(im.Type)})
	}
	return rec
}

// fieldMaskForRecord returns the comma-separated updatePersonFields mask for
// a full-state Update. Includes every field present on the record — the
// People API REPLACES the named fields wholesale, so we send everything we
// know about to avoid accidentally clearing fields that didn't change.
//
// The alternative (mask only the diff) requires loading the server state
// before the patch — extra round-trip per update. We prefer "send the whole
// known state" because Aerion's ContactPatch already collapses partial edits
// into a full intended state via applyContactPatchToRecord.
func fieldMaskForRecord(rec *contact.Record) string {
	if rec == nil {
		return ""
	}
	parts := []string{
		"names",
		"nicknames",
		"emailAddresses",
		"phoneNumbers",
		"addresses",
		"urls",
		"imClients",
		"organizations",
		"biographies",
		"birthdays",
	}
	return strings.Join(parts, ",")
}

// parseAddressbookGroupID converts a synthetic addressbook id from
// listGoogleAddressbooks back into a contactGroup resourceName (or "" for
// "My Contacts"). Returns ("", nil) for empty or unrecognized prefixes —
// callers treat that as "default destination, no group membership."
//
//	"google-mycontacts:<sourceID>"   → ""
//	"google-group:contactGroups/abc" → "contactGroups/abc"
//	""                                → ""
//	"unknown:..."                     → ""  (logged-but-tolerated)
func parseAddressbookGroupID(addressbookID string) string {
	switch {
	case addressbookID == "":
		return ""
	case strings.HasPrefix(addressbookID, "google-mycontacts:"):
		return ""
	case strings.HasPrefix(addressbookID, "google-group:"):
		return strings.TrimPrefix(addressbookID, "google-group:")
	}
	return ""
}

// etagFromPerson extracts the source-level etag Google requires on the
// next update. Lives at metadata.sources[0].etag (NOT the top-level Person.ETag,
// which is for compound calls). Returns empty string when the server response
// didn't carry one, in which case the API layer skips SetETag — the next
// update will GET-to-refresh before writing.
func etagFromPerson(p *googlePerson) string {
	if p == nil || p.Metadata == nil {
		return ""
	}
	for _, s := range p.Metadata.Sources {
		if s.Type == "CONTACT" && s.ETag != "" {
			return s.ETag
		}
	}
	return ""
}

// ---- helpers ---------------------------------------------------------------

// mapTypeToGoogle translates an Aerion-side type label (vCard-flavored) into
// Google's preferred lower-case enum-style label. Google accepts arbitrary
// strings ("custom") so unknowns pass through unchanged.
func mapTypeToGoogle(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "":
		return ""
	case "home", "work", "mobile", "main", "other":
		return t
	}
	return t
}

// mapTypeFromGoogle is the inverse — currently identity (Google already
// returns lowercase labels). Separate function so future type-vocabulary
// drift only changes one side.
func mapTypeFromGoogle(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// splitIMPP splits Aerion's "protocol:handle" IMPP storage into Google's
// separate Protocol + Username fields. When the handle has no scheme prefix,
// the protocol comes back empty — Google still accepts that.
func splitIMPP(handle string) (username, protocol string) {
	if idx := strings.Index(handle, ":"); idx > 0 {
		return handle[idx+1:], handle[:idx]
	}
	return handle, ""
}

// parseGoogleBirthday accepts vCard date forms ("YYYY-MM-DD", "--MM-DD",
// "YYYYMMDD", "--MMDD") and returns a googleBirthday. Returns nil for
// formats we can't parse — the caller drops the field in that case.
func parseGoogleBirthday(s string) *googleBirthday {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Normalize compact "YYYYMMDD" / "--MMDD" to dashed.
	switch {
	case len(s) == 8 && !strings.Contains(s, "-"):
		s = s[:4] + "-" + s[4:6] + "-" + s[6:8]
	case len(s) == 6 && strings.HasPrefix(s, "--"):
		s = "--" + s[2:4] + "-" + s[4:6]
	}
	if strings.HasPrefix(s, "--") && len(s) == 7 {
		// --MM-DD, no year.
		month := atoiOrZero(s[2:4])
		day := atoiOrZero(s[5:7])
		if month == 0 || day == 0 {
			return nil
		}
		return &googleBirthday{Date: &googleDate{Month: month, Day: day}}
	}
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		year := atoiOrZero(s[0:4])
		month := atoiOrZero(s[5:7])
		day := atoiOrZero(s[8:10])
		if month == 0 || day == 0 {
			return nil
		}
		return &googleBirthday{Date: &googleDate{Year: year, Month: month, Day: day}}
	}
	// Fall back to text — Google accepts a freeform text birthday.
	return &googleBirthday{Text: s}
}

// formatGoogleBirthday is the inverse: emits "YYYY-MM-DD" when both date and
// year are present, "--MM-DD" when year is zero, or the text field otherwise.
func formatGoogleBirthday(b googleBirthday) string {
	if b.Text != "" {
		return b.Text
	}
	if b.Date == nil {
		return ""
	}
	if b.Date.Year > 0 {
		return fmt.Sprintf("%04d-%02d-%02d", b.Date.Year, b.Date.Month, b.Date.Day)
	}
	return fmt.Sprintf("--%02d-%02d", b.Date.Month, b.Date.Day)
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
