package v1

// MailEventType identifies a kind of mail event extensions can subscribe to.
type MailEventType string

const (
	MailEventNew          MailEventType = "new"           // new message arrived
	MailEventFlagsChanged MailEventType = "flags-changed" // flags updated
	MailEventExpunge      MailEventType = "expunge"       // message removed
	MailEventMoved        MailEventType = "moved"         // message moved between folders
)

// MailEvent is delivered to subscribers of Mail.SubscribeToMailEvents.
type MailEvent struct {
	Type      MailEventType `json:"type"`
	AccountID string        `json:"accountId"`
	FolderID  string        `json:"folderId"`
	MessageID string        `json:"messageId,omitempty"` // Aerion DB id when known
	UID       uint32        `json:"uid,omitempty"`       // IMAP UID when known
}

// Mail is the read/mutate/subscribe surface for messages and folders.
//
// Read methods are wired in Phase 1; mutators return ErrUnimplemented until
// a Phase 2+ consumer needs them.
type Mail interface {
	// Read
	ListMessages(filter MessageFilter) ([]Message, error)
	GetMessage(id string, includeBody bool) (*Message, error)
	ListFolders(accountID string) ([]Folder, error)
	GetSpecialFolder(accountID string, kind FolderKind) (*Folder, error)

	// Mutate — all paths reuse the existing UI-action pipeline so undo/sync/events
	// fire identically to a user action.
	MoveMessage(id string, destFolderID string) error
	Archive(id string) error
	Trash(id string) error
	SetFlags(id string, flags Flags) error
	AppendMessage(accountID string, folderID string, raw []byte, flags Flags) error

	// Events — extension receives events on the returned channel until cancel is called.
	SubscribeToMailEvents(types []MailEventType) (ch <-chan MailEvent, cancel Unsubscribe, err error)
}
