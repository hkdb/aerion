package backend

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
)

// probeCalDAVScheduling determines whether a CalDAV server supports RFC
// 6638's calendar-auto-schedule. Returns one of the itip_mode column
// values:
//   - "server": server handles iTIP delivery (PUTs with ATTENDEE
//     properties automatically deliver invitations to recipients via the
//     scheduling outbox).
//   - "none":   server doesn't support 6638; Aerion's PUT carries
//     ATTENDEE lines but no invitations leave. (SMTP-only 'client' mode
//     is out of scope for v0.3.0.)
//
// Detection prefers the strong signal:
//  1. OPTIONS response DAV header contains "calendar-auto-schedule".
//  2. PROPFIND on the principal returns a non-empty
//     `CALDAV:schedule-outbox-URL` element.
//
// On any error returns "server" — defensive: a Google or Microsoft
// account that briefly returns 5xx during probe shouldn't be flagged as
// non-scheduling, and a real Aerion PUT to a non-6638 CalDAV server is
// no worse than the pre-v0.3.0 behavior (no invitations delivered).
//
// Best-effort. NOT a blocking dependency: AddCalDAVSource succeeds even
// when probe fails; itip_mode just defaults to "server" and the UI's
// "Don't send" choice for that source stays available.
func probeCalDAVScheduling(ctx context.Context, baseURL, username, password string) string {
	const defaultMode = "server"

	// Stage 1: OPTIONS DAV: header.
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, baseURL, nil)
	if err != nil {
		return defaultMode
	}
	req.SetBasicAuth(username, password)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		dav := resp.Header.Get("DAV")
		resp.Body.Close()
		if strings.Contains(strings.ToLower(dav), "calendar-auto-schedule") {
			return "server"
		}
	}

	// Stage 2: PROPFIND on the URL probing for schedule-outbox-URL. This
	// would normally run against the principal URL — but the discovery
	// step has already resolved that and gone home; for the probe we
	// PROPFIND at the user-provided base. Most 6638-compliant servers
	// return schedule-outbox-URL on the calendar home too.
	body := `<?xml version="1.0" encoding="utf-8"?>
<propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <prop>
    <C:schedule-outbox-URL/>
  </prop>
</propfind>`
	pf, err := http.NewRequestWithContext(ctx, "PROPFIND", baseURL, strings.NewReader(body))
	if err != nil {
		return defaultMode
	}
	pf.Header.Set("Depth", "0")
	pf.Header.Set("Content-Type", "application/xml; charset=utf-8")
	pf.SetBasicAuth(username, password)
	resp, err = http.DefaultClient.Do(pf)
	if err != nil {
		return defaultMode
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return defaultMode
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return defaultMode
	}

	// We don't need the actual URL — only whether the element is
	// non-empty. A lightweight XML scan keeps this independent of the
	// principal's URL/structure.
	type prop struct {
		ScheduleOutboxURL *struct {
			Href string `xml:"href"`
		} `xml:"schedule-outbox-URL"`
	}
	type propstat struct {
		Prop prop `xml:"prop"`
	}
	type response struct {
		Propstat propstat `xml:"propstat"`
	}
	type multistatus struct {
		XMLName  xml.Name   `xml:"multistatus"`
		Response []response `xml:"response"`
	}
	var ms multistatus
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return defaultMode
	}
	for _, r := range ms.Response {
		if r.Propstat.Prop.ScheduleOutboxURL != nil && strings.TrimSpace(r.Propstat.Prop.ScheduleOutboxURL.Href) != "" {
			return "server"
		}
	}
	// Server is reachable but doesn't advertise scheduling support.
	return "none"
}

// probeCalDAVOrganizerIdentities returns the list of email addresses the
// principal is authorized to act as for scheduling — discovered via
// PROPFIND for `<C:calendar-user-address-set>` on the principal URL (RFC
// 6638 §2.4.1). Returned addresses are stripped of the `mailto:` prefix,
// lowercased, deduped. Empty slice means either the server doesn't expose
// the property OR the principal has no scheduling addresses configured;
// in both cases the caller falls back to the user-supplied "Organizer
// email" field on the setup dialog.
//
// Best-effort: any transport / auth / parse error returns nil so the
// caller can surface ErrCalDAVOrganizerEmailRequired and let the user
// provide an organizer email manually.
//
// Note: we PROPFIND at the URL the user supplied in the setup dialog.
// Discovery has already resolved that to the calendar-home-set on a
// well-formed CalDAV server; many RFC 6638-compliant servers expose
// `calendar-user-address-set` on the home-set too. Servers that only
// publish it on the principal URL return empty here — same UX as
// non-compliant servers (user types the email).
func probeCalDAVOrganizerIdentities(ctx context.Context, baseURL, username, password string) []string {
	body := `<?xml version="1.0" encoding="utf-8"?>
<propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <prop>
    <C:calendar-user-address-set/>
  </prop>
</propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", baseURL, strings.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.SetBasicAuth(username, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil
	}

	type href struct {
		Value string `xml:",chardata"`
	}
	type addressSet struct {
		Hrefs []href `xml:"href"`
	}
	type prop struct {
		AddressSet *addressSet `xml:"calendar-user-address-set"`
	}
	type propstat struct {
		Prop prop `xml:"prop"`
	}
	type response struct {
		Propstat []propstat `xml:"propstat"`
	}
	type multistatus struct {
		XMLName  xml.Name   `xml:"multistatus"`
		Response []response `xml:"response"`
	}
	var ms multistatus
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var out []string
	for _, r := range ms.Response {
		for _, ps := range r.Propstat {
			if ps.Prop.AddressSet == nil {
				continue
			}
			for _, h := range ps.Prop.AddressSet.Hrefs {
				v := strings.TrimSpace(h.Value)
				if v == "" {
					continue
				}
				// Address-set values can be mailto:, https:, or opaque
				// principal URIs. Per the v0.3.0 plan we only consume
				// mailto: (the only form that gives us an email to use
				// as ORGANIZER). Filter the rest silently.
				low := strings.ToLower(v)
				if !strings.HasPrefix(low, "mailto:") {
					continue
				}
				email := strings.ToLower(strings.TrimSpace(v[len("mailto:"):]))
				if email == "" {
					continue
				}
				if _, ok := seen[email]; ok {
					continue
				}
				seen[email] = struct{}{}
				out = append(out, email)
			}
		}
	}
	return out
}
