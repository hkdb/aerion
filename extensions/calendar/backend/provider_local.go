package backend

import "context"

// localProvider — Provider impl for the SourceTypeLocal case.
//
// Local sources have no remote, so all four methods are intentional
// no-ops: persistence happens entirely in event_crud.go's DB-mutation
// paths. The provider's role for local is to participate uniformly in
// the dispatch the way caldav / google / microsoft do, so the API layer
// can call provider.PushEvent unconditionally without per-type branching.

type localProvider struct{}

func (localProvider) Capabilities() Capabilities {
	return Capabilities{
		CanWrite:        true,
		CanDeleteSeries: true,
		CanSetReminders: true,
	}
}

func (localProvider) SyncCalendar(_ context.Context, _ Source, _ Calendar) error {
	return nil
}

func (localProvider) PushEvent(_ context.Context, _ Source, _ Calendar, _ Event) (PushResult, error) {
	return PushResult{}, nil
}

func (localProvider) DeleteRemote(_ context.Context, _ Source, _ Calendar, _ Event) error {
	return nil
}

func (localProvider) PushInstance(_ context.Context, _ Source, _ Calendar, _ PushInstancePayload) (PushInstanceResult, error) {
	// Local has no remote — event_crud.go's existing scope branches
	// (updateThis / updateThisAndFuture / deleteThis / deleteThisAndFuture)
	// handle the persistence directly.
	return PushInstanceResult{}, nil
}
