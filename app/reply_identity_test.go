package app

import (
	"testing"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/message"
)

// TestSelectReplyFromIdentity verifies the reply/forward From selection prefers
// the identity the original message was addressed to (To/Cc/Bcc), then the
// default identity, then the first — the fix for #325.
func TestSelectReplyFromIdentity(t *testing.T) {
	def := &account.Identity{Email: "me@x.com", IsDefault: true}
	alias := &account.Identity{Email: "alias@x.com"}
	full := []*account.Identity{def, alias}

	cases := []struct {
		name       string
		identities []*account.Identity
		msg        *message.Message
		want       string // expected identity email; "" means nil
	}{
		{"To match wins over default", full, &message.Message{ToList: "alias@x.com"}, "alias@x.com"},
		{"Cc match", full, &message.Message{ToList: "someone@else.com", CcList: "alias@x.com"}, "alias@x.com"},
		{"Bcc match", full, &message.Message{BccList: "alias@x.com"}, "alias@x.com"},
		{"case-insensitive and whitespace", full, &message.Message{ToList: "  ALIAS@X.COM "}, "alias@x.com"},
		{"no match falls back to default", full, &message.Message{ToList: "stranger@x.com"}, "me@x.com"},
		{"no default falls back to first", []*account.Identity{{Email: "a@x.com"}, {Email: "b@x.com"}}, &message.Message{ToList: "none@x.com"}, "a@x.com"},
		{"empty identities yields nil", nil, &message.Message{ToList: "alias@x.com"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectReplyFromIdentity(tc.identities, tc.msg)
			gotEmail := ""
			if got != nil {
				gotEmail = got.Email
			}
			if gotEmail != tc.want {
				t.Errorf("selectReplyFromIdentity = %q, want %q", gotEmail, tc.want)
			}
		})
	}
}
