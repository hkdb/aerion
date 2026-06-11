package v1

// Core is the aggregate API handle passed to extensions at initialization.
// Extensions hold a reference to Core and access each surface as needed:
//
//	core.Mail().ListMessages(filter)
//	core.Auth().HTTPClient(accountID, scopes)
//	core.UI().RegisterRailTab(req)
//
// Extensions for which a capability is not granted will receive an
// ErrCapabilityDenied from the relevant method. For first-party extensions
// in Phase 1, all capabilities are implicitly granted.
type Core interface {
	Mail() Mail
	Composer() Composer
	Contacts() Contacts
	Auth() Auth
	Notifications() Notifications
	UI() UI
	Storage() Storage
	Events() EventBus
	Log() Logger

	// Extension returns the typed handle published by another extension via
	// its api.go interface, or (nil, false) if the extension is not enabled
	// or has not published a typed API.
	Extension(id string) (any, bool)
}
