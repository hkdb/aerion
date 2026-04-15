package app

import (
	"runtime/debug"

	"github.com/hkdb/aerion/internal/logging"
)

// recoverPanic is a deferred helper that catches panics in background goroutines
// to prevent the entire application from crashing. Usage:
//
//	go func() {
//	    defer recoverPanic("component", "operation")
//	    // ... work ...
//	}()
func recoverPanic(component, operation string) {
	if r := recover(); r != nil {
		log := logging.WithComponent(component)
		log.Error().
			Interface("panic", r).
			Str("operation", operation).
			Str("stack", string(debug.Stack())).
			Msg("Goroutine panicked (recovered)")
	}
}
