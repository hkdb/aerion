package ipc

import (
	"testing"
)

func TestNewMessage(t *testing.T) {
	msg, err := NewMessage(TypePing, nil)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if msg.Type != TypePing {
		t.Fatalf("expected type %q, got %q", TypePing, msg.Type)
	}
	if msg.Payload != nil {
		t.Fatalf("expected nil payload, got %v", msg.Payload)
	}
}

func TestNewMessageWithPayload(t *testing.T) {
	payload := MessageSentPayload{AccountID: "a1", FolderID: 42}
	msg, err := NewMessage(TypeMessageSent, payload)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}
	if msg.Type != TypeMessageSent {
		t.Fatalf("expected type %q, got %q", TypeMessageSent, msg.Type)
	}
	if msg.Payload == nil {
		t.Fatal("expected non-nil payload")
	}

	var parsed MessageSentPayload
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}
	if parsed.AccountID != "a1" {
		t.Fatalf("expected AccountID %q, got %q", "a1", parsed.AccountID)
	}
	if parsed.FolderID != 42 {
		t.Fatalf("expected FolderID %d, got %d", 42, parsed.FolderID)
	}
}

func TestCustomCSSChangedPayload(t *testing.T) {
	want := ":root { --primary: red; }"
	msg, err := NewMessage(TypeCustomCSSChanged, CustomCSSChangedPayload{CSS: want})
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var payload CustomCSSChangedPayload
	if err := msg.ParsePayload(&payload); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}
	if payload.CSS != want {
		t.Fatalf("parsed CSS = %q, want %q", payload.CSS, want)
	}
}

func TestNewReply(t *testing.T) {
	original, err := NewMessage(TypePing, nil)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	reply, err := NewReply(original, TypePong, nil)
	if err != nil {
		t.Fatalf("NewReply failed: %v", err)
	}
	if reply.ReplyTo != original.ID {
		t.Fatalf("expected ReplyTo %q, got %q", original.ID, reply.ReplyTo)
	}
	if reply.Type != TypePong {
		t.Fatalf("expected type %q, got %q", TypePong, reply.Type)
	}
}

func TestNewErrorReply(t *testing.T) {
	original, err := NewMessage(TypePing, nil)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	errReply := NewErrorReply(original, "something went wrong")
	if errReply.ReplyTo != original.ID {
		t.Fatalf("expected ReplyTo %q, got %q", original.ID, errReply.ReplyTo)
	}
	if errReply.Type != TypeError {
		t.Fatalf("expected type %q, got %q", TypeError, errReply.Type)
	}
	if errReply.Error != "something went wrong" {
		t.Fatalf("expected error %q, got %q", "something went wrong", errReply.Error)
	}
}

func TestParsePayload(t *testing.T) {
	payload := MessageSentPayload{AccountID: "acc123", FolderID: 7}
	msg, err := NewMessage(TypeMessageSent, payload)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var parsed MessageSentPayload
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}
	if parsed.AccountID != "acc123" {
		t.Fatalf("expected AccountID %q, got %q", "acc123", parsed.AccountID)
	}
	if parsed.FolderID != 7 {
		t.Fatalf("expected FolderID %d, got %d", 7, parsed.FolderID)
	}
}

func TestParsePayloadNil(t *testing.T) {
	msg, err := NewMessage(TypePing, nil)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var parsed MessageSentPayload
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("expected nil error for nil payload, got: %v", err)
	}
}
