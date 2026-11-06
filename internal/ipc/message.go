package ipc

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Message type constants for Composer -> Main communication
const (
	// TypeMessageSent indicates an email was successfully sent
	TypeMessageSent = "message_sent"

	// TypeDraftSaved indicates a draft was saved or updated
	TypeDraftSaved = "draft_saved"

	// TypeDraftDeleted indicates a draft was deleted
	TypeDraftDeleted = "draft_deleted"

	// TypeComposerReady indicates the composer finished loading
	TypeComposerReady = "composer_ready"

	// TypeComposerClosed indicates the composer window was closed
	TypeComposerClosed = "composer_closed"
)

// Message type constants for Main -> Composer (broadcast) communication
const (
	// TypeThemeChanged indicates the selected application theme changed
	TypeThemeChanged = "theme_changed"

	// TypeCustomCSSChanged indicates the user stylesheet changed
	TypeCustomCSSChanged = "custom_css_changed"

	// TypeShutdown indicates the main app is closing
	TypeShutdown = "shutdown"
)

// Bidirectional message type constants
const (
	// TypePing is a health check request
	TypePing = "ping"

	// TypePong is a health check response
	TypePong = "pong"

	// TypeAuth is the authentication message (first message from client)
	TypeAuth = "auth"

	// TypeAuthResponse is the server's response to authentication
	TypeAuthResponse = "auth_response"

	// TypeError indicates an error occurred
	TypeError = "error"
)

// MessageSentPayload is the payload for TypeMessageSent messages.
type MessageSentPayload struct {
	AccountID string `json:"account_id"`
	FolderID  int64  `json:"folder_id"`
}

// DraftSavedPayload is the payload for TypeDraftSaved messages.
type DraftSavedPayload struct {
	AccountID string `json:"account_id"`
	DraftID   string `json:"draft_id"`
}

// DraftDeletedPayload is the payload for TypeDraftDeleted messages.
type DraftDeletedPayload struct {
	AccountID string `json:"account_id"`
}

// ComposerClosedPayload is the payload for TypeComposerClosed messages.
type ComposerClosedPayload struct {
	DraftID *int64 `json:"draft_id,omitempty"`
}

// ThemeChangedPayload is the payload for TypeThemeChanged messages.
type ThemeChangedPayload struct {
	Theme string `json:"theme"`
}

// CustomCSSChangedPayload is the payload for TypeCustomCSSChanged messages.
type CustomCSSChangedPayload struct {
	CSS string `json:"css"`
}

// ShutdownPayload is the payload for TypeShutdown messages.
type ShutdownPayload struct {
	Reason string `json:"reason,omitempty"`
}

// AuthPayload is the payload for TypeAuth messages.
type AuthPayload struct {
	Token string `json:"token"`
}

// AuthResponsePayload is the payload for TypeAuthResponse messages.
type AuthResponsePayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// NewMessage creates a new message with a unique ID.
func NewMessage(msgType string, payload interface{}) (Message, error) {
	msg := Message{
		ID:   uuid.New().String(),
		Type: msgType,
	}

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return Message{}, err
		}
		msg.Payload = data
	}

	return msg, nil
}

// NewReply creates a reply to an existing message.
func NewReply(original Message, msgType string, payload interface{}) (Message, error) {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		return Message{}, err
	}
	msg.ReplyTo = original.ID
	return msg, nil
}

// NewErrorReply creates an error reply to an existing message.
func NewErrorReply(original Message, errMsg string) Message {
	return Message{
		ID:      uuid.New().String(),
		Type:    TypeError,
		ReplyTo: original.ID,
		Error:   errMsg,
	}
}

// ParsePayload unmarshals the message payload into the provided struct.
func (m *Message) ParsePayload(v interface{}) error {
	if m.Payload == nil {
		return nil
	}
	return json.Unmarshal(m.Payload, v)
}
