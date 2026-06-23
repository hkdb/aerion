package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UpdateMyAttendeeStatus changes the current user's PARTSTAT on an event
// they were invited to. Self is resolved against the supplied lowercased
// email set (Phase D's frontend passes the union of account.Email +
// Identity.Email per the cross-cutting decision in the v0.3.0 plan).
//
// Flow:
//  1. Load Event + resolve source/calendar.
//  2. Find the attendee whose email matches one of selfEmails.
//  3. Mutate that attendee's PartStat; re-serialize ICS; re-upsert (the
//     attendees_json column + side index update via store.UpsertEventTx).
//  4. Push through the provider via the dispatch helper below.
//  5. Bump synced_at via the same UpsertEventTx pathway.
//
// PartStat must be one of the canonical iCal values. Invalid values
// return an error before any side effect.
func (a *API) UpdateMyAttendeeStatus(eventID string, selfEmails []string, partStat string) error {
	if eventID == "" {
		return errors.New("calendar: eventID required")
	}
	switch strings.ToUpper(strings.TrimSpace(partStat)) {
	case PartStatAccepted, PartStatDeclined, PartStatTentative, PartStatNeedsAction:
	default:
		return fmt.Errorf("calendar: invalid partStat %q", partStat)
	}
	normalizedPartStat := strings.ToUpper(strings.TrimSpace(partStat))

	ev, err := a.store.GetEvent(eventID)
	if err != nil {
		return fmt.Errorf("load event: %w", err)
	}
	if ev == nil {
		return fmt.Errorf("event %s not found", eventID)
	}

	// Build a lowercase self-email set for the membership probe.
	self := make(map[string]struct{}, len(selfEmails))
	for _, e := range selfEmails {
		t := strings.ToLower(strings.TrimSpace(e))
		if t == "" {
			continue
		}
		self[t] = struct{}{}
	}
	if len(self) == 0 {
		return errors.New("calendar: no identity emails supplied; cannot resolve self attendee")
	}

	// Locate self in the attendee list. Mutate the matching entry only.
	mutated := false
	updatedAttendees := make([]Attendee, len(ev.Attendees))
	for i, att := range ev.Attendees {
		updatedAttendees[i] = att
		if _, ok := self[strings.ToLower(att.Email)]; !ok {
			continue
		}
		updatedAttendees[i].PartStat = normalizedPartStat
		mutated = true
	}
	if !mutated {
		return errors.New("calendar: you are not an attendee on this event")
	}
	ev.Attendees = updatedAttendees

	// Re-serialize ICS so the JSON column and the ICSBlob stay coherent.
	// Re-use the existing EventInput conversion shape that serializeVEVENT
	// expects; rebuild from the Event fields we have.
	in := eventToEventInput(*ev)
	newICS, err := serializeVEVENT(ev.UID, in)
	if err != nil {
		return fmt.Errorf("re-serialize event: %w", err)
	}
	ev.ICSBlob = newICS

	// Resolve source + calendar so the provider dispatch knows which
	// backend to talk to. Local source has no remote push.
	cal, err := a.store.GetCalendar(ev.CalendarID)
	if err != nil {
		return fmt.Errorf("load calendar: %w", err)
	}
	if cal == nil {
		return fmt.Errorf("calendar %s not found", ev.CalendarID)
	}
	src, err := a.store.GetSource(cal.SourceID)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
	}
	if src == nil {
		return fmt.Errorf("source %s not found", cal.SourceID)
	}

	// Push to the provider FIRST so any conflict (412 / etag mismatch) is
	// visible before we mutate the local copy. The push helper returns
	// the new ETag / Schedule-Tag if applicable; we stitch it onto ev
	// before the local upsert so subsequent edits send the fresh tag.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pushed, err := a.pushRSVPThroughProvider(ctx, *src, *cal, *ev, normalizedPartStat)
	if err != nil {
		return fmt.Errorf("push rsvp: %w", err)
	}
	if pushed.ETag != "" {
		ev.ETag = pushed.ETag
	}
	if pushed.ProviderEventID != "" {
		ev.ProviderEventID = pushed.ProviderEventID
	}

	// Persist locally.
	if err := a.store.WithTx(func(tx *sql.Tx) error {
		return a.store.UpsertEventTx(tx, *ev)
	}); err != nil {
		return fmt.Errorf("upsert event: %w", err)
	}
	return nil
}

// attendeesFromInput is the EventInput → Event copy used by CreateEvent
// and updateAllAndPush to persist the user's chosen attendees + organizer
// onto the Event struct (and therefore into the attendees_json /
// organizer_json columns + the event_attendee_index side table).
//
// Without this, the ICS blob carries ATTENDEE lines (because
// serializeVEVENT reads `in` directly) and the provider receives the
// attendees correctly, but the local JSON columns stay empty — so
// EventDetail + EventComposerDialog (which read the JSON, not the
// parsed ICS) show no attendees after save.
//
// ScheduleStatus is left zero — that field is populated only on read
// from server responses (RFC 6638 CalDAV) and never written from
// EventInput.
func attendeesFromInput(in EventInput) ([]Attendee, *Organizer) {
	var out []Attendee
	if len(in.Attendees) > 0 {
		out = make([]Attendee, 0, len(in.Attendees))
		for _, a := range in.Attendees {
			out = append(out, Attendee{
				Email:      a.Email,
				CommonName: a.CommonName,
				PartStat:   a.PartStat,
				Role:       a.Role,
				RSVP:       a.RSVP,
				CUType:     a.CUType,
				Delegate:   a.Delegate,
			})
		}
	}
	var org *Organizer
	if in.Organizer != nil {
		org = &Organizer{Email: in.Organizer.Email, CommonName: in.Organizer.CommonName}
	}
	return out, org
}

// eventToEventInput rebuilds the write-side EventInput shape from a loaded
// Event. Used by UpdateMyAttendeeStatus to round-trip through
// serializeVEVENT after mutating an attendee's PartStat.
func eventToEventInput(ev Event) EventInput {
	in := EventInput{
		CalendarID:  ev.CalendarID,
		Summary:     ev.Summary,
		Description: ev.Description,
		Location:    ev.Location,
		DTStartUnix: ev.DTStartUnix,
		DTEndUnix:   ev.DTEndUnix,
		IsAllDay:    ev.IsAllDay,
		TZName:      ev.TZName,
	}
	// Recurrence: we deliberately don't reconstruct a RecurrenceSpec from
	// ev.RRuleText (lossy). ICS round-trip via ev.ICSBlob preserves the raw
	// form. UpdateMyAttendeeStatus changes only attendee PartStat, so the
	// recurrence pattern is unaffected.
	if len(ev.Attendees) > 0 {
		in.Attendees = make([]AttendeeInput, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			in.Attendees = append(in.Attendees, AttendeeInput{
				Email:      a.Email,
				CommonName: a.CommonName,
				PartStat:   a.PartStat,
				Role:       a.Role,
				RSVP:       a.RSVP,
				CUType:     a.CUType,
				Delegate:   a.Delegate,
			})
		}
	}
	if ev.Organizer != nil {
		in.Organizer = &OrganizerInput{Email: ev.Organizer.Email, CommonName: ev.Organizer.CommonName}
	}
	return in
}

// rsvpPushResult carries the bits the local row needs to stitch after a
// provider RSVP push. Both fields are optional — providers that don't
// surface a new etag (Microsoft's /accept etc. returns 202 No Content)
// leave them empty.
type rsvpPushResult struct {
	ETag            string
	ProviderEventID string
}

// pushRSVPThroughProvider dispatches the RSVP write to whichever provider
// owns the source.
//   - Local: no-op (no remote to talk to).
//   - CalDAV: re-PUT the whole event via the existing PushEvent path.
//     RFC 6638-aware servers auto-send REPLY. Older servers swallow it
//     silently; the user-visible PartStat is still local-correct.
//   - Google: PATCH /events/{id} with the updated attendees[] array.
//   - Microsoft: POST /me/events/{id}/{accept|decline|tentativelyAccept}
//     (see provider_microsoft_rsvp.go). These dedicated endpoints carry
//     `sendResponse: true` semantics and are preferred over a generic PATCH.
func (a *API) pushRSVPThroughProvider(ctx context.Context, src Source, cal Calendar, ev Event, partStat string) (rsvpPushResult, error) {
	provider := ProviderForSource(src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
	if provider == nil {
		// Local source — nothing to push.
		return rsvpPushResult{}, nil
	}
	switch src.Type {
	case SourceTypeMicrosoft:
		// Provider-specific RSVP endpoints.
		return a.pushMicrosoftRSVP(ctx, src, ev, partStat)
	case SourceTypeGoogle, SourceTypeCalDAV:
		// Both these providers accept a re-PUT/PATCH of the whole event;
		// our serialized ICS already carries the updated PARTSTAT.
		result, err := provider.PushEvent(ctx, src, cal, ev)
		if err != nil {
			return rsvpPushResult{}, err
		}
		return rsvpPushResult{ETag: result.ETag, ProviderEventID: result.ProviderEventID}, nil
	}
	return rsvpPushResult{}, nil
}
