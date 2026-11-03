package app

import (
	"testing"

	"github.com/hkdb/aerion/internal/message"
)

func TestUnreadIn(t *testing.T) {
	msgs := []*message.Message{{IsRead: false}, {IsRead: true}, nil, {IsRead: false}}
	if got := unreadIn(msgs); got != 2 {
		t.Fatalf("unreadIn = %d, want 2", got)
	}
	if got := unreadIn(nil); got != 0 {
		t.Fatalf("unreadIn(nil) = %d, want 0", got)
	}
}

func TestReadFlipDelta(t *testing.T) {
	mixed := []*message.Message{{IsRead: false}, {IsRead: true}, {IsRead: false}}
	cases := []struct {
		name   string
		msgs   []*message.Message
		isRead bool
		want   int
	}{
		{"mark read: two unread flip", mixed, true, -2},
		{"mark unread: one read flips", mixed, false, 1},
		{"already read, mark read", []*message.Message{{IsRead: true}, {IsRead: true}}, true, 0},
		{"already unread, mark unread", []*message.Message{{IsRead: false}}, false, 0},
		{"empty", nil, true, 0},
	}
	for _, tc := range cases {
		if got := readFlipDelta(tc.msgs, tc.isRead); got != tc.want {
			t.Errorf("%s: readFlipDelta = %d, want %d", tc.name, got, tc.want)
		}
	}
}
