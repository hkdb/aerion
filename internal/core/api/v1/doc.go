// Package v1 defines the Core API contract that Aerion extensions consume.
//
// Extensions never reach into core packages directly. Cross-extension and
// extension-to-core data access flows through the interfaces in this package:
// Mail, Composer, Contacts, Auth, Notifications, UI, Storage, EventBus.
//
// Stability promise: v1 is the stable API surface for Aerion v0.3.0+. Non-
// breaking additions (new methods, new event types, new fields with sensible
// zero values) may be added between minor releases. Breaking changes require
// introducing v2 and keeping v1 as a compatibility shim.
//
// Implementations live under internal/extensions/<category>/api.go (e.g.,
// internal/extensions/mail/api.go) and wrap existing core packages without
// changing their behavior.
package v1
