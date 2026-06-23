package v1

// EventBus is cross-extension loose coupling. An extension publishes a named
// event with arbitrary payload; any number of other extensions can subscribe
// without direct dependency on the publisher.
//
// Example: Calendar publishes "calendar.event.created" — Chat extension can
// subscribe and post a "Just added a meeting" message to a configured room.
// Neither knows about the other directly.
type EventBus interface {
	Publish(name string, payload any) error
	Subscribe(name string, handler func(payload any)) (Unsubscribe, error)
}
