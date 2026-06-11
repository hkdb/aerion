package backend

import (
	"context"
	"strings"
	"time"
)

// queryLocalFreeBusy returns busy blocks from the user's own local
// (and synced) events that overlap the requested window. Caller passes
// only the user's own identity emails; the function expands every
// matching event in the range and emits one BUSY block per instance.
//
// Doesn't implement FreeBusyProvider — it's keyed on the user's emails
// (not arbitrary attendee addresses), so the aggregator dispatches it
// for self-emails only. Returns nil when len(selfEmails) == 0.
func (a *API) queryLocalFreeBusy(_ context.Context, selfEmails []string, fromUnix, toUnix int64) ([]FreeBusyBlock, error) {
	if len(selfEmails) == 0 {
		return nil, nil
	}
	// Build a lowercase set for membership tests when matching events to
	// emails. The local DB doesn't carry per-event owner email — events
	// in a local source belong to the user; for synced sources the user
	// is implicitly the calendar owner. So each event's instances become
	// busy blocks for ALL selfEmails (the user's blocks are the same
	// regardless of which identity is being queried).
	self := make(map[string]struct{}, len(selfEmails))
	for _, e := range selfEmails {
		t := strings.ToLower(strings.TrimSpace(e))
		if t == "" {
			continue
		}
		self[t] = struct{}{}
	}
	if len(self) == 0 {
		return nil, nil
	}

	// Fetch all events across all calendars + expand to instances in window.
	// (For tighter perf with thousands of events, this can later be
	// narrowed via a SQL range query — fine for v1.)
	sources, err := a.store.ListSources()
	if err != nil {
		return nil, err
	}
	var calendarIDs []string
	for _, src := range sources {
		cals, err := a.store.ListCalendars(src.ID)
		if err != nil {
			continue
		}
		for _, cal := range cals {
			calendarIDs = append(calendarIDs, cal.ID)
		}
	}
	if len(calendarIDs) == 0 {
		return nil, nil
	}
	events, err := a.store.ListEventsForExpansion(calendarIDs)
	if err != nil {
		return nil, err
	}
	from := time.Unix(fromUnix, 0)
	to := time.Unix(toUnix, 0)
	var blocks []FreeBusyBlock
	for _, ev := range events {
		// Overrides for this event aren't fetched; recurrence expansion
		// without overrides is acceptable for free/busy (occurrence
		// times don't change due to overrides much) and avoids an N+1
		// query at this stage. A future optimization can batch-load
		// overrides per calendar.
		instances, err := ExpandInRange(ev, nil, from, to)
		if err != nil {
			continue
		}
		for _, inst := range instances {
			for email := range self {
				blocks = append(blocks, FreeBusyBlock{
					Email:     email,
					StartUnix: inst.InstanceStartUnix,
					EndUnix:   inst.InstanceEndUnix,
					Status:    "BUSY",
				})
			}
		}
	}
	return blocks, nil
}
