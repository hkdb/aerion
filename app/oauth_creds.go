package app

import (
	"fmt"

	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/oauth2"
)

// AuthContextInfo describes a single authenticated identity (email account or
// standalone contacts source) that the Contacts extension's write-access
// picker can attach a new write grant to. Enumerated by ListAuthContextsForProvider.
//
// Standalone contacts sources are first-class auth contexts even though they
// don't have an entry in the `accounts` table — their OAuth tokens are stored
// against the source id directly.
type AuthContextInfo struct {
	// Kind is "mail" or "standalone-contacts". Drives which backend method
	// the picker's confirm calls (incremental consent on a mail account vs.
	// incremental consent / fresh OAuth against a contacts source).
	Kind string `json:"kind"`
	// Identifier is the account_id (for "mail") or source_id (for
	// "standalone-contacts") the picker passes back to the bridge.
	Identifier string `json:"identifier"`
	// Email is the user-facing identifier shown in the picker.
	Email string `json:"email"`
	// Label is a short tag rendered next to the email — "Mail" or "Contacts".
	Label string `json:"label"`
}

// OAuthCredsStatus is the metadata returned by GetOAuthCredsStatus. Secret
// values themselves NEVER leave the credentials store via this surface — only
// presence flags + a short fingerprint of the client_id for visual
// confirmation in the Settings UI.
type OAuthCredsStatus struct {
	// ConfigID is the slot identifier (e.g., "google-mail", "google-contacts").
	ConfigID string `json:"configId"`
	// HasUserOverride is true when the user has supplied their own creds for
	// this slot via Settings → OAuth Credentials.
	HasUserOverride bool `json:"hasUserOverride"`
	// HasShipped is true when shipped/built-in creds for this slot are
	// populated (the build-time vars are non-empty).
	HasShipped bool `json:"hasShipped"`
	// ClientIDFingerprint is the last 4 characters of the currently-active
	// client_id (whichever wins resolution — user override beats shipped).
	// Empty when no creds exist at all. Used by the UI for visual
	// confirmation that the saved value is what the user expects.
	ClientIDFingerprint string `json:"clientIdFingerprint"`
}

// GetOAuthCredsStatus reports whether user-supplied AND/OR shipped creds are
// present for the given client config id. Never exposes the secret values.
//
// Wails-bound. Called by Settings → Accounts → OAuth Credentials section AND
// by each extension's settings dialog (when checking its own slots).
func (a *App) GetOAuthCredsStatus(configID string) (OAuthCredsStatus, error) {
	status := OAuthCredsStatus{ConfigID: configID}

	if a.credStore != nil {
		status.HasUserOverride = a.credStore.HasUserClientCreds(configID)
	}

	// Has shipped creds? Temporarily unset UserOverrideLookup so we can
	// query only the registered providers' shipped values.
	saved := oauth2.UserOverrideLookup
	oauth2.UserOverrideLookup = nil
	_, hasShipped := oauth2.ClientConfigForID(configID)
	oauth2.UserOverrideLookup = saved
	status.HasShipped = hasShipped

	activeCreds, ok := oauth2.ClientConfigForID(configID)
	status.ClientIDFingerprint = fingerprintClientID(ok, activeCreds.ClientID)

	return status, nil
}

func fingerprintClientID(found bool, id string) string {
	if !found || id == "" {
		return ""
	}
	if len(id) > 4 {
		return "…" + id[len(id)-4:]
	}
	return id
}

// SetOAuthCreds saves user-supplied OAuth client credentials for the given
// config id. Overrides any shipped/built-in values for that slot.
//
// Wails-bound.
func (a *App) SetOAuthCreds(configID, clientID, clientSecret string) error {
	if a.credStore == nil {
		return fmt.Errorf("credential store not initialized")
	}
	return a.credStore.SetUserClientCreds(configID, clientID, clientSecret)
}

// ClearOAuthCreds removes a user-supplied override for the given config id,
// reverting that slot to its shipped value (or empty if none was shipped).
//
// Wails-bound.
func (a *App) ClearOAuthCreds(configID string) error {
	if a.credStore == nil {
		return fmt.Errorf("credential store not initialized")
	}
	return a.credStore.ClearUserClientCreds(configID)
}

// ListAuthContextsForProvider returns the existing authenticated identities
// (mail accounts + standalone contacts sources) that match the given OAuth
// provider. Used by the Contacts extension's write-access picker to let the
// user attach a write grant to one of their EXISTING reads, rather than
// adding a new account from inside the extension (which Aerion's design
// forbids — new accounts always come through core setup paths).
//
// Wails-bound. Returns an empty slice when nothing matches — the picker
// renders an "Add a Google account in Mail or Contacts first" empty state.
//
// `provider` is "google" or "microsoft".
func (a *App) ListAuthContextsForProvider(provider string) ([]AuthContextInfo, error) {
	if provider != "google" && provider != "microsoft" {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}

	var out []AuthContextInfo

	// Mail accounts. We discover their provider via the existing OAuth
	// tokens table; account IDs without OAuth tokens (basic-auth IMAP)
	// don't match and are skipped.
	if a.accountStore != nil && a.credStore != nil {
		accounts, err := a.accountStore.List()
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		for _, acc := range accounts {
			if acc == nil {
				continue
			}
			tokenProvider, err := a.credStore.GetOAuthProvider(acc.ID)
			if err != nil || tokenProvider == "" {
				continue
			}
			if tokenProvider != provider {
				continue
			}
			out = append(out, AuthContextInfo{
				Kind:       "mail",
				Identifier: acc.ID,
				Email:      acc.Email,
				Label:      "Mail",
			})
		}
	}

	// Standalone contacts sources — carddav sources with AccountID == nil
	// and Type matching the provider.
	if a.carddavStore != nil {
		sources, err := a.carddavStore.ListSources()
		if err != nil {
			return nil, fmt.Errorf("list contact sources: %w", err)
		}
		for _, s := range sources {
			if s == nil {
				continue
			}
			if s.AccountID != nil && *s.AccountID != "" {
				continue // linked to a mail account — already covered above
			}
			if string(s.Type) != provider {
				continue
			}
			email := contactSourceEmail(s)
			if email == "" {
				continue
			}
			out = append(out, AuthContextInfo{
				Kind:       "standalone-contacts",
				Identifier: s.ID,
				Email:      email,
				Label:      "Contacts",
			})
		}
	}

	return out, nil
}

// contactSourceEmail extracts the user-facing email for a standalone contacts
// source. Standalone sources don't carry the email as a structured field —
// it's stored against the source's username, which is set at
// CompleteContactSourceOAuthSetup time to the email returned by Google/MS.
func contactSourceEmail(s *carddav.Source) string {
	if s == nil {
		return ""
	}
	return s.Username
}

