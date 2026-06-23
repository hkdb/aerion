package backend

import (
	"strings"

	"github.com/hkdb/aerion/internal/contact"
)

// sidecarFromRecord extracts the email-type map + full URL list from a
// contact.Record into a MSSidecar suitable for SetMSSidecar. Mirrors the
// fields Microsoft Graph's contact schema can't represent natively (no per-
// email type field; only one URL slot). Address keys are lowercased and
// trimmed for case-insensitive matching on subsequent reads.
func sidecarFromRecord(rec *contact.Record) MSSidecar {
	side := MSSidecar{
		EmailTypes: map[string]string{},
		URLs:       []MSSidecarURL{},
	}
	if rec == nil {
		return side
	}
	for _, e := range rec.Emails {
		addr := strings.ToLower(strings.TrimSpace(e.Email))
		if addr == "" || e.EmailType == "" {
			continue
		}
		side.EmailTypes[addr] = e.EmailType
	}
	for _, u := range rec.URLs {
		url := strings.TrimSpace(u.URL)
		if url == "" {
			continue
		}
		side.URLs = append(side.URLs, MSSidecarURL{URL: url, Type: u.URLType})
	}
	return side
}

// applyMSExtrasToRecord re-stamps the Microsoft fields lost on the Graph
// round-trip (per-email type + URLs beyond businessHomePage) onto a Record
// freshly produced by `microsoftContactToRecord`. The intent comes from
// `src` — usually the original record the API layer wrote (carries the
// user's chosen types and the full URL list).
//
// Email match is by lowercase address. URLs are wholesale-replaced when
// src has any (the user's intent is the authoritative ordered list); when
// src has none we keep whatever Graph returned (single businessHomePage).
func applyMSExtrasToRecord(dst, src *contact.Record) {
	if dst == nil || src == nil {
		return
	}

	if len(src.Emails) > 0 {
		typeByAddr := map[string]string{}
		for _, e := range src.Emails {
			addr := strings.ToLower(strings.TrimSpace(e.Email))
			if addr == "" || e.EmailType == "" {
				continue
			}
			typeByAddr[addr] = e.EmailType
		}
		for i := range dst.Emails {
			addr := strings.ToLower(strings.TrimSpace(dst.Emails[i].Email))
			if t, ok := typeByAddr[addr]; ok && dst.Emails[i].EmailType == "" {
				dst.Emails[i].EmailType = t
			}
		}
	}

	if len(src.URLs) > 0 {
		dst.URLs = append([]contact.RecordURL{}, src.URLs...)
	}

	// Photo bytes never come back in Graph's POST/PATCH contact response —
	// only fetchable via the separate /photo/$value endpoint. Inherit the
	// user's uploaded bytes from `src` so the local DB shows the avatar
	// straight after save.
	dst.PhotoData = src.PhotoData
	dst.PhotoMediaType = src.PhotoMediaType
	dst.PhotoURL = src.PhotoURL
}

// applyMSSidecarToRecord stamps email types from a stored sidecar onto a
// Record (typically one produced by a sync or read path that has Graph-side
// data only) and replaces the URL list with the sidecar's full list when
// non-empty. Empty sidecar is a no-op — the Record keeps whatever Graph
// gave us.
//
// Stale-sidecar tolerance: emails in the sidecar that no longer exist on
// the Record (user deleted one via Outlook.com) are silently skipped. URLs
// from the sidecar replace whatever was on the Record because the sidecar
// IS the authoritative list (it's where the multi-URL intent lives).
func applyMSSidecarToRecord(rec *contact.Record, side MSSidecar) {
	if rec == nil {
		return
	}
	if len(side.EmailTypes) > 0 {
		for i := range rec.Emails {
			addr := strings.ToLower(strings.TrimSpace(rec.Emails[i].Email))
			if t, ok := side.EmailTypes[addr]; ok && rec.Emails[i].EmailType == "" {
				rec.Emails[i].EmailType = t
			}
		}
	}
	if len(side.URLs) > 0 {
		rec.URLs = make([]contact.RecordURL, 0, len(side.URLs))
		for _, u := range side.URLs {
			rec.URLs = append(rec.URLs, contact.RecordURL{URL: u.URL, URLType: u.Type})
		}
	}
}
