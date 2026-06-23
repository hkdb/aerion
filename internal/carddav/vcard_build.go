// Package carddav — vCard builder for the write path (Phase 2b.2.b).
//
// BuildVCard turns a contact.Record into a vCard byte slice ready for PUT to
// a CardDAV server. When the record has a non-empty `vcard_raw` (preserved by
// the parser on every sync), we parse it first so unknown properties survive
// the round-trip — only the standard field set is rewritten from the Record's
// current state. When `vcard_raw` is empty (e.g., a future local→carddav
// promote with no prior server-side state), we synthesize a minimal vCard
// 3.0 card from scratch.
//
// The field mapping mirrors parseVCard in client.go so build/parse round-trip
// is lossless for the standard fields.
package carddav

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/emersion/go-vcard"
	"github.com/hkdb/aerion/internal/contact"
)

// BuildVCard renders a contact.Record into vCard wire bytes. When originalRaw
// is non-empty, unknown properties (e.g., X-FOO) in the original are preserved
// verbatim in the output. When empty, a minimal vCard 3.0 card is built.
//
// The standard field set this builder writes:
//
//	FN, N, NICKNAME, BDAY, ORG, TITLE, NOTE, CATEGORIES, EMAIL+TYPE, TEL+TYPE,
//	ADR+TYPE (structured), URL+TYPE, IMPP+TYPE, PHOTO.
//
// PHOTO is emitted inline (vCard 3.0 dialect: `PHOTO;ENCODING=b;TYPE=...:<base64>`)
// when rec.PhotoData is set. When both PhotoData and PhotoURL are empty, the
// PHOTO field is deleted from the underlying card (so a previous photo is
// removed on round-trip). URL-ref output is deliberately unsupported — write
// path always emits inline.
//
// Other binary-laden fields (KEY, SOUND, LOGO) are NOT in the standard set we
// mutate; they pass through unchanged when present in originalRaw.
func BuildVCard(rec *contact.Record, originalRaw string) ([]byte, error) {
	if rec == nil {
		return nil, fmt.Errorf("BuildVCard: nil record")
	}

	card, err := startingCard(originalRaw)
	if err != nil {
		return nil, fmt.Errorf("BuildVCard: parse original: %w", err)
	}

	// Wipe the standard fields the Record owns; unknown fields (X-*, KEY,
	// SOUND, CATEGORIES dialect, etc.) stay because we only touch known keys.
	// PHOTO is now in the owned set — wiped + re-emitted below (or stays
	// deleted when the record has no photo, naturally removing it on save).
	for _, k := range []string{
		vcard.FieldFormattedName,
		vcard.FieldName,
		vcard.FieldNickname,
		vcard.FieldBirthday,
		vcard.FieldOrganization,
		vcard.FieldTitle,
		vcard.FieldNote,
		vcard.FieldCategories,
		vcard.FieldEmail,
		vcard.FieldTelephone,
		vcard.FieldAddress,
		vcard.FieldURL,
		vcard.FieldIMPP,
		vcard.FieldPhoto,
	} {
		delete(card, k)
	}

	// Re-populate from the Record. Use SetValue for single-value scalars;
	// Add for multi-value lists (so PREF/TYPE on the first entry is the
	// natural primary indicator).
	if fn := strings.TrimSpace(rec.Fn); fn != "" {
		card.SetValue(vcard.FieldFormattedName, fn)
	}
	if rec.NFamily != "" || rec.NGiven != "" {
		card.SetName(&vcard.Name{
			FamilyName: rec.NFamily,
			GivenName:  rec.NGiven,
		})
	}
	if nick := strings.TrimSpace(rec.Nickname); nick != "" {
		card.SetValue(vcard.FieldNickname, nick)
	}
	if bday := strings.TrimSpace(rec.Bday); bday != "" {
		card.SetValue(vcard.FieldBirthday, bday)
	}
	if org := strings.TrimSpace(rec.Org); org != "" {
		card.SetValue(vcard.FieldOrganization, org)
	}
	if title := strings.TrimSpace(rec.Title); title != "" {
		card.SetValue(vcard.FieldTitle, title)
	}
	if note := strings.TrimSpace(rec.Note); note != "" {
		card.SetValue(vcard.FieldNote, note)
	}
	if len(rec.Categories) > 0 {
		card.SetCategories(rec.Categories)
	}

	// PHOTO — emit inline base64 in vCard 3.0 dialect when PhotoData is set.
	// Empty PhotoData = no PHOTO field emitted (effectively removes a previous
	// photo because we wiped FieldPhoto above). URL-ref output is deliberately
	// unsupported.
	if data := strings.TrimSpace(rec.PhotoData); data != "" {
		mediaType := strings.TrimSpace(rec.PhotoMediaType)
		// Derive vCard 3.0 TYPE param from media type ("image/jpeg" → "JPEG").
		typeSuffix := "JPEG" // safe default; most servers accept it
		if mediaType != "" {
			if i := strings.LastIndex(mediaType, "/"); i >= 0 {
				typeSuffix = strings.ToUpper(mediaType[i+1:])
			}
		}
		card.Add(vcard.FieldPhoto, &vcard.Field{
			Value: data,
			Params: vcard.Params{
				"ENCODING": []string{"b"},
				"TYPE":     []string{typeSuffix},
			},
		})
	}

	for _, e := range rec.Emails {
		val := strings.TrimSpace(e.Email)
		if val == "" {
			continue
		}
		card.Add(vcard.FieldEmail, &vcard.Field{
			Value:  val,
			Params: typeParams(e.EmailType),
		})
	}
	for _, p := range rec.Phones {
		val := strings.TrimSpace(p.Number)
		if val == "" {
			continue
		}
		card.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  val,
			Params: typeParams(p.PhoneType),
		})
	}
	for _, a := range rec.Addresses {
		if isEmptyAddress(a) {
			continue
		}
		addr := &vcard.Address{
			Field: &vcard.Field{
				Params: typeParams(a.AddrType),
			},
			StreetAddress: a.Street,
			Locality:      a.City,
			Region:        a.Region,
			PostalCode:    a.Postcode,
			Country:       a.Country,
		}
		card.AddAddress(addr)
	}
	for _, u := range rec.URLs {
		val := strings.TrimSpace(u.URL)
		if val == "" {
			continue
		}
		card.Add(vcard.FieldURL, &vcard.Field{
			Value:  val,
			Params: typeParams(u.URLType),
		})
	}
	for _, i := range rec.IMPPs {
		val := strings.TrimSpace(i.Handle)
		if val == "" {
			continue
		}
		card.Add(vcard.FieldIMPP, &vcard.Field{
			Value:  val,
			Params: typeParams(i.IMPPType),
		})
	}

	// VERSION is required by go-vcard's encoder. Default to 3.0 for broad
	// server compatibility unless the original card carried 4.0.
	if card.Value(vcard.FieldVersion) == "" {
		card.SetValue(vcard.FieldVersion, "3.0")
	}
	// UID — keep the original if present; synthesize from rec.ID when not.
	if card.Value(vcard.FieldUID) == "" && rec.ID != "" {
		card.SetValue(vcard.FieldUID, rec.ID)
	}

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return nil, fmt.Errorf("BuildVCard: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// startingCard returns the parsed original or a fresh empty Card.
func startingCard(originalRaw string) (vcard.Card, error) {
	if strings.TrimSpace(originalRaw) == "" {
		return vcard.Card{}, nil
	}
	dec := vcard.NewDecoder(strings.NewReader(originalRaw))
	card, err := dec.Decode()
	if err != nil {
		return nil, err
	}
	return card, nil
}

// typeParams returns vCard Params carrying a single TYPE param, or nil when
// the type is empty. Uppercased for wire-level conventionality (vCard TYPEs
// are case-insensitive but the canonical form is upper).
func typeParams(t string) vcard.Params {
	t = strings.TrimSpace(t)
	if t == "" {
		return nil
	}
	return vcard.Params{vcard.ParamType: []string{strings.ToUpper(t)}}
}

// isEmptyAddress reports whether all structured parts of the address are blank.
func isEmptyAddress(a contact.RecordAddress) bool {
	return a.Street == "" && a.City == "" && a.Region == "" && a.Postcode == "" && a.Country == ""
}
