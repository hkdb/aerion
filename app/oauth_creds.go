package app

import (
	"fmt"
	"strings"

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

	// Probe shipped creds via the bypass helper so we don't mutate the
	// package-level lookup hooks (which would race against every
	// concurrent OAuth refresh reading them on the sync path).
	_, hasShipped := oauth2.ShippedClientConfigForID(configID)
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

// OAuthCredsChoice is one selectable option in the picker UI. The user picks
// exactly one choice per slot; the chosen ID determines which credentials
// flow at OAuth time. Choices have stable IDs for persistence; labels are
// user-visible strings.
type OAuthCredsChoice struct {
	// ID is the stable identifier ("custom" | "aerion-shipped" | "aerion-mail").
	// Stored in the credentials store as the user's pick for this slot.
	ID string `json:"id"`
	// Label is the user-visible string shown in the dropdown.
	Label string `json:"label"`
}

// OAuthCredsChoices is the payload returned by GetOAuthCredsChoices. It
// supersedes OAuthCredsStatus's binary flags with an enumerated list of
// what the user can pick from for a given slot, plus the current selection.
type OAuthCredsChoices struct {
	// ConfigID is the slot id this payload describes.
	ConfigID string `json:"configId"`
	// Choices in the order the picker should render them.
	Choices []OAuthCredsChoice `json:"choices"`
	// Current is the ID of the currently-selected choice. Reflects what
	// ClientConfigForID would return today.
	Current string `json:"current"`
	// HasUserOverride reports whether a saved Custom credentials row
	// exists for the slot — INDEPENDENT of whether Custom is the
	// currently-active choice. Frontend uses this to drive:
	//   - placeholder copy ("Leave empty to keep current" vs "Paste …")
	//   - "Clear saved Custom credentials" button visibility
	//   - the "You also have a saved Custom override" hint when the
	//     user is currently on an Aerion choice but Custom is still on file
	// Under the pre-active-choice architecture this was equivalent to
	// `Current == "custom"` because the picker wiped the row when the
	// user switched away from Custom. With explicit active-choice
	// routing the row survives picker switches, so the two conditions
	// are now independent.
	HasUserOverride bool `json:"hasUserOverride"`
	// ClientIDFingerprint mirrors OAuthCredsStatus.ClientIDFingerprint —
	// last 4 of the currently-active client_id, for visual confirmation.
	ClientIDFingerprint string `json:"clientIdFingerprint"`
}

// GetOAuthCredsChoices enumerates the picker options available for the given
// slot. The choice set depends on:
//
//   - Whether the slot's own shipped creds resolve to non-empty (always
//     adds an "aerion-shipped" option labeled per provider).
//   - Whether the extension's manifest declares the OAuth provider of this
//     slot in first_party_uses_core_for_scopes AND the mail slot has
//     shipped creds (adds "aerion-mail" — reuse mail's verified client).
//
// extensionID is the manifest id ("contacts", "calendar"); pass "" when
// the caller is mail's own settings UI (no manifest, no aerion-mail option).
//
// Wails-bound.
func (a *App) GetOAuthCredsChoices(configID, extensionID string) (OAuthCredsChoices, error) {
	out := OAuthCredsChoices{ConfigID: configID}

	// Always offer Custom.
	out.Choices = append(out.Choices, OAuthCredsChoice{ID: "custom", Label: "Custom"})

	// Probe shipped creds via the bypass helper so we don't mutate the
	// package-level lookup hooks (which would race against every
	// concurrent OAuth refresh reading them on the sync path).
	_, hasShipped := oauth2.ShippedClientConfigForID(configID)

	if hasShipped {
		out.Choices = append(out.Choices, OAuthCredsChoice{
			ID:    "aerion-shipped",
			Label: shippedLabelForSlot(configID),
		})
	}

	// Mail-reuse option ("Aerion - Google"): only when the extension's
	// manifest declares first_party_uses_core_for_scopes for THIS provider
	// AND the mail slot has shipped creds. Mail's own settings call us
	// with extensionID="" and skip this whole branch.
	//
	// Skipped entirely for Microsoft — `microsoft-contacts` and
	// `microsoft-calendar` are core-registered aliases of `microsoft-mail`
	// (microsoft-mail's client IS the consolidated Microsoft client), so
	// showing a separate "use mail's client" option would be a redundant
	// duplicate of the "Aerion - Microsoft" shipped choice already added
	// above.
	if extensionID != "" && providerFromConfigID(configID) == "google" {
		if a.extensionDeclaresCoreScopesForProvider(extensionID, "google") {
			const mailSlot = "google-mail"
			if mailSlot != configID {
				_, mailHasShipped := oauth2.ShippedClientConfigForID(mailSlot)
				if mailHasShipped {
					out.Choices = append(out.Choices, OAuthCredsChoice{
						ID:    "aerion-mail",
						Label: "Aerion - Google",
					})
				}
			}
		}
	}

	// Determine the Current selection from credStore state.
	out.Current = a.resolveCurrentChoice(configID)
	// Independent of the active choice — the row may exist while the
	// user is currently routed to Aerion-shipped or Aerion-mail.
	out.HasUserOverride = a.credStore != nil && a.credStore.HasUserClientCreds(configID)

	// Fingerprint of whatever currently resolves.
	activeCreds, ok := oauth2.ClientConfigForID(configID)
	out.ClientIDFingerprint = fingerprintClientID(ok, activeCreds.ClientID)

	return out, nil
}

// SetOAuthCredsChoice persists the user's picker selection for the slot.
// The active-choice marker is what the resolver consults; saved
// credentials and alias rows are LEFT IN PLACE so switching between
// options is non-destructive.
//
//   - "custom"          → record marker. Caller invokes SetOAuthCreds
//                          separately (via the editor's Save button) to
//                          write or replace the actual credentials.
//   - "aerion-shipped"  → record marker. The resolver skips override +
//                          alias and routes to the slot's own shipped
//                          creds. Any user_oauth_clients / alias rows
//                          remain in storage so switching back to Custom
//                          restores the user's saved values.
//   - "aerion-mail"     → record marker AND ensure the alias row exists.
//                          The user_oauth_clients row is preserved for
//                          the same round-trip restore reason.
//
// The only path that actually DELETES the user's stored credentials is
// the explicit ClearOAuthCreds Wails method ("Clear saved Custom
// credentials" in the editor).
//
// Wails-bound.
func (a *App) SetOAuthCredsChoice(configID, choiceID string) error {
	if a.credStore == nil {
		return fmt.Errorf("credential store not initialized")
	}
	switch choiceID {
	case "custom":
		return a.credStore.SetOAuthActiveChoice(configID, "custom")
	case "aerion-shipped":
		return a.credStore.SetOAuthActiveChoice(configID, "aerion-shipped")
	case "aerion-mail":
		provider := providerFromConfigID(configID)
		if provider == "" {
			return fmt.Errorf("cannot derive provider from config id %q", configID)
		}
		// Ensure the alias row points to the right target so the resolver
		// can recurse correctly on the mail slot.
		if err := a.credStore.SetOAuthSlotAlias(configID, provider+"-mail"); err != nil {
			return err
		}
		return a.credStore.SetOAuthActiveChoice(configID, "aerion-mail")
	}
	return fmt.Errorf("unknown choice id %q", choiceID)
}

// resolveCurrentChoice reports which picker option is effectively active
// for the slot. Reads the explicit active-choice marker first; falls back
// to inferring the choice from row presence for slots that haven't had
// the picker touched since upgrading from a pre-marker version (the
// inference branch will stop running for a slot the first time
// SetOAuthCredsChoice writes its marker).
//
// Returned ID is one of "custom" / "aerion-mail" / "aerion-shipped".
func (a *App) resolveCurrentChoice(configID string) string {
	if a.credStore == nil {
		return "aerion-shipped"
	}
	if marker, err := a.credStore.GetOAuthActiveChoice(configID); err == nil && marker != "" {
		return marker
	}
	// Backward-compat inference (pre-marker installs). First touch of
	// the picker post-upgrade records an explicit marker and this branch
	// stops running for that slot.
	if a.credStore.HasUserClientCreds(configID) {
		return "custom"
	}
	target, ok, _ := a.credStore.GetOAuthSlotAlias(configID)
	if ok && target != "" {
		// Only "aerion-mail" is exposed today; any alias maps to that
		// label. Future alias targets would need their own choice IDs.
		return "aerion-mail"
	}
	return "aerion-shipped"
}

// extensionDeclaresCoreScopesForProvider returns true iff the extension's
// manifest carries first_party_uses_core_for_scopes entries for the given
// provider. Provider-detection is heuristic — Google scopes are URLs under
// googleapis.com, Microsoft scopes use bare names like Contacts.Read.
func (a *App) extensionDeclaresCoreScopesForProvider(extensionID, provider string) bool {
	for _, ext := range a.knownExtensions {
		m := ext.Manifest()
		if m.ID != extensionID {
			continue
		}
		if m.OAuth == nil {
			return false
		}
		for _, scope := range m.OAuth.FirstPartyUsesCoreForScopes {
			if scopeBelongsToProvider(scope, provider) {
				return true
			}
		}
		return false
	}
	return false
}

// scopeBelongsToProvider classifies an OAuth scope string by provider.
// Google scopes are URLs hosted under googleapis.com; Microsoft Graph
// scopes are bare strings like "Contacts.Read" or "Calendars.ReadWrite".
func scopeBelongsToProvider(scope, provider string) bool {
	switch provider {
	case "google":
		return strings.Contains(scope, "googleapis.com")
	case "microsoft":
		return !strings.Contains(scope, "googleapis.com")
	}
	return false
}

// providerFromConfigID strips the well-known prefix from a slot id.
//   "google-contacts"     → "google"
//   "microsoft-calendar"  → "microsoft"
//   anything else         → ""
func providerFromConfigID(configID string) string {
	switch {
	case strings.HasPrefix(configID, "google-"):
		return "google"
	case strings.HasPrefix(configID, "microsoft-"):
		return "microsoft"
	}
	return ""
}

// shippedLabelForSlot returns the user-visible label for the slot's own
// shipped option. Google extension slots are the un-Google-verified test
// clients (broader scopes than the mail-app's verified client) — labeled
// "Aerion - Google (Testing)" so users understand they may see Google's
// unverified-app warning during OAuth consent. Once Google verifies the
// mail project for the extension scopes, the default switches to
// "Aerion - Google" (the aerion-mail choice — mail's verified client).
func shippedLabelForSlot(configID string) string {
	switch configID {
	case "google-contacts", "google-calendar":
		return "Aerion - Google (Testing)"
	case "google-mail":
		return "Aerion - Google"
	case "microsoft-mail", "microsoft-contacts", "microsoft-calendar":
		return "Aerion - Microsoft"
	}
	return "Aerion - Default"
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

