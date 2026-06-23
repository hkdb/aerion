// Package compose implements the coreapi.Composer interface by building a
// mailto: URL from a ComposeRequest and delegating to Aerion's existing
// composer-window opener. The launcher is passed in via interface to avoid
// importing the app package (which would cycle).
//
// Phase 1 supports the common path: open a fresh composer with prefilled
// To/Cc/Bcc/Subject/Body. Attachments and ReplyTo are returned as
// ErrUnimplemented; Phase 2+ adds them when a consumer needs them.
package compose
