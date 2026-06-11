package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/notification"
	"github.com/hkdb/aerion/internal/oauth2"
	"github.com/hkdb/aerion/internal/platform"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// coreImpl is the host-side implementation of coreapi.Core handed to each
// extension during its lifecycle Register call. It exposes the existing App
// dependencies (mailAPI, composerAPI, contactsAPI, authBroker, uiRegistry)
// through the v1 interfaces.
//
// One coreImpl is constructed PER extension at App.Startup. The extensionID
// field scopes Auth() to that specific extension so the Auth Broker can route
// HTTPClient requests via the extension's own client config (or via Aerion
// core's mail OAuth, per the manifest's first_party_uses_core_for_scopes).
//
// Storage, Notifications, and Events are still Phase 1 stubs.
type coreImpl struct {
	app         *App
	extensionID string
	manifest    coreapi.Manifest
}

// newCoreForExtension constructs a coreImpl scoped to the given extension.
// Safe to call after App.Startup has constructed the underlying APIs.
func newCoreForExtension(a *App, ext coreapi.Extension) *coreImpl {
	m := ext.Manifest()
	return &coreImpl{
		app:         a,
		extensionID: m.ID,
		manifest:    m,
	}
}

func (c *coreImpl) Mail() coreapi.Mail         { return c.app.mailAPI }
func (c *coreImpl) Composer() coreapi.Composer { return c.app.composerAPI }

// Contacts returns a coreapi.Contacts surface that exposes source management
// (ListSources, LinkAccountSource) to extensions. The contact CRUD methods
// (Search/Get/List/Create/Update/Delete) are still owned by the Contacts
// extension's Bridge (extensions/contacts/backend/bridge.go) and remain
// ErrUnimplemented at this host-level surface — no cross-extension consumer
// queries those yet, and routing them through coreImpl would force the
// Contacts extension to initialize even when disabled, breaking the
// lightweight-by-default invariant. Source management lives here (rather
// than on the Bridge) because contact_sources is a host-owned table and
// the host has the full source CRUD already; the extension just needs a
// read-and-link surface to drive its sidebar + account-setup hook.
func (c *coreImpl) Contacts() coreapi.Contacts {
	return contactsCoreImpl{app: c.app}
}

type contactsCoreImpl struct {
	app *App
}

// SearchContacts delegates to App.SearchContacts (the same backend mail's
// composer uses via `Contacts_SearchContacts`) and adapts the flat host
// contact.Contact shape into the richer coreapi.Contact shape used by
// cross-extension consumers. First wired for the Calendar extension's
// attendee picker (Phase C of the v0.3.0 attendees feature) — see
// docs/EXTENSIONS.md §"Wails-bound surface" for the consumer pattern.
//
// Empty query yields []; errors flow through unchanged.
func (c contactsCoreImpl) SearchContacts(query string, limit int) ([]coreapi.Contact, error) {
	if c.app == nil {
		return nil, nil
	}
	results, err := c.app.SearchContacts(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]coreapi.Contact, 0, len(results))
	for _, r := range results {
		if r == nil || r.Email == "" {
			continue
		}
		out = append(out, coreapi.Contact{
			ID:        r.Email, // host's flat Contact uses email as identity
			Name:      r.DisplayName,
			Emails:    []string{r.Email},
			UpdatedAt: r.LastUsed,
		})
	}
	return out, nil
}
func (contactsCoreImpl) GetContact(string) (*coreapi.Contact, error) {
	return nil, coreapi.ErrUnimplemented
}
func (contactsCoreImpl) ListContacts(coreapi.ContactFilter) ([]coreapi.Contact, error) {
	return nil, coreapi.ErrUnimplemented
}
func (contactsCoreImpl) ListAddressbooks(string) ([]coreapi.Addressbook, error) {
	return nil, coreapi.ErrUnimplemented
}

// ListSources wraps the host's existing contact-source store. Filters down
// to the API-surface shape (ContactSource) so the extension only sees what
// it consumes — id, name, type, writable.
func (c contactsCoreImpl) ListSources() ([]coreapi.ContactSource, error) {
	sources, err := c.app.carddavStore.ListSources()
	if err != nil {
		return nil, err
	}
	out := make([]coreapi.ContactSource, 0, len(sources))
	for _, s := range sources {
		if s == nil {
			continue
		}
		accountID := ""
		if s.AccountID != nil {
			accountID = *s.AccountID
		}
		out = append(out, coreapi.ContactSource{
			ID:        s.ID,
			Name:      s.Name,
			Type:      string(s.Type),
			Writable:  s.Writable,
			AccountID: accountID,
		})
	}
	return out, nil
}

// SetSourceWritable flips the writable flag on a contact source. Pure delegation
// to the host's existing carddav store — the contacts extension uses this from
// its incremental-consent flow (Phase 2b.3) to flip Writable after the user
// grants write scopes.
func (c contactsCoreImpl) SetSourceWritable(sourceID string, writable bool) error {
	return c.app.carddavStore.SetSourceWritable(sourceID, writable)
}

// LinkAccountSource delegates to the host's existing LinkAccountContactSource
// Wails method. Returns the new source's id (Wails method returned the full
// source struct; we extract its ID since that's all the extension needs).
func (c contactsCoreImpl) LinkAccountSource(accountID, name string, syncInterval int) (string, error) {
	source, err := c.app.LinkAccountContactSource(accountID, name, syncInterval)
	if err != nil {
		return "", err
	}
	if source == nil {
		return "", nil
	}
	return source.ID, nil
}

// SyncSource delegates to the host's existing SyncContactSource. Used by
// the contacts extension's Ctrl+Shift+S handler and per-source "Sync now"
// affordances.
func (c contactsCoreImpl) SyncSource(sourceID string) error {
	return c.app.SyncContactSource(sourceID)
}

// SyncAllSources delegates to the host's existing SyncAllContactSources.
// Used by the contacts extension's Ctrl+Shift+A shortcut and bulk-sync
// affordances.
func (c contactsCoreImpl) SyncAllSources() error {
	return c.app.SyncAllContactSources()
}

func (contactsCoreImpl) CreateContact(coreapi.ContactCreateInput) (string, error) {
	return "", coreapi.ErrUnimplemented
}
func (contactsCoreImpl) UpdateContact(string, coreapi.ContactPatch) error {
	return coreapi.ErrUnimplemented
}
func (contactsCoreImpl) DeleteContact(string) error { return coreapi.ErrUnimplemented }
func (contactsCoreImpl) SubscribeToContactEvents([]coreapi.ContactEventType) (<-chan coreapi.ContactEvent, coreapi.Unsubscribe, error) {
	return nil, func() {}, coreapi.ErrUnimplemented
}
func (c *coreImpl) Auth() coreapi.Auth {
	return &extensionAuth{
		app:         c.app,
		extensionID: c.extensionID,
		manifest:    c.manifest,
	}
}
func (c *coreImpl) UI() coreapi.UI                       { return uiCoreImpl{app: c.app} }
func (c *coreImpl) Notifications() coreapi.Notifications { return notificationsCoreImpl{app: c.app} }
func (c *coreImpl) Storage() coreapi.Storage             { return storageCoreImpl{app: c.app} }
func (c *coreImpl) Events() coreapi.EventBus             { return c.app.coreEventBus() }
func (c *coreImpl) Log() coreapi.Logger                  { return loggerCoreImpl{extensionID: c.extensionID} }

// Extension returns the typed handle published by another extension via its
// api.go, or (nil, false) if the extension is not loaded.
func (c *coreImpl) Extension(id string) (any, bool) {
	return nil, false
}

// --- Per-extension Auth wrapper --------------------------------------------
//
// extensionAuth bundles the calling extension's identity + manifest with the
// shared Auth Broker. HTTPClient consults the manifest's
// first_party_uses_core_for_scopes to decide whether each scope routes through
// Aerion core's mail OAuth (<provider>-mail) or the extension's own client
// config (<provider>-<extensionID>). Mixed-scope calls are rejected; the
// extension must issue separate HTTPClient calls for each routing target.

type extensionAuth struct {
	app         *App
	extensionID string
	manifest    coreapi.Manifest
}

func (a *extensionAuth) HTTPClient(accountID string, scopes []coreapi.AuthScope) (*http.Client, error) {
	return a.app.authBroker.HTTPClientForExtension(a.extensionID, a.manifest, accountID, scopes)
}

func (a *extensionAuth) IMAPClient(accountID string, requiredCaps []string) (coreapi.IMAPClient, error) {
	// IMAP via broker isn't wired yet (Phase 2+). Mail uses imap.Pool directly.
	return a.app.authBroker.IMAPClient(accountID, requiredCaps)
}

func (a *extensionAuth) SMTPClient(accountID string) (coreapi.SMTPClient, error) {
	return a.app.authBroker.SMTPClient(accountID)
}

// StartIncrementalConsent runs an interactive OAuth flow that adds scopes
// against an existing identity (mail account OR standalone contact source).
// Synchronous: blocks until success / cancel / error.
//
// Phase 2b.3: used by the Contacts extension's write-access picker.
// Exactly one of req.AccountID / req.SourceID drives where tokens are
// persisted (account-keyed vs source-keyed). req.ExpectedEmail is enforced
// on the OAuth callback; req.LoginHint pre-fills the IdP account picker.
//
// Errors: cancel from user → "OAuth callback failed: ..."; wrong account →
// explicit mismatch error; persistence failure → wrapped credentials error.
func (a *extensionAuth) StartIncrementalConsent(req coreapi.StartIncrementalConsentRequest) error {
	log := logging.WithComponent("app.incremental-consent")

	if a.app == nil || a.app.ctx == nil {
		return fmt.Errorf("incremental consent: app not initialized")
	}
	if req.ClientConfigID == "" {
		return fmt.Errorf("incremental consent: clientConfigID is required")
	}
	if (req.AccountID == "") == (req.SourceID == "") {
		return fmt.Errorf("incremental consent: exactly one of accountID / sourceID must be set")
	}

	// Validate the EXTENSION's own slot has creds via the proper resolver
	// (user override → registered providers, NOT inherited from mail-side
	// ldflags via the legacy GetProvider fallback).
	slotCreds, slotOK := oauth2.ClientConfigForID(string(req.ClientConfigID))
	if !slotOK || slotCreds.ClientID == "" {
		return fmt.Errorf("incremental consent: no OAuth credentials configured for %q — set them up in Settings → Extensions → Contacts → OAuth Credentials", req.ClientConfigID)
	}

	// Pull the provider's URLs + default scope set. We override ClientID/
	// Secret with the slot creds resolved above so we always run against the
	// extension's project.
	baseProvider, err := oauth2.GetProvider(string(req.ClientConfigID))
	if err != nil {
		return fmt.Errorf("incremental consent: %w", err)
	}
	baseProvider.ClientID = slotCreds.ClientID
	baseProvider.ClientSecret = slotCreds.ClientSecret
	baseProvider.LoginHint = req.LoginHint

	have := make(map[string]struct{}, len(baseProvider.Scopes))
	for _, s := range baseProvider.Scopes {
		have[s] = struct{}{}
	}
	unioned := append([]string(nil), baseProvider.Scopes...)
	for _, want := range req.Scopes {
		if want.Resource == "" {
			continue
		}
		if _, ok := have[want.Resource]; ok {
			continue
		}
		have[want.Resource] = struct{}{}
		unioned = append(unioned, want.Resource)
	}

	extended := baseProvider
	extended.Scopes = unioned

	log.Info().
		Str("account_id", req.AccountID).
		Str("source_id", req.SourceID).
		Str("client_config_id", string(req.ClientConfigID)).
		Int("scope_count", len(unioned)).
		Bool("login_hint_set", req.LoginHint != "").
		Msg("Starting incremental consent flow")

	authURL, err := a.app.oauth2Manager.StartAuthFlowWithProvider(a.app.ctx, &extended)
	if err != nil {
		return fmt.Errorf("incremental consent: start flow: %w", err)
	}

	if perr := platform.PortalOpenURI(authURL); perr != nil {
		log.Debug().Err(perr).Msg("Portal OpenURI failed, falling back to BrowserOpenURL")
		wailsRuntime.BrowserOpenURL(a.app.ctx, authURL)
	}

	tokens, email, err := a.app.oauth2Manager.WaitForCallback(a.app.ctx)
	if err != nil {
		return fmt.Errorf("incremental consent: callback: %w", err)
	}
	if tokens == nil {
		return fmt.Errorf("incremental consent: no tokens returned")
	}

	if req.ExpectedEmail != "" && email != "" && !strings.EqualFold(email, req.ExpectedEmail) {
		return fmt.Errorf("incremental consent: granted account %q does not match expected account %q", email, req.ExpectedEmail)
	}

	storeTokens := &credentials.OAuthTokens{
		Provider:     baseProvider.Name,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
		Scopes:       unioned,
	}

	if req.AccountID != "" {
		if err := a.app.credStore.SetOAuthTokensForClientConfig(req.AccountID, string(req.ClientConfigID), storeTokens); err != nil {
			return fmt.Errorf("incremental consent: persist account tokens: %w", err)
		}
	}
	if req.SourceID != "" {
		if err := a.app.credStore.SetContactSourceOAuthTokens(req.SourceID, storeTokens); err != nil {
			return fmt.Errorf("incremental consent: persist source tokens: %w", err)
		}
	}

	log.Info().
		Str("account_id", req.AccountID).
		Str("source_id", req.SourceID).
		Str("client_config_id", string(req.ClientConfigID)).
		Msg("Incremental consent completed")
	return nil
}

// --- Phase 1 stubs for unimplemented surfaces -------------------------------

// notificationsCoreImpl bridges coreapi.NotifyRequest to internal/notification's
// Notifier. Click actions are encoded into NotificationData.{ExtensionID,Path}
// so the dispatcher in background.go's SetClickHandler can route on those
// fields. The Notifier itself stays mail-agnostic.
type notificationsCoreImpl struct {
	app *App
}

func (n notificationsCoreImpl) Show(req coreapi.NotifyRequest) error {
	if n.app == nil || n.app.notifier == nil {
		return fmt.Errorf("notifier not ready")
	}
	data := notification.NotificationData{}
	switch req.OnClick.Kind {
	case "open-extension", "open-deep-link":
		data.ExtensionID = req.OnClick.ExtensionID
		data.Path = req.OnClick.Path
	}
	_, err := n.app.notifier.Show(notification.Notification{
		Title: req.Title,
		Body:  req.Body,
		Icon:  req.Icon,
		Data:  data,
	})
	return err
}

// storageCoreImpl is the host implementation of coreapi.Storage. KV is still
// a not-implemented stub (extensions open their own SQLite); Secrets is
// fully wired, delegating to credentials.Store for the keyring + AES-fallback
// orchestration so extensions can stash sensitive values without ever
// importing internal/credentials.
type storageCoreImpl struct {
	app *App
}

func (s storageCoreImpl) KV(extensionID string) coreapi.KVStore {
	return stubKV{extensionID: extensionID}
}

func (s storageCoreImpl) Secrets(extensionID string) coreapi.Secrets {
	return secretsCoreImpl{app: s.app, extensionID: extensionID}
}

func (s storageCoreImpl) HostSecrets() coreapi.HostSecrets {
	return hostSecretsCoreImpl(s)
}

// hostSecretsCoreImpl is the host implementation of coreapi.HostSecrets.
// Read-only access to credentials whose lifecycle the host owns. Routes by
// the key's class prefix to the matching credStore helper; add new prefixes
// as new Pattern B consumers emerge.
type hostSecretsCoreImpl struct {
	app *App
}

func (h hostSecretsCoreImpl) Get(key string) (string, error) {
	if h.app.credStore == nil {
		return "", fmt.Errorf("storage.HostSecrets: credentials store not initialized")
	}
	switch {
	case strings.HasPrefix(key, "carddav:"):
		return h.app.credStore.GetCardDAVPassword(strings.TrimPrefix(key, "carddav:"))
	}
	return "", fmt.Errorf("storage.HostSecrets: unsupported key prefix in %q", key)
}

// secretsCoreImpl is the per-extension Secrets handle. The extension ID is
// captured here so the extension's bridge code only types `core.Storage().
// Secrets(extensionID).Set(key, value)` once — the handle remembers the
// scope for subsequent calls.
type secretsCoreImpl struct {
	app         *App
	extensionID string
}

func (s secretsCoreImpl) Set(key, value string) error {
	if s.app.credStore == nil {
		return fmt.Errorf("storage.Secrets: credentials store not initialized")
	}
	return s.app.credStore.SetExtensionSecret(s.extensionID, key, value)
}

func (s secretsCoreImpl) Get(key string) (string, error) {
	if s.app.credStore == nil {
		return "", fmt.Errorf("storage.Secrets: credentials store not initialized")
	}
	return s.app.credStore.GetExtensionSecret(s.extensionID, key)
}

func (s secretsCoreImpl) Delete(key string) error {
	if s.app.credStore == nil {
		return fmt.Errorf("storage.Secrets: credentials store not initialized")
	}
	return s.app.credStore.DeleteExtensionSecret(s.extensionID, key)
}

func (s secretsCoreImpl) DeleteAll() error {
	if s.app.credStore == nil {
		return fmt.Errorf("storage.Secrets: credentials store not initialized")
	}
	return s.app.credStore.DeleteAllExtensionSecrets(s.extensionID)
}

type stubKV struct {
	extensionID string
}

func (k stubKV) Get(key string) (string, error) {
	return "", fmt.Errorf("storage.KV: extension %q has no host-provided KV in Phase 2a (use its own Store directly)", k.extensionID)
}
func (k stubKV) Set(key, value string) error          { return coreapi.ErrUnimplemented }
func (k stubKV) Delete(key string) error              { return coreapi.ErrUnimplemented }
func (k stubKV) List(prefix string) ([]string, error) { return nil, coreapi.ErrUnimplemented }

// loggerCoreImpl routes Logger calls through the host's zerolog with an
// extension tag so unified log output is filterable per-extension.
type loggerCoreImpl struct {
	extensionID string
}

func (l loggerCoreImpl) logger() zerologStr {
	// Embedded component=frontend/backend distinction is up to the call
	// site; coreapi.Logger.Log just adds the extension tag.
	base := logging.WithComponent("extension")
	if l.extensionID != "" {
		base = base.With().Str("extension", l.extensionID).Logger()
	}
	return zerologStr{l: base}
}

func (l loggerCoreImpl) Debug(msg string) { l.logger().Debug(msg) }
func (l loggerCoreImpl) Info(msg string)  { l.logger().Info(msg) }
func (l loggerCoreImpl) Warn(msg string)  { l.logger().Warn(msg) }
func (l loggerCoreImpl) Error(msg string) { l.logger().Error(msg) }

// zerologStr is a tiny wrapper so the call sites stay readable.
type zerologStr struct{ l zerolog.Logger }

func (z zerologStr) Debug(msg string) { z.l.Debug().Msg(msg) }
func (z zerologStr) Info(msg string)  { z.l.Info().Msg(msg) }
func (z zerologStr) Warn(msg string)  { z.l.Warn().Msg(msg) }
func (z zerologStr) Error(msg string) { z.l.Error().Msg(msg) }

// uiCoreImpl wraps the host's extension UI Registry so extensions consume
// a single `coreapi.UI` surface that owns both registration methods AND
// platform actions like OpenURL. The Registry stays focused on
// registrations; platform-specific concerns live here.
type uiCoreImpl struct {
	app *App
}

func (u uiCoreImpl) RegisterRailTab(req coreapi.RailTabRequest) (coreapi.Unregister, error) {
	return u.app.uiRegistry.RegisterRailTab(req)
}
func (u uiCoreImpl) RegisterSettingsTab(req coreapi.SettingsTabRequest) (coreapi.Unregister, error) {
	return u.app.uiRegistry.RegisterSettingsTab(req)
}
func (u uiCoreImpl) RegisterContextMenuItem(req coreapi.ContextMenuRequest) (coreapi.Unregister, error) {
	return u.app.uiRegistry.RegisterContextMenuItem(req)
}
func (u uiCoreImpl) RegisterInboxView(req coreapi.InboxViewRequest) (coreapi.Unregister, error) {
	return u.app.uiRegistry.RegisterInboxView(req)
}
func (u uiCoreImpl) RegisterAccountSetupHook(req coreapi.AccountSetupHookRequest) (coreapi.Unregister, error) {
	return u.app.uiRegistry.RegisterAccountSetupHook(req)
}

// OpenURL delegates to App.OpenURL which owns the protocol allowlist +
// Linux portal-first path + xdg-open fallback. The extension never sees
// internal/platform directly.
func (u uiCoreImpl) OpenURL(url string) error {
	return u.app.OpenURL(url)
}

// compile-time check: coreImpl satisfies coreapi.Core, extensionAuth satisfies coreapi.Auth
var _ coreapi.Core = (*coreImpl)(nil)
var _ coreapi.Auth = (*extensionAuth)(nil)
var _ coreapi.Logger = loggerCoreImpl{}
var _ coreapi.UI = uiCoreImpl{}
