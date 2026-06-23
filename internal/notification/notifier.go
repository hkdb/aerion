// Package notification provides cross-platform desktop notification support
// with click handling for navigating to specific content.
package notification

import "context"

// ClickHandler is called when a notification is clicked
type ClickHandler func(data NotificationData)

// NotificationData contains the context for a notification click. Mail
// notifications populate AccountID/FolderID/ThreadID; extension notifications
// fired via coreapi.Notifications populate ExtensionID + Path. The dispatcher
// in app/ routes based on which fields are set — extension-routed clicks
// switch the rail tab and emit a path-scoped event; mail clicks fall through
// to the existing mail navigation flow.
type NotificationData struct {
	// Mail-routed fields
	AccountID string
	FolderID  string
	ThreadID  string

	// Extension-routed fields (set by coreapi.Notifications consumers).
	// Both empty = mail-routed click.
	ExtensionID string
	Path        string
}

// Notification represents a desktop notification to be shown
type Notification struct {
	Title   string
	Body    string
	Icon    string
	Data    NotificationData
}

// Notifier provides cross-platform notification support with click handling
type Notifier interface {
	// Start begins listening for notification events
	Start(ctx context.Context) error

	// Stop stops the notifier and cleans up resources
	Stop()

	// Show displays a notification and returns its ID
	Show(n Notification) (uint32, error)

	// SetClickHandler sets the callback for notification clicks
	SetClickHandler(handler ClickHandler)
}

// New creates a platform-specific Notifier
func New(appName string, useDirectDBus bool) Notifier {
	return newPlatformNotifier(appName, useDirectDBus)
}
