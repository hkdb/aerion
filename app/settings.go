package app

import (
	"fmt"
	"strings"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/certificate"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/settings"
	"github.com/hkdb/aerion/internal/smtp"
)

// ============================================================================
// Settings API - Exposed to frontend via Wails bindings
// ============================================================================

// IsExtensionEnabled returns whether the named first-party extension is
// enabled. Unknown extensions return (false, nil). Used by the frontend to
// gate UI affordances (rail tab, settings tab) without throwing on
// not-yet-defined extensions.
func (a *App) IsExtensionEnabled(name string) (bool, error) {
	return a.settingsStore.IsExtensionEnabled(name)
}

// SetExtensionEnabled toggles a first-party extension on or off. Phase 1
// only writes the setting flag; Phase 2 hooks this to start/stop the
// extension's background services and emit a UI refresh event.
func (a *App) SetExtensionEnabled(name string, enabled bool) error {
	return a.settingsStore.SetExtensionEnabled(name, enabled)
}

// GetReadReceiptResponsePolicy returns the current read receipt response policy
// Values: "never", "ask", "always"
func (a *App) GetReadReceiptResponsePolicy() (string, error) {
	return a.settingsStore.GetReadReceiptResponsePolicy()
}

// SetReadReceiptResponsePolicy sets the read receipt response policy
// Valid values: "never", "ask", "always"
func (a *App) SetReadReceiptResponsePolicy(policy string) error {
	return a.settingsStore.SetReadReceiptResponsePolicy(policy)
}

// GetMarkAsReadDelay returns the delay before marking messages as read (in milliseconds)
// Returns: -1 = manual only, 0 = immediate, >0 = delay in ms
func (a *App) GetMarkAsReadDelay() (int, error) {
	return a.settingsStore.GetMarkAsReadDelay()
}

// SetMarkAsReadDelay sets the delay before marking messages as read (in milliseconds)
// Valid values: -1 (manual only), 0 (immediate), or 100-5000 (delay in ms)
func (a *App) SetMarkAsReadDelay(delayMs int) error {
	return a.settingsStore.SetMarkAsReadDelay(delayMs)
}

// GetMessageListDensity returns the message list density setting
func (a *App) GetMessageListDensity() (string, error) {
	return a.settingsStore.GetMessageListDensity()
}

// SetMessageListDensity sets the message list density
func (a *App) SetMessageListDensity(density string) error {
	return a.settingsStore.SetMessageListDensity(density)
}

// GetAccentBarUnread returns whether the accent bar for unread messages is enabled
func (a *App) GetAccentBarUnread() (bool, error) {
	return a.settingsStore.GetAccentBarUnread()
}

// SetAccentBarUnread enables or disables the accent bar for unread messages
func (a *App) SetAccentBarUnread(enabled bool) error {
	return a.settingsStore.SetAccentBarUnread(enabled)
}

// GetShowMessageListCircles returns whether colored sender circles are shown in the message list
func (a *App) GetShowMessageListCircles() (bool, error) {
	return a.settingsStore.GetShowMessageListCircles()
}

// SetShowMessageListCircles enables or disables colored sender circles in the message list
func (a *App) SetShowMessageListCircles(enabled bool) error {
	return a.settingsStore.SetShowMessageListCircles(enabled)
}

// GetShowViewerCircles returns whether colored sender circles are shown in the conversation viewer
func (a *App) GetShowViewerCircles() (bool, error) {
	return a.settingsStore.GetShowViewerCircles()
}

// SetShowViewerCircles enables or disables colored sender circles in the conversation viewer
func (a *App) SetShowViewerCircles(enabled bool) error {
	return a.settingsStore.SetShowViewerCircles(enabled)
}

// GetMessageListSortOrder returns the message list sort order setting
func (a *App) GetMessageListSortOrder() (string, error) {
	return a.settingsStore.GetMessageListSortOrder()
}

// SetMessageListSortOrder sets the message list sort order
func (a *App) SetMessageListSortOrder(sortOrder string) error {
	return a.settingsStore.SetMessageListSortOrder(sortOrder)
}

// GetThemeMode returns the current theme mode setting
// Values: "system", "light", "light-blue", "light-orange", "dark", "dark-gray", "dark-balanced"
func (a *App) GetThemeMode() (string, error) {
	return a.settingsStore.GetThemeMode()
}

// SetThemeMode sets the theme mode
// Valid values: "system", "light", "light-blue", "light-orange", "dark", "dark-gray", "dark-balanced"
func (a *App) SetThemeMode(mode string) error {
	if err := a.settingsStore.SetThemeMode(mode); err != nil {
		return err
	}

	// Broadcast theme change to all detached composer windows
	a.BroadcastThemeChange(mode)

	return nil
}

// GetShowTitleBar returns whether the title bar should be shown
func (a *App) GetShowTitleBar() (bool, error) {
	return a.settingsStore.GetShowTitleBar()
}

// SetShowTitleBar sets whether the title bar should be shown
func (a *App) SetShowTitleBar(show bool) error {
	return a.settingsStore.SetShowTitleBar(show)
}

// GetTermsAccepted returns whether the user has accepted the terms of service
func (a *App) GetTermsAccepted() (bool, error) {
	return a.settingsStore.GetTermsAccepted()
}

// SetTermsAccepted sets whether the user has accepted the terms of service
func (a *App) SetTermsAccepted(accepted bool) error {
	return a.settingsStore.SetTermsAccepted(accepted)
}

// GetLastSeenVersion returns the Aerion version last acknowledged by the user
// in the "What's new in this version" launch dialog. Empty = never acknowledged.
func (a *App) GetLastSeenVersion() (string, error) {
	return a.settingsStore.GetLastSeenVersion()
}

// SetLastSeenVersion records the current version as acknowledged. The
// frontend calls this from the dialog's OK click handler only — not on
// ESC / outside-click — so a user who dismisses without acknowledging
// sees the dialog again next launch.
func (a *App) SetLastSeenVersion(version string) error {
	return a.settingsStore.SetLastSeenVersion(version)
}

// GetOAuthWarningDisabled reports whether the user has opted out of the
// missing-OAuth-creds launch warning via "Don't show again".
func (a *App) GetOAuthWarningDisabled() (bool, error) {
	return a.settingsStore.GetOAuthWarningDisabled()
}

// SetOAuthWarningDisabled persists the user's "Don't show again" choice
// from the OAuth-credentials-missing launch warning.
func (a *App) SetOAuthWarningDisabled(disabled bool) error {
	return a.settingsStore.SetOAuthWarningDisabled(disabled)
}

// GetRunBackground returns whether Aerion keeps running when the window is closed
func (a *App) GetRunBackground() (bool, error) {
	return a.settingsStore.GetRunBackground()
}

// SetRunBackground sets whether Aerion keeps running when the window is closed.
// Disabling also force-disables start_hidden.
func (a *App) SetRunBackground(enabled bool) error {
	if err := a.settingsStore.SetRunBackground(enabled); err != nil {
		return err
	}
	if !enabled {
		return a.settingsStore.SetStartHidden(false)
	}
	return nil
}

// GetStartHidden returns whether Aerion starts with the window hidden
func (a *App) GetStartHidden() (bool, error) {
	return a.settingsStore.GetStartHidden()
}

// SetStartHidden sets whether Aerion starts with the window hidden.
// Enabling also force-enables run_background (start hidden requires background mode).
func (a *App) SetStartHidden(enabled bool) error {
	if enabled {
		if err := a.settingsStore.SetRunBackground(true); err != nil {
			return err
		}
	}
	return a.settingsStore.SetStartHidden(enabled)
}

// GetAutostart returns whether Aerion starts on login
func (a *App) GetAutostart() (bool, error) {
	return a.settingsStore.GetAutostart()
}

// SetAutostart sets whether Aerion starts on login.
// Manages the XDG autostart .desktop file or Flatpak Background portal.
func (a *App) SetAutostart(enabled bool) error {
	// Check current value to avoid unnecessary OS-level changes
	// (e.g., Flatpak Background portal D-Bus calls that may fail)
	current, _ := a.settingsStore.GetAutostart()

	if err := a.settingsStore.SetAutostart(enabled); err != nil {
		return err
	}
	if a.autostartMgr == nil || current == enabled {
		return nil
	}
	if enabled {
		return a.autostartMgr.Enable()
	}
	return a.autostartMgr.Disable()
}

// GetLanguage returns the saved language preference (locale code)
// Returns empty string if not set (frontend uses system locale detection)
func (a *App) GetLanguage() (string, error) {
	return a.settingsStore.GetLanguage()
}

// SetLanguage sets the language preference
// Valid values: "en", "zh-TW", "zh-HK", "zh-CN"
func (a *App) SetLanguage(language string) error {
	return a.settingsStore.SetLanguage(language)
}

// GetComposerMode returns the default compose mode ("inline" or "detached")
func (a *App) GetComposerMode() (string, error) {
	return a.settingsStore.GetComposerMode()
}

// SetComposerMode sets the default compose mode.
// Setting to "detached" also auto-sets mailto mode to "detached".
func (a *App) SetComposerMode(mode string) error {
	if err := a.settingsStore.SetComposerMode(mode); err != nil {
		return err
	}
	if mode == "detached" {
		return a.settingsStore.SetMailtoMode("detached")
	}
	return nil
}

// GetMailtoMode returns the external mailto link handling mode ("inline" or "detached")
func (a *App) GetMailtoMode() (string, error) {
	return a.settingsStore.GetMailtoMode()
}

// SetMailtoMode sets the external mailto link handling mode
func (a *App) SetMailtoMode(mode string) error {
	return a.settingsStore.SetMailtoMode(mode)
}

// GetComposerFormat returns the default composer format ("rich" or "plain")
func (a *App) GetComposerFormat() (string, error) {
	return a.settingsStore.GetComposerFormat()
}

// SetComposerFormat sets the default composer format
func (a *App) SetComposerFormat(format string) error {
	return a.settingsStore.SetComposerFormat(format)
}

// GetNativeTitleBar returns whether the native OS title bar is enabled
func (a *App) GetNativeTitleBar() (bool, error) {
	return a.settingsStore.GetNativeTitleBar()
}

// SetNativeTitleBar sets whether the native OS title bar is enabled.
func (a *App) SetNativeTitleBar(enabled bool) error {
	return a.settingsStore.SetNativeTitleBar(enabled)
}

// GetAlwaysLoadImages returns whether remote images should always be loaded
func (a *App) GetAlwaysLoadImages() (bool, error) {
	return a.settingsStore.GetAlwaysLoadImages()
}

// SetAlwaysLoadImages sets whether remote images should always be loaded
func (a *App) SetAlwaysLoadImages(enabled bool) error {
	return a.settingsStore.SetAlwaysLoadImages(enabled)
}

// GetDarkMailContent returns whether email content should be visually darkened
// while Aerion is in dark mode.
func (a *App) GetDarkMailContent() (bool, error) {
	return a.settingsStore.GetDarkMailContent()
}

// SetDarkMailContent persists the dark-mail-content toggle.
func (a *App) SetDarkMailContent(enabled bool) error {
	return a.settingsStore.SetDarkMailContent(enabled)
}

// GetOverrideEmailColors returns whether email content background is forced to transparent
// and text/link colors match the theme.
func (a *App) GetOverrideEmailColors() (bool, error) {
	return a.settingsStore.GetOverrideEmailColors()
}

// SetOverrideEmailColors persists the override-email-colors toggle.
func (a *App) SetOverrideEmailColors(enabled bool) error {
	return a.settingsStore.SetOverrideEmailColors(enabled)
}

// AddImageAllowlist adds a domain or sender to the image allowlist
// entryType: "domain" or "sender"
// value: the domain (e.g., "company.com") or email (e.g., "newsletter@company.com")
func (a *App) AddImageAllowlist(entryType, value string) error {
	return a.imageAllowlistStore.Add(entryType, value)
}

// RemoveImageAllowlist removes an entry from the image allowlist by ID
func (a *App) RemoveImageAllowlist(id int64) error {
	return a.imageAllowlistStore.Remove(id)
}

// IsImageAllowed checks if the sender's email or domain is in the allowlist
func (a *App) IsImageAllowed(email string) (bool, error) {
	return a.imageAllowlistStore.IsAllowed(email)
}

// GetImageAllowlist returns all allowlist entries
func (a *App) GetImageAllowlist() ([]*settings.AllowlistEntry, error) {
	return a.imageAllowlistStore.List()
}

// SendReadReceipt sends a read receipt (MDN) for the specified message
func (a *App) SendReadReceipt(accountID, messageID string) error {
	log := logging.WithComponent("app")

	// Get the message
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return fmt.Errorf("message not found: %s", messageID)
	}

	// Check if read receipt is requested
	if msg.ReadReceiptTo == "" {
		return fmt.Errorf("message does not request a read receipt")
	}

	// Check if already handled
	if msg.ReadReceiptHandled {
		return fmt.Errorf("read receipt already handled for this message")
	}

	// Get account for SMTP settings
	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	// Get default identity for the account
	identities, err := a.accountStore.GetIdentities(accountID)
	if err != nil {
		return fmt.Errorf("failed to get identities: %w", err)
	}

	var fromName, fromEmail string
	for _, id := range identities {
		if id.IsDefault {
			fromName = id.Name
			fromEmail = id.Email
			break
		}
	}
	if fromEmail == "" && len(identities) > 0 {
		fromName = identities[0].Name
		fromEmail = identities[0].Email
	}
	if fromEmail == "" {
		fromEmail = acc.Email
		fromName = acc.Name
	}

	// Build MDN message
	mdnBytes, err := smtp.BuildMDN(msg, fromName, fromEmail, smtp.MDNDisplayed)
	if err != nil {
		return fmt.Errorf("failed to build MDN: %w", err)
	}

	// Create SMTP config
	smtpConfig := smtp.ClientConfig{
		Host:      acc.SMTPHost,
		Port:      acc.SMTPPort,
		Username:  acc.Username,
		Security:  smtp.SecurityType(acc.SMTPSecurity),
		TLSConfig: certificate.BuildTLSConfig(acc.SMTPHost, a.certStore),
	}

	// Handle authentication based on auth type
	if acc.AuthType == account.AuthOAuth2 {
		// Get valid OAuth token (refreshing if needed)
		tokens, err := a.getValidOAuthToken(accountID)
		if err != nil {
			return fmt.Errorf("failed to get OAuth token: %w", err)
		}
		smtpConfig.AuthType = smtp.AuthTypeOAuth2
		smtpConfig.AccessToken = tokens.AccessToken
	} else {
		// Default to password authentication
		password, err := a.credStore.GetPassword(accountID)
		if err != nil {
			return fmt.Errorf("failed to get password: %w", err)
		}
		smtpConfig.AuthType = smtp.AuthTypePassword
		smtpConfig.Password = password
	}

	// Create SMTP client and connect
	client := smtp.NewClient(smtpConfig)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to SMTP: %w", err)
	}
	defer client.Close()

	if err := client.Login(); err != nil {
		return fmt.Errorf("failed to authenticate to SMTP: %w", err)
	}

	// Extract recipient email
	recipientEmail := extractEmailFromHeader(msg.ReadReceiptTo)

	// Send the MDN
	if err := client.SendMail(fromEmail, []string{recipientEmail}, mdnBytes); err != nil {
		log.Error().Err(err).Str("from", fromEmail).Str("to", recipientEmail).Msg("Failed to send read receipt MDN")
		return fmt.Errorf("failed to send read receipt: %w", err)
	}

	// Mark as handled
	if err := a.messageStore.MarkReadReceiptHandled(messageID); err != nil {
		log.Warn().Err(err).Str("message_id", messageID).Msg("Failed to mark read receipt as handled")
	}

	log.Info().
		Str("message_id", messageID).
		Str("to", recipientEmail).
		Msg("Read receipt sent")

	return nil
}

// IgnoreReadReceipt marks a message's read receipt request as ignored (handled without sending)
func (a *App) IgnoreReadReceipt(accountID, messageID string) error {
	log := logging.WithComponent("app")

	// Mark as handled without sending
	if err := a.messageStore.MarkReadReceiptHandled(messageID); err != nil {
		return fmt.Errorf("failed to mark read receipt as handled: %w", err)
	}

	log.Info().
		Str("message_id", messageID).
		Msg("Read receipt ignored")

	return nil
}

// extractEmailFromHeader extracts the email address from a header value
// e.g., "John Doe <john@example.com>" -> "john@example.com"
func extractEmailFromHeader(header string) string {
	header = strings.TrimSpace(header)

	// Check if it's in "Name <email>" format
	if start := strings.Index(header, "<"); start != -1 {
		if end := strings.Index(header, ">"); end > start {
			return header[start+1 : end]
		}
	}

	// Otherwise, assume it's just an email address
	return header
}
