package backend

import (
	"context"
	"time"
)

// QueryFreeBusyForAttendees is the public API-layer entry point the
// Wails bridge calls. Wraps the aggregator in a context timeout so a
// stuck provider doesn't hang the UI.
//
// `selfEmails` is the union of the current user's account + identity
// emails (lowercased) so the aggregator can route self lookups to the
// local DB scan instead of a remote query.
func (a *API) QueryFreeBusyForAttendees(selfEmails, attendeeEmails []string, fromUnix, toUnix int64) ([]FreeBusyResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.QueryAggregatedFreeBusy(ctx, selfEmails, attendeeEmails, fromUnix, toUnix)
}
