package backend

import (
	"strings"

	"github.com/hkdb/aerion/internal/contact"
	"github.com/rs/zerolog"
)

// microsoft_convert.go: maps between contact.Record and msContact for the
// Microsoft Graph write path. Mirrors google_convert.go's shape. The mapping
// is lossier than Google's in two places:
//
//   1. URLs collapse: contact.Record can carry many URLs with types; Graph's
//      Contact has only `businessHomePage` (single string). On write we keep
//      the first URL and warn via log; the rest are dropped.
//
//   2. Phones are bucketed: contact.Record.Phones is a single typed list;
//      Graph distributes into `businessPhones[]` / `homePhones[]` /
//      `mobilePhone` (single). Phones whose type doesn't map cleanly fall
//      into businessPhones (Graph's catch-all bucket).
//
// On read-back from Graph (UpdateContact / CreateContact response), the
// inverse maps reconstruct contact.RecordPhones from the three buckets.
// Categories and IMPP round-trip cleanly. Birthdays use Graph's ISO 8601
// datetime field (`birthday: "YYYY-MM-DDTHH:00:00Z"`), parsed loosely.

// recordToMicrosoftContact builds an msContact request body from rec. The
// passed logger is used to warn on lossy field collapses (multi-URL, > 3
// addresses); pass zerolog.Nop() in tests where the warning noise isn't
// useful.
func recordToMicrosoftContact(rec *contact.Record, log zerolog.Logger) *msContact {
	if rec == nil {
		return &msContact{}
	}
	c := &msContact{}

	// Name split. Aerion stores NGiven/NFamily separately + Fn as the display
	// label. Send all three to Graph; if one is empty, Graph falls back to
	// composing displayName from given+surname.
	c.GivenName = rec.NGiven
	c.Surname = rec.NFamily
	c.DisplayName = rec.Fn

	c.NickName = rec.Nickname
	c.CompanyName = rec.Org
	c.JobTitle = rec.Title
	c.Personal = rec.Note

	if rec.Bday != "" {
		c.Birthday = microsoftBirthdayFromString(rec.Bday)
	}

	// Emails — Graph's emailAddresses[].name slot rounds-trip the type label.
	for _, e := range rec.Emails {
		if e.Email == "" {
			continue
		}
		c.EmailAddresses = append(c.EmailAddresses, msEmailAddress{
			Address: e.Email,
			Name:    e.EmailType,
		})
	}

	// Phone distribution. We bucket by EmailType-equivalent on the phone.
	// Unknown types go to businessPhones (the Graph default), so we never
	// drop a phone.
	for _, p := range rec.Phones {
		if p.Number == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(p.PhoneType)) {
		case "mobile", "cell":
			// mobilePhone is a single string. Last one wins if multiple.
			c.MobilePhone = p.Number
		case "home":
			c.HomePhones = append(c.HomePhones, p.Number)
		default:
			c.BusinessPhones = append(c.BusinessPhones, p.Number)
		}
	}

	// Addresses: distribute into home / business / other slots. Past three
	// addresses get dropped — Graph has no slot for a 4th address type.
	var dropped int
	for _, a := range rec.Addresses {
		if a.Street == "" && a.City == "" && a.Region == "" && a.Postcode == "" && a.Country == "" {
			continue
		}
		addr := &msPhysicalAddress{
			Street:          a.Street,
			City:            a.City,
			State:           a.Region,
			CountryOrRegion: a.Country,
			PostalCode:      a.Postcode,
		}
		switch strings.ToLower(strings.TrimSpace(a.AddrType)) {
		case "home":
			if c.HomeAddress == nil {
				c.HomeAddress = addr
				continue
			}
			dropped++
		case "work", "business":
			if c.BusinessAddress == nil {
				c.BusinessAddress = addr
				continue
			}
			dropped++
		default:
			if c.OtherAddress == nil {
				c.OtherAddress = addr
				continue
			}
			dropped++
		}
	}
	if dropped > 0 {
		log.Warn().Int("dropped", dropped).Msg("Microsoft contact addresses past 3 slots dropped (Graph has no slot for extras)")
	}

	// URLs collapse to a single businessHomePage. First non-empty wins.
	var urlsKept int
	for _, u := range rec.URLs {
		if u.URL == "" {
			continue
		}
		if urlsKept == 0 {
			c.BusinessHomePage = u.URL
			urlsKept++
			continue
		}
		urlsKept++
	}
	if urlsKept > 1 {
		log.Warn().Int("urls_total", urlsKept).Msg("Microsoft contact has only a single URL slot; extras dropped")
	}

	// IMPPs round-trip as opaque strings; the writer doesn't split the
	// "protocol:handle" prefix (Graph doesn't model protocol).
	for _, im := range rec.IMPPs {
		if im.Handle == "" {
			continue
		}
		c.IMAddresses = append(c.IMAddresses, im.Handle)
	}

	// Categories are a plain string[] on Graph — same shape as Record.
	if len(rec.Categories) > 0 {
		c.Categories = append(c.Categories, rec.Categories...)
	}

	return c
}

// microsoftContactToRecord maps a Graph Contact response back into a partial
// contact.Record. Used by the API layer after CreateContact / UpdateContact
// to reconcile the local row with the server's accepted state.
//
// NOT round-trip lossless — see recordToMicrosoftContact for the lossy
// fields. Multi-URL records that hit the wire come back with one URL.
func microsoftContactToRecord(c *msContact) *contact.Record {
	if c == nil {
		return nil
	}
	rec := &contact.Record{}

	rec.Fn = c.DisplayName
	rec.NGiven = c.GivenName
	rec.NFamily = c.Surname
	rec.Nickname = c.NickName
	rec.Org = c.CompanyName
	rec.Title = c.JobTitle
	rec.Note = c.Personal

	if c.Birthday != "" {
		rec.Bday = microsoftBirthdayToString(c.Birthday)
	}

	for _, e := range c.EmailAddresses {
		if e.Address == "" {
			continue
		}
		rec.Emails = append(rec.Emails, contact.RecordEmail{
			Email:     strings.ToLower(strings.TrimSpace(e.Address)),
			EmailType: e.Name,
		})
	}

	// Rebuild phones from the three buckets. Type metadata comes from which
	// bucket the number came from — preserves user intent across the round
	// trip (a "home" phone written as homePhones[] reads back as PhoneType="home").
	for _, p := range c.BusinessPhones {
		if p == "" {
			continue
		}
		rec.Phones = append(rec.Phones, contact.RecordPhone{Number: p, PhoneType: "work"})
	}
	for _, p := range c.HomePhones {
		if p == "" {
			continue
		}
		rec.Phones = append(rec.Phones, contact.RecordPhone{Number: p, PhoneType: "home"})
	}
	if c.MobilePhone != "" {
		rec.Phones = append(rec.Phones, contact.RecordPhone{Number: c.MobilePhone, PhoneType: "mobile"})
	}

	if c.HomeAddress != nil {
		rec.Addresses = append(rec.Addresses, addressFromMicrosoft(c.HomeAddress, "home"))
	}
	if c.BusinessAddress != nil {
		rec.Addresses = append(rec.Addresses, addressFromMicrosoft(c.BusinessAddress, "work"))
	}
	if c.OtherAddress != nil {
		rec.Addresses = append(rec.Addresses, addressFromMicrosoft(c.OtherAddress, "other"))
	}

	if c.BusinessHomePage != "" {
		rec.URLs = append(rec.URLs, contact.RecordURL{URL: c.BusinessHomePage, URLType: "work"})
	}

	for _, h := range c.IMAddresses {
		if h == "" {
			continue
		}
		rec.IMPPs = append(rec.IMPPs, contact.RecordIMPP{Handle: h})
	}

	if len(c.Categories) > 0 {
		rec.Categories = append(rec.Categories, c.Categories...)
	}

	return rec
}

func addressFromMicrosoft(a *msPhysicalAddress, addrType string) contact.RecordAddress {
	return contact.RecordAddress{
		AddrType: addrType,
		Street:   a.Street,
		City:     a.City,
		Region:   a.State,
		Postcode: a.PostalCode,
		Country:  a.CountryOrRegion,
	}
}

// parseAddressbookFolderID converts a synthetic addressbook id from
// listMicrosoftAddressbooks back into a contactFolder id (or "" for the
// default mailbox folder). Mirrors google_convert.go's parseAddressbookGroupID.
//
//	"ms-default:<sourceID>"     → "" (default folder, no routing)
//	"ms-folder:<folderID>"      → "<folderID>"
//	""                           → ""
//	unknown                       → "" (logged-but-tolerated; defaults to default folder)
func parseAddressbookFolderID(addressbookID string) string {
	switch {
	case addressbookID == "":
		return ""
	case strings.HasPrefix(addressbookID, "ms-default:"):
		return ""
	case strings.HasPrefix(addressbookID, "ms-folder:"):
		return strings.TrimPrefix(addressbookID, "ms-folder:")
	}
	return ""
}

// microsoftBirthdayFromString emits a Graph-compatible birthday string from
// Aerion's "YYYY-MM-DD" (or "--MM-DD" no-year shorthand). Graph requires an
// ISO 8601 datetime; we anchor at midnight UTC. No-year shorthand emits a
// synthetic year 1604 (the Graph "unknown year" convention used in Outlook
// — matches what the Outlook UI emits for date-only birthdays).
func microsoftBirthdayFromString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "--") && len(s) == 7 {
		// --MM-DD, no year. Graph wants a year; use 1604 per the Outlook convention.
		return "1604-" + s[2:7] + "T00:00:00Z"
	}
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		return s + "T00:00:00Z"
	}
	return ""
}

// microsoftBirthdayToString is the inverse: extracts "YYYY-MM-DD" (or
// "--MM-DD" when the year is the 1604 sentinel) from Graph's ISO datetime.
// Tolerant of timezone offsets — we only care about the date portion.
func microsoftBirthdayToString(s string) string {
	if len(s) < 10 {
		return ""
	}
	date := s[:10]
	if !strings.HasPrefix(date, "1604-") {
		return date
	}
	// 1604 sentinel → no year known.
	return "--" + date[5:]
}
