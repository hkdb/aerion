package v1

// NotifyClickAction identifies what happens when the user clicks a notification.
type NotifyClickAction struct {
	// Kind is one of:
	//   - "open-extension": switch the active extension to ExtensionID
	//   - "open-deep-link": switch to ExtensionID and open Path within it
	//   - "custom": invoke the extension-defined handler at HandlerID
	Kind        string `json:"kind"`
	ExtensionID string `json:"extensionId,omitempty"`
	Path        string `json:"path,omitempty"`      // for "open-deep-link"
	HandlerID   string `json:"handlerId,omitempty"` // for "custom"
}

// NotifyRequest describes a desktop notification to show.
type NotifyRequest struct {
	Title   string            `json:"title"`
	Body    string            `json:"body"`
	Icon    string            `json:"icon,omitempty"` // optional icon path or URL
	OnClick NotifyClickAction `json:"onClick,omitempty"`
}

// Notifications shows desktop notifications with extension-aware click handling.
type Notifications interface {
	Show(req NotifyRequest) error
}
