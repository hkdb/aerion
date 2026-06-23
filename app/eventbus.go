package app

import (
	"fmt"
	"strings"
	"sync"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// eventBusCoreImpl is the host implementation of coreapi.EventBus. It serves
// two consumer audiences:
//
//  1. Go-side subscribers — extensions that call core.Events().Subscribe(name,
//     handler) get fan-out as in-process function calls. Used by the calendar
//     extension's Syncer to listen for `system:wake` and `system:network-online`.
//  2. Frontend — every Publish also calls wailsRuntime.EventsEmit so the
//     Svelte side can subscribe to the same event names via EventsOn().
//
// System-event publishing (`system:wake`, `system:network-online`): the host's
// existing sleep/wake + network monitors are already running for mail's
// IMAP IDLE auto-reconnect (see `app/background.go::processSleepWakeEvents`
// and `processNetworkEvents`). Those processors call `a.coreEventBus().
// Publish(...)` inline after they handle the event. Lightweight-by-default
// (R15) is preserved: Publish on an empty subscriber list is one
// map-lookup + len-zero loop (~10ns); no extra goroutines, no extra
// listeners, no extra D-Bus subscriptions when no extension is consuming.
type eventBusCoreImpl struct {
	app *App

	mu          sync.Mutex
	subscribers map[string][]*handlerEntry
}

// handlerEntry is one registered subscriber. We use pointer identity so
// Unsubscribe can find-and-remove without comparing function values
// (which is forbidden in Go).
type handlerEntry struct {
	fn func(payload any)
}

// coreEventBus returns the lazily-constructed singleton EventBus. The first
// caller wins; subsequent calls return the same instance. Constructed
// on-demand so disabled-only configurations don't allocate it.
func (a *App) coreEventBus() *eventBusCoreImpl {
	a.eventBusInitOnce.Do(func() {
		a.eventBus = &eventBusCoreImpl{
			app:         a,
			subscribers: make(map[string][]*handlerEntry),
		}
	})
	return a.eventBus
}

// Publish fans out the event to all registered Go-side subscribers and emits
// it to the frontend via Wails. Go handlers run synchronously in caller
// order; if you need async behavior, the handler can spawn its own
// goroutine.
func (e *eventBusCoreImpl) Publish(name string, payload any) error {
	if name == "" {
		return fmt.Errorf("eventbus: event name required")
	}

	// Snapshot the subscriber list under lock, then call handlers OUTSIDE
	// the lock so a handler that re-subscribes / unsubscribes doesn't
	// deadlock.
	e.mu.Lock()
	handlers := append([]*handlerEntry(nil), e.subscribers[name]...)
	e.mu.Unlock()

	for _, h := range handlers {
		h.fn(payload)
	}

	// Tee to the frontend, EXCEPT for `system:*` events. Those are
	// Go-side infrastructure (sleep/wake/network state for sync engines);
	// the frontend uses its own event names (e.g., `network:online` for
	// the same underlying state). Keeps the public Wails event surface
	// clean.
	if strings.HasPrefix(name, "system:") {
		return nil
	}
	if e.app.ctx != nil {
		wailsRuntime.EventsEmit(e.app.ctx, name, payload)
	}
	return nil
}

// Subscribe registers a handler for the given event name. Returns an
// Unsubscribe func that removes this exact registration (using pointer
// identity so the same handler function can be registered multiple times
// safely).
func (e *eventBusCoreImpl) Subscribe(name string, handler func(payload any)) (coreapi.Unsubscribe, error) {
	if name == "" {
		return nil, fmt.Errorf("eventbus: event name required")
	}
	if handler == nil {
		return nil, fmt.Errorf("eventbus: handler required")
	}

	entry := &handlerEntry{fn: handler}

	e.mu.Lock()
	e.subscribers[name] = append(e.subscribers[name], entry)
	e.mu.Unlock()

	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		handlers := e.subscribers[name]
		for i, h := range handlers {
			if h == entry {
				e.subscribers[name] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}, nil
}
