package sync

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hkdb/aerion/internal/message"
)

func TestMissingUIDs(t *testing.T) {
	requested := []uint32{5, 4, 3, 2, 1}
	saved := []*message.Message{{UID: 5}}

	got := missingUIDs(requested, saved)
	want := []uint32{4, 3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("missingUIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missingUIDs = %v, want %v", got, want)
		}
	}

	all := []*message.Message{{UID: 5}, {UID: 4}, {UID: 3}, {UID: 2}, {UID: 1}}
	if got := missingUIDs(requested, all); len(got) != 0 {
		t.Fatalf("missingUIDs with everything saved = %v, want empty", got)
	}
}

func TestIsEnvelopeDecodeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"mailfence bare-space subject (#209)", errors.New(`in response-data: in envelope: imapwire: expected string, got " "`), true},
		{"malformed address list inside envelope", errors.New(`in response-data: in envelope: in env-from: imapwire: expected SP, got "("`), true},
		{"plain connection error", errors.New("use of closed network connection"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isEnvelopeDecodeError(tc.err); got != tc.want {
			t.Errorf("%s: isEnvelopeDecodeError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnvelopeDecodeErrorUnwraps(t *testing.T) {
	inner := errors.New(`in envelope: imapwire: expected string, got " "`)
	wrapped := fmt.Errorf("header batch: %w", &envelopeDecodeError{Err: inner, Missing: []uint32{7, 8}})

	var envErr *envelopeDecodeError
	if !errors.As(wrapped, &envErr) {
		t.Fatal("errors.As failed to find envelopeDecodeError through fmt.Errorf wrapping")
	}
	if len(envErr.Missing) != 2 || envErr.Missing[0] != 7 {
		t.Fatalf("Missing = %v, want [7 8]", envErr.Missing)
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("errors.Is failed to reach the inner go-imap error via Unwrap")
	}
}
