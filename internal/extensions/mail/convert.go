package mail

import (
	"encoding/json"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/message"
)

// fromHeader converts a stored MessageHeader into the API-surface Message.
// Body fields are left zero — ListMessages returns headers only; full bodies
// require GetMessage(id, includeBody=true).
func fromHeader(h *message.MessageHeader) coreapi.Message {
	return coreapi.Message{
		ID:             h.ID,
		AccountID:      h.AccountID,
		FolderID:       h.FolderID,
		UID:            h.UID,
		Subject:        h.Subject,
		From:           coreapi.Address{Name: h.FromName, Email: h.FromEmail},
		Date:           h.Date,
		Snippet:        h.Snippet,
		HasAttachments: h.HasAttachments,
		Flags: coreapi.Flags{
			Seen:    h.IsRead,
			Flagged: h.IsStarred,
		},
	}
}

// fromMessage converts a fully-loaded internal Message into the API-surface
// Message. Address fields stored as JSON arrays in the internal store are
// unmarshalled into typed Address slices for the API.
func fromMessage(m *message.Message, includeBody bool) coreapi.Message {
	out := coreapi.Message{
		ID:             m.ID,
		AccountID:      m.AccountID,
		FolderID:       m.FolderID,
		UID:            m.UID,
		MessageID:      m.MessageID,
		InReplyTo:      m.InReplyTo,
		ThreadID:       m.ThreadID,
		Subject:        m.Subject,
		From:           coreapi.Address{Name: m.FromName, Email: m.FromEmail},
		ReplyTo:        m.ReplyTo,
		Date:           m.Date,
		Snippet:        m.Snippet,
		Size:           m.Size,
		HasAttachments: m.HasAttachments,
		Flags: coreapi.Flags{
			Seen:      m.IsRead,
			Flagged:   m.IsStarred,
			Answered:  m.IsAnswered,
			Draft:     m.IsDraft,
			Deleted:   m.IsDeleted,
			Forwarded: m.IsForwarded,
		},
	}
	out.To = decodeAddressList(m.ToList)
	out.Cc = decodeAddressList(m.CcList)
	out.Bcc = decodeAddressList(m.BccList)
	if includeBody {
		out.BodyHTML = m.BodyHTML
		out.BodyText = m.BodyText
	}
	return out
}

// decodeAddressList parses the JSON address list format used in
// internal/message storage into the API's typed Address slice. Returns nil
// for empty / malformed JSON (the store occasionally has legacy formats —
// extensions should treat absent address lists as empty).
func decodeAddressList(s string) []coreapi.Address {
	if s == "" {
		return nil
	}
	// internal/message stores addresses as JSON arrays of either
	// {"name", "email"} (modern) or {"name", "address"} (some legacy paths).
	// Try both shapes.
	type modern struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	var asModern []modern
	if err := json.Unmarshal([]byte(s), &asModern); err == nil {
		out := make([]coreapi.Address, 0, len(asModern))
		for _, a := range asModern {
			if a.Email == "" {
				continue
			}
			out = append(out, coreapi.Address{Name: a.Name, Email: a.Email})
		}
		if len(out) > 0 {
			return out
		}
	}
	type legacy struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	var asLegacy []legacy
	if err := json.Unmarshal([]byte(s), &asLegacy); err == nil {
		out := make([]coreapi.Address, 0, len(asLegacy))
		for _, a := range asLegacy {
			if a.Address == "" {
				continue
			}
			out = append(out, coreapi.Address{Name: a.Name, Email: a.Address})
		}
		return out
	}
	return nil
}

// fromFolder converts an internal folder into the API-surface Folder.
func fromFolder(f *folder.Folder) coreapi.Folder {
	return coreapi.Folder{
		ID:          f.ID,
		AccountID:   f.AccountID,
		Name:        f.Name,
		Path:        f.Path,
		Kind:        coreapi.FolderKind(f.Type),
		ParentID:    f.ParentID,
		Subscribed:  f.Subscribed,
		TotalCount:  f.TotalCount,
		UnreadCount: f.UnreadCount,
	}
}
