// Package mail implements the coreapi.Mail interface as a wrapper over
// Aerion's existing core mail packages (internal/message, internal/folder).
// It exposes only the read-side surface in Phase 1; mutators are scaffolded
// as ErrUnimplemented until a Phase 2+ consumer needs them.
//
// This is the API the Mail extension presents to OTHER extensions. Mail
// itself doesn't consume this package — it uses the underlying stores
// directly.
package mail
