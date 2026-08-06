package account

import "testing"

func TestEffectiveSMTPAuthMechanism(t *testing.T) {
	tests := []struct {
		name string
		acc  Account
		want AuthMechanism
	}{
		{
			name: "same-as-incoming creds follow IMAP mechanism",
			acc:  Account{IMAPAuthMechanism: AuthMechLogin, SMTPAuthMechanism: AuthMechAuto},
			want: AuthMechLogin,
		},
		{
			name: "separate creds use SMTP mechanism",
			acc:  Account{SMTPUsername: "smtp-user", IMAPAuthMechanism: AuthMechLogin, SMTPAuthMechanism: AuthMechPlain},
			want: AuthMechPlain,
		},
		{
			name: "defaults stay auto",
			acc:  Account{IMAPAuthMechanism: AuthMechAuto, SMTPAuthMechanism: AuthMechAuto},
			want: AuthMechAuto,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.acc.EffectiveSMTPAuthMechanism()
			if got != tt.want {
				t.Errorf("EffectiveSMTPAuthMechanism() = %q, want %q", got, tt.want)
			}
		})
	}
}
