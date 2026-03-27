package app

import (
	"fmt"

	"github.com/hkdb/aerion/internal/account"
)

// resolveSMTPAuthUsername returns the SMTP auth username for an account.
// For Microsoft shared mailboxes, authenticate as the source user while
// preserving the shared mailbox address in the message headers/envelope.
func resolveSMTPAuthUsername(store *account.Store, acc *account.Account) (string, error) {
	if acc == nil {
		return "", fmt.Errorf("account is nil")
	}

	if acc.Provider != "microsoft" || acc.Kind != account.AccountKindShared || acc.OAuthSourceAccountID == "" {
		return acc.Username, nil
	}

	source, err := store.Get(acc.OAuthSourceAccountID)
	if err != nil {
		return "", fmt.Errorf("failed to get source account for SMTP auth: %w", err)
	}
	if source == nil {
		return "", fmt.Errorf("source account not found for SMTP auth: %s", acc.OAuthSourceAccountID)
	}
	if source.Username == "" {
		return "", fmt.Errorf("source account has no SMTP auth username: %s", source.ID)
	}

	return source.Username, nil
}
