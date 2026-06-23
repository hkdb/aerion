package backend

import (
	"strings"

	"github.com/emersion/go-ical"
)

// parseAttendeesFromVEVENT extracts the ATTENDEE properties from a
// go-ical Event into the Aerion Attendee shape. Returns nil when no
// ATTENDEE props are present (the common case for local single-user
// events).
//
// Per RFC 5545 § 3.8.4.1, ATTENDEE's value is a CAL-ADDRESS (typically
// `mailto:user@host`). Standard params extracted:
//   - CN          → CommonName (display string)
//   - PARTSTAT    → PartStat   (NEEDS-ACTION default per spec)
//   - ROLE        → Role       (REQ-PARTICIPANT default)
//   - RSVP        → RSVP       (TRUE/FALSE; FALSE default)
//   - CUTYPE      → CUType     (INDIVIDUAL default)
//   - DELEGATED-TO → Delegate  (first email only; multi-delegation rare)
//   - SCHEDULE-STATUS → ScheduleStatus (RFC 6638 status code from
//     CalDAV server after a PUT; empty for Google/MS)
func parseAttendeesFromVEVENT(ev *ical.Event) []Attendee {
	props := ev.Props.Values(ical.PropAttendee)
	if len(props) == 0 {
		return nil
	}
	out := make([]Attendee, 0, len(props))
	for i := range props {
		a := parseAttendeeProp(&props[i])
		if a.Email == "" {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseAttendeeProp converts one go-ical ATTENDEE property into an
// Attendee. Exposed for the test surface; production code goes through
// parseAttendeesFromVEVENT.
func parseAttendeeProp(p *ical.Prop) Attendee {
	if p == nil {
		return Attendee{}
	}
	a := Attendee{
		Email:          calAddressEmail(p.Value),
		CommonName:     strings.TrimSpace(p.Params.Get("CN")),
		PartStat:       upperOrDefault(p.Params.Get("PARTSTAT"), PartStatNeedsAction),
		Role:           upperOrDefault(p.Params.Get("ROLE"), RoleReqParticipant),
		RSVP:           strings.EqualFold(p.Params.Get("RSVP"), "TRUE"),
		CUType:         upperOrDefault(p.Params.Get("CUTYPE"), CUTypeIndividual),
		Delegate:       calAddressEmail(p.Params.Get("DELEGATED-TO")),
		ScheduleStatus: strings.TrimSpace(p.Params.Get("SCHEDULE-STATUS")),
	}
	return a
}

// parseOrganizerFromVEVENT extracts the ORGANIZER property, if any.
// Returns nil for events without an organizer (local single-user events
// commonly have none).
func parseOrganizerFromVEVENT(ev *ical.Event) *Organizer {
	p := ev.Props.Get(ical.PropOrganizer)
	if p == nil {
		return nil
	}
	email := calAddressEmail(p.Value)
	if email == "" {
		return nil
	}
	return &Organizer{
		Email:      email,
		CommonName: strings.TrimSpace(p.Params.Get("CN")),
	}
}

// emitAttendeesIntoVEVENT writes ATTENDEE and ORGANIZER properties onto
// a go-ical Event. Called by serializeVEVENT after the other base
// properties are set. Pass empty input to emit nothing.
func emitAttendeesIntoVEVENT(ev *ical.Event, organizer *OrganizerInput, attendees []AttendeeInput) {
	if organizer != nil && organizer.Email != "" {
		p := ical.NewProp(ical.PropOrganizer)
		p.Value = mailtoCalAddress(organizer.Email)
		if cn := strings.TrimSpace(organizer.CommonName); cn != "" {
			p.Params.Set("CN", cn)
		}
		ev.Props.Set(p)
	}
	for _, a := range attendees {
		email := strings.ToLower(strings.TrimSpace(a.Email))
		if email == "" {
			continue
		}
		p := ical.NewProp(ical.PropAttendee)
		p.Value = mailtoCalAddress(email)
		if cn := strings.TrimSpace(a.CommonName); cn != "" {
			p.Params.Set("CN", cn)
		}
		if ps := upperOrEmpty(a.PartStat); ps != "" {
			p.Params.Set("PARTSTAT", ps)
		}
		if r := upperOrEmpty(a.Role); r != "" && r != RoleReqParticipant {
			// Only emit ROLE when non-default; keeps the wire form clean.
			p.Params.Set("ROLE", r)
		}
		if a.RSVP {
			p.Params.Set("RSVP", "TRUE")
		}
		if ct := upperOrEmpty(a.CUType); ct != "" && ct != CUTypeIndividual {
			p.Params.Set("CUTYPE", ct)
		}
		if d := strings.ToLower(strings.TrimSpace(a.Delegate)); d != "" {
			p.Params.Set("DELEGATED-TO", mailtoCalAddress(d))
		}
		ev.Props.Add(p)
	}
}

// calAddressEmail extracts a lowercased email from an iCalendar CAL-ADDRESS
// value (`mailto:user@host`). Tolerant of missing prefix + leading/trailing
// whitespace.
func calAddressEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const prefix = "mailto:"
	if strings.HasPrefix(strings.ToLower(s), prefix) {
		s = s[len(prefix):]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// mailtoCalAddress is the inverse: prepends `mailto:` to a lowercased email.
func mailtoCalAddress(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	return "mailto:" + email
}

// upperOrDefault returns strings.ToUpper(s) when s is non-empty, else def.
func upperOrDefault(s, def string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return def
	}
	return strings.ToUpper(t)
}

// upperOrEmpty returns strings.ToUpper(s) when s is non-empty, else "".
func upperOrEmpty(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	return strings.ToUpper(t)
}
