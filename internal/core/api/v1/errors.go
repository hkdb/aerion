package v1

import (
	"errors"
	"fmt"
)

// Sentinel errors. Extensions should errors.Is(err, v1.ErrXxx) to detect them.
var (
	// ErrDisabled is returned by API methods when the underlying extension or
	// feature is disabled. Callers should treat this as a benign "feature off"
	// signal rather than a failure.
	ErrDisabled = errors.New("extension or feature is disabled")

	// ErrCapabilityDenied is returned when an extension calls an API it has
	// not been granted the capability for. For first-party extensions in
	// Phase 1, this should never occur (all-or-nothing grants).
	ErrCapabilityDenied = errors.New("capability denied")

	// ErrAccountNotFound is returned when an API call references an account
	// ID that does not exist (or has been deleted).
	ErrAccountNotFound = errors.New("account not found")

	// ErrUnimplemented is returned by API methods that are scaffolded but
	// not implemented in the current release. Phase 1 returns this for Mail
	// mutators, event subscriptions, the event bus, and UI registrations.
	ErrUnimplemented = errors.New("not implemented in this release")
)

// ErrConflict signals that a mutation lost a race with concurrent state on
// the source's authoritative side (e.g., a CardDAV server returned 412 for a
// PUT/DELETE because its current ETag differs from ours). The local cache has
// been refreshed from the source to mirror current state; the user's edit was
// dropped. The Wails layer translates this into an event the UI handles by
// toast + reload.
type ErrConflict struct {
	ContactID string // the record id (UUID) the conflict was on
	Message   string // human-readable detail; safe to display
}

func (e *ErrConflict) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("conflict on contact %s", e.ContactID)
	}
	return fmt.Sprintf("conflict on contact %s: %s", e.ContactID, e.Message)
}

// ErrAdditionalConsentRequired signals that the extension's request needs
// additional OAuth consent from the user before it can succeed. The host (not
// the extension) handles the consent flow and retries.
type ErrAdditionalConsentRequired struct {
	AccountID      string
	ClientConfigID ClientConfigID
	MissingScopes  []AuthScope
}

func (e *ErrAdditionalConsentRequired) Error() string {
	return fmt.Sprintf("additional consent required for account %s under %s: %d scope(s) missing",
		e.AccountID, e.ClientConfigID, len(e.MissingScopes))
}
