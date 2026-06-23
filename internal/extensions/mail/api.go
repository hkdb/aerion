package mail

import (
	"fmt"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/message"
)

// API implements coreapi.Mail by wrapping Aerion's existing message and folder
// stores. Read methods are fully implemented in Phase 1; mutators return
// ErrUnimplemented until a Phase 2+ consumer needs them.
type API struct {
	messageStore *message.Store
	folderStore  *folder.Store
}

// NewAPI constructs the Mail API wrapper. Both stores must be non-nil.
func NewAPI(messageStore *message.Store, folderStore *folder.Store) *API {
	return &API{messageStore: messageStore, folderStore: folderStore}
}

// ListMessages returns headers matching the filter. Phase 1 supports
// FolderID + Limit/Offset pagination via the existing ListByFolder query.
// Filter fields not yet wired (Unread, Starred, From, Since) are applied
// post-fetch in Go — adequate for moderate folder sizes, optimizable later.
func (a *API) ListMessages(filter coreapi.MessageFilter) ([]coreapi.Message, error) {
	if filter.FolderID == "" {
		return nil, fmt.Errorf("mail.ListMessages: FolderID is required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	headers, err := a.messageStore.ListByFolder(filter.FolderID, filter.Offset, limit)
	if err != nil {
		return nil, fmt.Errorf("mail.ListMessages: %w", err)
	}
	out := make([]coreapi.Message, 0, len(headers))
	for _, h := range headers {
		if filter.Unread != nil && h.IsRead == *filter.Unread {
			continue
		}
		if filter.Starred != nil && h.IsStarred != *filter.Starred {
			continue
		}
		if filter.From != "" && h.FromEmail != filter.From {
			continue
		}
		if !filter.Since.IsZero() && h.Date.Before(filter.Since) {
			continue
		}
		out = append(out, fromHeader(h))
	}
	return out, nil
}

// GetMessage returns a single message by id. When includeBody is false, the
// BodyHTML/BodyText fields on the returned Message are empty — the rest of
// the envelope is still populated.
func (a *API) GetMessage(id string, includeBody bool) (*coreapi.Message, error) {
	m, err := a.messageStore.Get(id)
	if err != nil {
		return nil, fmt.Errorf("mail.GetMessage: %w", err)
	}
	if m == nil {
		return nil, nil
	}
	out := fromMessage(m, includeBody)
	return &out, nil
}

// ListFolders returns all folders for the account.
func (a *API) ListFolders(accountID string) ([]coreapi.Folder, error) {
	folders, err := a.folderStore.List(accountID)
	if err != nil {
		return nil, fmt.Errorf("mail.ListFolders: %w", err)
	}
	out := make([]coreapi.Folder, 0, len(folders))
	for _, f := range folders {
		out = append(out, fromFolder(f))
	}
	return out, nil
}

// GetSpecialFolder returns the account's folder for the given kind (inbox,
// sent, drafts, etc.) via the internal folder store's type-based lookup.
func (a *API) GetSpecialFolder(accountID string, kind coreapi.FolderKind) (*coreapi.Folder, error) {
	f, err := a.folderStore.GetByType(accountID, folder.Type(kind))
	if err != nil {
		return nil, fmt.Errorf("mail.GetSpecialFolder: %w", err)
	}
	if f == nil {
		return nil, nil
	}
	out := fromFolder(f)
	return &out, nil
}

// MoveMessage is scaffolded; Phase 2+ wires this through the existing
// app/actions.go MoveToFolder pipeline so undo/sync/events fire identically
// to a user action.
func (a *API) MoveMessage(id string, destFolderID string) error {
	return coreapi.ErrUnimplemented
}

// Archive is scaffolded; Phase 2+ wires through app/actions.go.
func (a *API) Archive(id string) error { return coreapi.ErrUnimplemented }

// Trash is scaffolded; Phase 2+ wires through app/actions.go.
func (a *API) Trash(id string) error { return coreapi.ErrUnimplemented }

// SetFlags is scaffolded; Phase 2+ wires through app/actions.go.
func (a *API) SetFlags(id string, flags coreapi.Flags) error { return coreapi.ErrUnimplemented }

// AppendMessage is scaffolded; Phase 2+ wires through the IMAP append path
// used by the local mail-filter extension.
func (a *API) AppendMessage(accountID string, folderID string, raw []byte, flags coreapi.Flags) error {
	return coreapi.ErrUnimplemented
}

// SubscribeToMailEvents is scaffolded; needs an event-bus wiring (Phase 2+)
// that fans Aerion's existing sync events out to extension subscribers.
func (a *API) SubscribeToMailEvents(types []coreapi.MailEventType) (<-chan coreapi.MailEvent, coreapi.Unsubscribe, error) {
	return nil, func() {}, coreapi.ErrUnimplemented
}
