package app

import (
	"fmt"
	"time"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/oauth2"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// OAuthStatus represents the OAuth status for an account
type OAuthStatus struct {
	IsOAuth     bool      `json:"isOAuth"`     // Whether the account uses OAuth
	Provider    string    `json:"provider"`    // OAuth provider name (google, microsoft)
	Email       string    `json:"email"`       // Authenticated email address
	ExpiresAt   time.Time `json:"expiresAt"`   // Token expiry time
	IsExpired   bool      `json:"isExpired"`   // Whether the token has expired
	NeedsReauth bool      `json:"needsReauth"` // Whether re-authorization is required
}

// ============================================================================
// OAuth2 API - Exposed to frontend via Wails bindings
// ============================================================================

// StartOAuthFlow initiates the OAuth2 authorization flow for a provider.
// Opens the system browser with the authorization URL and waits for callback.
// Emits events: oauth:started, oauth:success, oauth:error
func (a *App) StartOAuthFlow(provider string) error {
	log := logging.WithComponent("app.oauth")

	// Check if provider is configured
	if !oauth2.IsProviderConfigured(provider) {
		return fmt.Errorf("OAuth provider %s is not configured", provider)
	}

	log.Info().Str("provider", provider).Msg("Starting OAuth flow")

	// Emit started event
	wailsRuntime.EventsEmit(a.ctx, "oauth:started", map[string]interface{}{
		"provider": provider,
	})

	// Start the OAuth flow
	authURL, err := a.oauth2Manager.StartAuthFlow(a.ctx, provider)
	if err != nil {
		wailsRuntime.EventsEmit(a.ctx, "oauth:error", map[string]interface{}{
			"provider": provider,
			"error":    err.Error(),
		})
		return fmt.Errorf("failed to start OAuth flow: %w", err)
	}

	// Open browser with auth URL
	wailsRuntime.BrowserOpenURL(a.ctx, authURL)

	// Wait for callback in background
	go func() {
		defer recoverPanic("app.oauth", "OAuth callback")
		tokens, email, err := a.oauth2Manager.WaitForCallback(a.ctx)
		if err != nil {
			log.Error().Err(err).Str("provider", provider).Msg("OAuth callback failed")
			wailsRuntime.EventsEmit(a.ctx, "oauth:error", map[string]interface{}{
				"provider": provider,
				"error":    err.Error(),
			})
			return
		}

		// Store tokens temporarily for account creation
		a.pendingOAuthTokens = tokens
		a.pendingOAuthEmail = email

		log.Info().
			Str("provider", provider).
			Str("email", email).
			Msg("OAuth flow completed successfully")

		// Emit success event with tokens info (frontend will handle account creation)
		wailsRuntime.EventsEmit(a.ctx, "oauth:success", map[string]interface{}{
			"provider":  provider,
			"email":     email,
			"expiresIn": tokens.ExpiresIn,
		})
	}()

	return nil
}

// CompleteOAuthAccountSetup completes account setup after successful OAuth flow.
// This should be called by the frontend after receiving oauth:success event.
// It creates the account and saves the OAuth tokens from the completed flow.
func (a *App) CompleteOAuthAccountSetup(provider, email, accountName, displayName, color string) (*account.Account, error) {
	log := logging.WithComponent("app.oauth")

	log.Info().
		Str("provider", provider).
		Str("email", email).
		Str("name", accountName).
		Msg("Completing OAuth account setup")

	// Check that we have pending tokens from the OAuth flow
	if a.pendingOAuthTokens == nil {
		return nil, fmt.Errorf("no pending OAuth tokens - please complete the sign-in process first")
	}

	// Verify the email matches
	if a.pendingOAuthEmail != "" && a.pendingOAuthEmail != email {
		log.Warn().
			Str("expected", a.pendingOAuthEmail).
			Str("provided", email).
			Msg("OAuth email mismatch, using provided email")
	}

	// Get provider config for IMAP/SMTP settings
	providerConfig, err := oauth2.GetProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("unknown provider: %w", err)
	}

	// Build account config based on provider
	var config account.AccountConfig
	switch provider {
	case "google":
		config = account.AccountConfig{
			Name:           accountName,
			DisplayName:    displayName,
			Color:          color,
			Email:          email,
			Username:       email,
			AuthType:       account.AuthOAuth2,
			IMAPHost:       "imap.gmail.com",
			IMAPPort:       993,
			IMAPSecurity:   account.SecurityTLS,
			SMTPHost:       "smtp.gmail.com",
			SMTPPort:       587,
			SMTPSecurity:   account.SecurityStartTLS,
			SyncPeriodDays: 180,
			SyncInterval:   30,
		}
	case "microsoft":
		config = account.AccountConfig{
			Name:           accountName,
			DisplayName:    displayName,
			Color:          color,
			Email:          email,
			Username:       email,
			AuthType:       account.AuthOAuth2,
			IMAPHost:       "outlook.office365.com",
			IMAPPort:       993,
			IMAPSecurity:   account.SecurityTLS,
			SMTPHost:       "smtp.office365.com",
			SMTPPort:       587,
			SMTPSecurity:   account.SecurityStartTLS,
			SyncPeriodDays: 180,
			SyncInterval:   30,
		}
	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %s", provider)
	}

	// Create the account
	acc, err := a.accountStore.Create(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	// Calculate token expiry
	expiresAt := time.Now().Add(time.Duration(a.pendingOAuthTokens.ExpiresIn) * time.Second)

	// Save OAuth tokens
	tokens := &credentials.OAuthTokens{
		Provider:     provider,
		AccessToken:  a.pendingOAuthTokens.AccessToken,
		RefreshToken: a.pendingOAuthTokens.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       providerConfig.Scopes,
	}

	log.Debug().
		Str("accountID", acc.ID).
		Str("provider", provider).
		Int("accessTokenLen", len(tokens.AccessToken)).
		Int("refreshTokenLen", len(tokens.RefreshToken)).
		Time("expiresAt", expiresAt).
		Strs("scopes", tokens.Scopes).
		Msg("Saving OAuth tokens")

	if err := a.credStore.SetOAuthTokens(acc.ID, tokens); err != nil {
		// Rollback: delete the account if we can't save tokens
		log.Error().Err(err).Str("accountID", acc.ID).Msg("Failed to save OAuth tokens, rolling back account creation")
		a.accountStore.Delete(acc.ID)
		return nil, fmt.Errorf("failed to save OAuth tokens: %w", err)
	}

	log.Debug().Str("accountID", acc.ID).Msg("OAuth tokens saved successfully")

	// Clear pending tokens
	a.pendingOAuthTokens = nil
	a.pendingOAuthEmail = ""

	log.Info().
		Str("accountID", acc.ID).
		Str("email", email).
		Str("provider", provider).
		Time("tokenExpiry", expiresAt).
		Msg("OAuth account created and tokens saved successfully")

	// Scale database connection pool for new account
	a.updateDBConnectionPool()

	return acc, nil
}

// SaveOAuthTokens stores OAuth tokens for an account after OAuth flow completion.
// This should be called immediately after CompleteOAuthAccountSetup.
func (a *App) SaveOAuthTokens(accountID, provider string, accessToken, refreshToken string, expiresIn int) error {
	log := logging.WithComponent("app.oauth")

	// Calculate expiry time
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)

	// Get scopes from provider config
	providerConfig, err := oauth2.GetProvider(provider)
	if err != nil {
		return fmt.Errorf("unknown provider: %w", err)
	}

	tokens := &credentials.OAuthTokens{
		Provider:     provider,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       providerConfig.Scopes,
	}

	if err := a.credStore.SetOAuthTokens(accountID, tokens); err != nil {
		return fmt.Errorf("failed to store OAuth tokens: %w", err)
	}

	log.Info().
		Str("accountID", accountID).
		Str("provider", provider).
		Time("expiresAt", expiresAt).
		Msg("OAuth tokens saved")

	return nil
}

// SavePendingOAuthTokens saves the pending OAuth tokens from a completed flow to an existing account.
// This is used for re-authorization when tokens have expired.
func (a *App) SavePendingOAuthTokens(accountID string) error {
	log := logging.WithComponent("app.oauth")

	if a.pendingOAuthTokens == nil {
		return fmt.Errorf("no pending OAuth tokens to save")
	}

	// Get the provider from the account
	provider, err := a.credStore.GetOAuthProvider(accountID)
	if err != nil || provider == "" {
		return fmt.Errorf("could not determine OAuth provider for account")
	}

	// Get scopes from provider config
	providerConfig, err := oauth2.GetProvider(provider)
	if err != nil {
		return fmt.Errorf("unknown provider: %w", err)
	}

	// Calculate expiry time
	expiresAt := time.Now().Add(time.Duration(a.pendingOAuthTokens.ExpiresIn) * time.Second)

	tokens := &credentials.OAuthTokens{
		Provider:     provider,
		AccessToken:  a.pendingOAuthTokens.AccessToken,
		RefreshToken: a.pendingOAuthTokens.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       providerConfig.Scopes,
	}

	if err := a.credStore.SetOAuthTokens(accountID, tokens); err != nil {
		return fmt.Errorf("failed to store OAuth tokens: %w", err)
	}

	// Propagate new tokens to any shared mailboxes linked to this account
	sharedMailboxes, _ := a.accountStore.ListBySharedMailboxParent(accountID)
	for _, sm := range sharedMailboxes {
		if smErr := a.credStore.SetOAuthTokens(sm.ID, tokens); smErr != nil {
			log.Warn().Err(smErr).Str("sharedID", sm.ID).Msg("Failed to propagate tokens to shared mailbox")
		}
	}

	log.Info().
		Str("accountID", accountID).
		Str("provider", provider).
		Time("expiresAt", expiresAt).
		Msg("Pending OAuth tokens saved to account")

	// Clear pending tokens
	a.pendingOAuthTokens = nil
	a.pendingOAuthEmail = ""

	return nil
}

// CancelOAuthFlow cancels any in-progress OAuth authorization flow.
func (a *App) CancelOAuthFlow() {
	log := logging.WithComponent("app.oauth")
	log.Info().Msg("Cancelling OAuth flow")

	a.oauth2Manager.CancelAuthFlow()

	// Clear any pending tokens
	a.pendingOAuthTokens = nil
	a.pendingOAuthEmail = ""

	wailsRuntime.EventsEmit(a.ctx, "oauth:cancelled", nil)
}

// GetOAuthStatus returns the OAuth status for an account.
func (a *App) GetOAuthStatus(accountID string) (*OAuthStatus, error) {
	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}

	status := &OAuthStatus{
		IsOAuth: acc.AuthType == account.AuthOAuth2,
	}

	if !status.IsOAuth {
		return status, nil
	}

	// Get OAuth token info
	tokens, err := a.credStore.GetOAuthTokens(accountID)
	if err != nil {
		// Tokens not found - needs re-auth
		status.NeedsReauth = true
		return status, nil
	}

	status.Provider = tokens.Provider
	status.ExpiresAt = tokens.ExpiresAt
	status.IsExpired = tokens.IsExpired()
	status.NeedsReauth = tokens.IsExpired() && tokens.RefreshToken == ""

	return status, nil
}

// IsOAuthConfigured returns whether OAuth is configured for a provider.
// This checks if the client ID was provided at build time.
func (a *App) IsOAuthConfigured(provider string) bool {
	return oauth2.IsProviderConfigured(provider)
}

// GetConfiguredOAuthProviders returns a list of OAuth providers that are configured.
func (a *App) GetConfiguredOAuthProviders() []string {
	var configured []string
	for _, p := range oauth2.SupportedProviders() {
		if oauth2.IsProviderConfigured(p) {
			configured = append(configured, p)
		}
	}
	return configured
}

// ReauthorizeAccount initiates re-authorization for an existing OAuth account.
// This is used when tokens have expired and refresh has failed.
func (a *App) ReauthorizeAccount(accountID string) error {
	log := logging.WithComponent("app.oauth")

	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return fmt.Errorf("account not found: %s", accountID)
	}

	if acc.AuthType != account.AuthOAuth2 {
		return fmt.Errorf("account is not an OAuth account")
	}

	// Get the provider from stored tokens
	provider, err := a.credStore.GetOAuthProvider(accountID)
	if err != nil || provider == "" {
		return fmt.Errorf("could not determine OAuth provider for account")
	}

	log.Info().
		Str("accountID", accountID).
		Str("provider", provider).
		Msg("Starting re-authorization for account")

	// Start OAuth flow - frontend will handle storing new tokens
	return a.StartOAuthFlow(provider)
}

// TestOAuthConnection tests the connection for an OAuth account.
// This verifies that the stored tokens work for IMAP access.
func (a *App) TestOAuthConnection(accountID string) error {
	log := logging.WithComponent("app.oauth")

	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return fmt.Errorf("account not found: %s", accountID)
	}

	if acc.AuthType != account.AuthOAuth2 {
		return fmt.Errorf("account is not an OAuth account")
	}

	// Get valid OAuth token
	tokens, err := a.getValidOAuthToken(accountID)
	if err != nil {
		return fmt.Errorf("failed to get OAuth token: %w", err)
	}

	// Create IMAP client and test connection
	clientConfig := imap.DefaultConfig()
	clientConfig.Host = acc.IMAPHost
	clientConfig.Port = acc.IMAPPort
	clientConfig.Security = imap.SecurityType(acc.IMAPSecurity)
	clientConfig.Username = acc.Username
	clientConfig.AuthType = imap.AuthTypeOAuth2
	clientConfig.AccessToken = tokens.AccessToken

	client := imap.NewClient(clientConfig)

	if err := client.Connect(); err != nil {
		log.Error().Err(err).Msg("OAuth connection test failed")
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	if err := client.Login(); err != nil {
		log.Error().Err(err).Msg("OAuth login test failed")
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	log.Info().Str("accountID", accountID).Msg("OAuth connection test successful")
	return nil
}
