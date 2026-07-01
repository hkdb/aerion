// Package settings provides global application settings storage
package settings

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/hkdb/aerion/internal/database"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
	_ "modernc.org/sqlite"
)

// Known setting keys
const (
	KeyReadReceiptResponsePolicy = "read_receipt_response_policy"
	KeyMarkAsReadDelay           = "mark_as_read_delay"
	KeyMessageListDensity        = "message_list_density"
	KeyMessageListSortOrder      = "message_list_sort_order"
	KeyThemeMode                 = "theme_mode"
	KeyShowTitleBar              = "show_title_bar"
	KeyTermsAccepted             = "terms_accepted"
	KeyRunBackground             = "run_background"
	KeyStartHidden               = "start_hidden"
	KeyAutostart                 = "autostart"
	KeyLanguage                  = "language"
	KeyComposerMode              = "composer_mode"
	KeyMailtoMode                = "mailto_mode"
	KeyComposerFormat            = "composer_format"
	KeyNativeTitleBar            = "native_titlebar"
	KeyAlwaysLoadImages          = "always_load_images"
	KeyDarkMailContent           = "dark_mail_content"
	KeyOverrideEmailColors       = "override_email_colors"
	KeyAccentBarUnread           = "accent_bar_unread"
	KeyShowMessageListCircles    = "show_message_list_circles"
	KeyShowViewerCircles         = "show_viewer_circles"
	KeyLastSeenVersion           = "last_seen_version"      // for "What's new in this version" launch dialog
	KeyOAuthWarningDisabled      = "oauth_warning_disabled" // user toggled "Don't show again" on the missing-OAuth-creds launch warning
	KeyShowActionToasts          = "show_action_toasts"
)

// Extension enable/disable keys. Format: extension_<name>_enabled.
// All extensions default to disabled — minimalists see no UI changes until
// they explicitly opt in. Phase 1 reserves keys only for confirmed first-
// party extensions (Calendar, Contacts).
const (
	KeyExtensionCalendarEnabled = "extension_calendar_enabled"
	KeyExtensionContactsEnabled = "extension_contacts_enabled"
)

// AllExtensionKeys is the list of all known first-party extension names. Add
// a new extension's name here when its enable/disable key is reserved above.
// IsExtensionEnabled / SetExtensionEnabled work on names from this list.
var AllExtensionKeys = []string{
	"calendar",
	"contacts",
}

// Density values for message list
const (
	DensityMicro    = "micro"
	DensityCompact  = "compact"
	DensityStandard = "standard"
	DensityLarge    = "large"
)

// DefaultMessageListDensity is the default density
const DefaultMessageListDensity = DensityStandard

// Sort order values for message list
const (
	SortOrderNewest = "newest"
	SortOrderOldest = "oldest"
)

// DefaultMessageListSortOrder is the default sort order
const DefaultMessageListSortOrder = SortOrderNewest

// Theme mode values
const (
	ThemeModeSystem      = "system"
	ThemeModeLight       = "light"
	ThemeModeLightBlue   = "light-blue"
	ThemeModeLightOrange   = "light-orange"
	ThemeModeLightBalanced = "light-balanced"
	ThemeModeAdwaitaLight  = "adwaita-light"
	ThemeModeBreezeLight   = "breeze-light"
	ThemeModeDark          = "dark"
	ThemeModeDarkGray     = "dark-gray"
	ThemeModeDarkBalanced = "dark-balanced"
	ThemeModeAdwaitaDark  = "adwaita-dark"
	ThemeModeBreezeDark   = "breeze-dark"
	ThemeModeCatppuccinLatte     = "catppuccin-latte"
	ThemeModeCatppuccinFrappe    = "catppuccin-frappe"
	ThemeModeCatppuccinMacchiato = "catppuccin-macchiato"
	ThemeModeCatppuccinMocha     = "catppuccin-mocha"
	ThemeModeDracula         = "dracula"
	ThemeModeGithubLight     = "github-light"
	ThemeModeGithubDark      = "github-dark"
	ThemeModeGithubSoftDark  = "github-soft-dark"
	ThemeModeTokyoNight      = "tokyo-night"
	ThemeModeNordLight       = "nord-light"
	ThemeModeNordDark        = "nord-dark"
	ThemeModePopLight        = "pop-light"
	ThemeModePopDark         = "pop-dark"
	ThemeModeYaruLight       = "yaru-light"
	ThemeModeYaruDark        = "yaru-dark"
	ThemeModeVSCodeLight     = "vs-code-light"
	ThemeModeVSCodeDark      = "vs-code-dark"
	ThemeModeEthereal = "ethereal"
	ThemeModeEverforest = "everforest"
	ThemeModeFlexokiLight = "flexoki-light"
	ThemeModeGruvbox = "gruvbox"
	ThemeModeHackerman = "hackerman"
	ThemeModeKanagawa = "kanagawa"
	ThemeModeLumon = "lumon"
	ThemeModeMatteBlack = "matte-black"
	ThemeModeMiasma = "miasma"
	ThemeModeOsakaJade = "osaka-jade"
	ThemeModeRetro82 = "retro-82"
	ThemeModeRistretto = "ristretto"
	ThemeModeRosePine = "rose-pine"
	ThemeModeVantablack = "vantablack"
	ThemeModeWhite = "white"
	ThemeModeFlexokiDark = "flexoki-dark"
)

// DefaultThemeMode is the default theme mode
const DefaultThemeMode = ThemeModeSystem

// Composer mode values
const (
	ComposerModeInline   = "inline"
	ComposerModeDetached = "detached"
)

// DefaultComposerMode is the default compose mode
const DefaultComposerMode = ComposerModeInline

// Composer format values
const (
	ComposerFormatRich  = "rich"
	ComposerFormatPlain = "plain"
)

// DefaultComposerFormat is the default composer format
const DefaultComposerFormat = ComposerFormatRich

// Policy values for read receipts
const (
	PolicyNever  = "never"
	PolicyAsk    = "ask"
	PolicyAlways = "always"
)

// Default mark as read delay in milliseconds (1 second)
const DefaultMarkAsReadDelay = 1000

// Store provides settings persistence operations
type Store struct {
	db  *database.DB
	log zerolog.Logger
}

// NewStore creates a new settings store
func NewStore(db *database.DB) *Store {
	return &Store{
		db:  db,
		log: logging.WithComponent("settings-store"),
	}
}

// Get retrieves a setting value by key
func (s *Store) Get(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %w", key, err)
	}
	return value, nil
}

// Set sets a setting value
func (s *Store) Set(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set setting %s: %w", key, err)
	}

	s.log.Debug().Str("key", key).Str("value", value).Msg("Setting updated")
	return nil
}

// IsExtensionEnabled returns whether the given first-party extension is
// enabled. Unknown / not-yet-set extensions return (false, nil) — the
// app should treat "not present in settings" as disabled.
func (s *Store) IsExtensionEnabled(name string) (bool, error) {
	value, err := s.Get("extension_" + name + "_enabled")
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetExtensionEnabled writes the enable/disable flag for the given first-party
// extension. The Wails-bound App method that wraps this is the only entry
// point the frontend uses to toggle extensions on/off.
func (s *Store) SetExtensionEnabled(name string, enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set("extension_"+name+"_enabled", v)
}

// GetReadReceiptResponsePolicy returns the current read receipt response policy
func (s *Store) GetReadReceiptResponsePolicy() (string, error) {
	value, err := s.Get(KeyReadReceiptResponsePolicy)
	if err != nil {
		return PolicyAsk, err
	}
	if value == "" {
		return PolicyAsk, nil // Default
	}
	return value, nil
}

// SetReadReceiptResponsePolicy sets the read receipt response policy
func (s *Store) SetReadReceiptResponsePolicy(policy string) error {
	// Validate policy
	if policy != PolicyNever && policy != PolicyAsk && policy != PolicyAlways {
		return fmt.Errorf("invalid policy: %s (must be 'never', 'ask', or 'always')", policy)
	}
	return s.Set(KeyReadReceiptResponsePolicy, policy)
}

// GetMarkAsReadDelay returns the delay before marking messages as read (in milliseconds)
// Returns: -1 = manual only, 0 = immediate, >0 = delay in ms
func (s *Store) GetMarkAsReadDelay() (int, error) {
	value, err := s.Get(KeyMarkAsReadDelay)
	if err != nil {
		return DefaultMarkAsReadDelay, err
	}
	if value == "" {
		return DefaultMarkAsReadDelay, nil
	}
	delay, err := strconv.Atoi(value)
	if err != nil {
		return DefaultMarkAsReadDelay, nil
	}
	return delay, nil
}

// SetMarkAsReadDelay sets the delay before marking messages as read (in milliseconds)
// Valid values: -1 (manual only), 0 (immediate), or 100-5000 (delay in ms)
func (s *Store) SetMarkAsReadDelay(delayMs int) error {
	// Validate delay
	if delayMs < -1 {
		return fmt.Errorf("invalid delay: %d (must be -1, 0, or 100-5000)", delayMs)
	}
	if delayMs > 0 && delayMs < 100 {
		return fmt.Errorf("invalid delay: %d (minimum non-zero delay is 100ms)", delayMs)
	}
	if delayMs > 5000 {
		return fmt.Errorf("invalid delay: %d (maximum delay is 5000ms)", delayMs)
	}
	return s.Set(KeyMarkAsReadDelay, strconv.Itoa(delayMs))
}

// GetMessageListDensity returns the current message list density setting
func (s *Store) GetMessageListDensity() (string, error) {
	value, err := s.Get(KeyMessageListDensity)
	if err != nil {
		return DefaultMessageListDensity, err
	}
	if value == "" {
		return DefaultMessageListDensity, nil
	}
	return value, nil
}

// SetMessageListDensity sets the message list density
func (s *Store) SetMessageListDensity(density string) error {
	if density != DensityMicro && density != DensityCompact && density != DensityStandard && density != DensityLarge {
		return fmt.Errorf("invalid density: %s (must be 'micro', 'compact', 'standard', or 'large')", density)
	}
	return s.Set(KeyMessageListDensity, density)
}

// GetAccentBarUnread returns whether the accent bar for unread messages is enabled
func (s *Store) GetAccentBarUnread() (bool, error) {
	value, err := s.Get(KeyAccentBarUnread)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetAccentBarUnread enables or disables the accent bar for unread messages
func (s *Store) SetAccentBarUnread(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set(KeyAccentBarUnread, v)
}

// GetShowActionToasts returns whether action toasts are enabled
func (s *Store) GetShowActionToasts() (bool, error) {
	value, err := s.Get(KeyShowActionToasts)
	if err != nil {
		return true, err
	}
	if value == "" {
		return true, nil
	}
	return value == "true", nil
}

// SetShowActionToasts enables or disables action toasts
func (s *Store) SetShowActionToasts(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set(KeyShowActionToasts, v)
}

// GetShowMessageListCircles returns whether colored sender circles
// are shown in the message list. Default: true.
func (s *Store) GetShowMessageListCircles() (bool, error) {
	value, err := s.Get(KeyShowMessageListCircles)
	if err != nil {
		return true, err
	}
	if value == "" {
		return true, nil
	}
	return value == "true", nil
}

// SetShowMessageListCircles enables or disables colored sender circles in the message list
func (s *Store) SetShowMessageListCircles(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set(KeyShowMessageListCircles, v)
}

// GetShowViewerCircles returns whether colored sender circles
// are shown in the conversation viewer. Default: true.
func (s *Store) GetShowViewerCircles() (bool, error) {
	value, err := s.Get(KeyShowViewerCircles)
	if err != nil {
		return true, err
	}
	if value == "" {
		return true, nil
	}
	return value == "true", nil
}

// SetShowViewerCircles enables or disables colored sender circles in the conversation viewer
func (s *Store) SetShowViewerCircles(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set(KeyShowViewerCircles, v)
}

// GetMessageListSortOrder returns the current message list sort order
func (s *Store) GetMessageListSortOrder() (string, error) {
	value, err := s.Get(KeyMessageListSortOrder)
	if err != nil {
		return DefaultMessageListSortOrder, err
	}
	if value == "" {
		return DefaultMessageListSortOrder, nil
	}
	return value, nil
}

// SetMessageListSortOrder sets the message list sort order
func (s *Store) SetMessageListSortOrder(sortOrder string) error {
	if sortOrder != SortOrderNewest && sortOrder != SortOrderOldest {
		return fmt.Errorf("invalid sort order: %s (must be 'newest' or 'oldest')", sortOrder)
	}
	return s.Set(KeyMessageListSortOrder, sortOrder)
}

// GetThemeMode returns the current theme mode setting
func (s *Store) GetThemeMode() (string, error) {
	value, err := s.Get(KeyThemeMode)
	if err != nil {
		return DefaultThemeMode, err
	}
	if value == "" {
		return DefaultThemeMode, nil
	}
	return value, nil
}

// SetThemeMode sets the theme mode
func (s *Store) SetThemeMode(mode string) error {
	switch mode {
	case ThemeModeSystem,
		ThemeModeLight,
		ThemeModeLightBlue,
		ThemeModeLightOrange,
		ThemeModeLightBalanced,
		ThemeModeAdwaitaLight,
		ThemeModeBreezeLight,
		ThemeModeDark,
		ThemeModeDarkGray,
		ThemeModeDarkBalanced,
		ThemeModeAdwaitaDark,
		ThemeModeBreezeDark,
		ThemeModeCatppuccinLatte,
		ThemeModeCatppuccinFrappe,
		ThemeModeCatppuccinMacchiato,
		ThemeModeCatppuccinMocha,
		ThemeModeDracula,
		ThemeModeGithubLight,
		ThemeModeGithubDark,
		ThemeModeGithubSoftDark,
		ThemeModeTokyoNight,
		ThemeModeNordLight,
		ThemeModeNordDark,
		ThemeModePopLight,
		ThemeModePopDark,
		ThemeModeVSCodeLight,
		ThemeModeVSCodeDark,
		ThemeModeYaruLight,
		ThemeModeYaruDark,
		ThemeModeEthereal,
		ThemeModeEverforest,
		ThemeModeFlexokiLight,
		ThemeModeGruvbox,
		ThemeModeHackerman,
		ThemeModeKanagawa,
		ThemeModeLumon,
		ThemeModeMatteBlack,
		ThemeModeMiasma,
		ThemeModeOsakaJade,
		ThemeModeRetro82,
		ThemeModeRistretto,
		ThemeModeRosePine,
		ThemeModeVantablack,
		ThemeModeWhite,
		ThemeModeFlexokiDark:
		return s.Set(KeyThemeMode, mode)
	default:
		return fmt.Errorf("invalid theme mode: %s (must be 'system', 'light', 'light-blue', 'light-orange', 'light-balanced', 'adwaita-light', 'breeze-light', 'dark', 'dark-gray', 'dark-balanced', 'adwaita-dark', 'breeze-dark', 'catppuccin-latte', 'catppuccin-frappe', 'catppuccin-macchiato', 'catppuccin-mocha', 'dracula', 'github-light', 'github-dark', 'github-soft-dark', 'tokyo-night', 'nord-light', 'nord-dark', 'pop-light', 'pop-dark', 'vs-code-light', 'vs-code-dark', 'yaru-light', 'yaru-dark', 'ethereal', 'everforest', 'flexoki-light', 'gruvbox', 'hackerman', 'kanagawa', 'lumon', 'matte-black', 'miasma', 'osaka-jade', 'retro-82', 'ristretto', 'rose-pine', 'vantablack', 'white', or 'flexoki-dark')", mode)
	}
}

// GetShowTitleBar returns whether the title bar should be shown
func (s *Store) GetShowTitleBar() (bool, error) {
	value, err := s.Get(KeyShowTitleBar)
	if err != nil {
		return true, err // Default to true (shown)
	}
	if value == "" {
		return true, nil // Default to true (shown)
	}
	return value == "true", nil
}

// SetShowTitleBar sets whether the title bar should be shown
func (s *Store) SetShowTitleBar(show bool) error {
	value := "false"
	if show {
		value = "true"
	}
	return s.Set(KeyShowTitleBar, value)
}

// GetTermsAccepted returns whether the user has accepted the terms of service
func (s *Store) GetTermsAccepted() (bool, error) {
	value, err := s.Get(KeyTermsAccepted)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetTermsAccepted sets whether the user has accepted the terms of service
func (s *Store) SetTermsAccepted(accepted bool) error {
	value := "false"
	if accepted {
		value = "true"
	}
	return s.Set(KeyTermsAccepted, value)
}

// GetLastSeenVersion returns the Aerion version that was running the last time
// the "What's new in this version" dialog was acknowledged with OK. Empty
// string means it's never been acknowledged (e.g. fresh install).
func (s *Store) GetLastSeenVersion() (string, error) {
	return s.Get(KeyLastSeenVersion)
}

// SetLastSeenVersion records the current Aerion version as acknowledged so the
// What's New dialog doesn't fire again until the next version upgrade.
func (s *Store) SetLastSeenVersion(version string) error {
	return s.Set(KeyLastSeenVersion, version)
}

// GetOAuthWarningDisabled returns whether the user has opted out of the
// missing-OAuth-creds launch warning via the dialog's "Don't show again"
// toggle. Defaults to false on first launch (key unset).
func (s *Store) GetOAuthWarningDisabled() (bool, error) {
	value, err := s.Get(KeyOAuthWarningDisabled)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetOAuthWarningDisabled persists the user's "Don't show again" choice from
// the OAuth-credentials-missing launch warning.
func (s *Store) SetOAuthWarningDisabled(disabled bool) error {
	value := "false"
	if disabled {
		value = "true"
	}
	return s.Set(KeyOAuthWarningDisabled, value)
}

// GetRunBackground returns whether Aerion should keep running when the window is closed
func (s *Store) GetRunBackground() (bool, error) {
	value, err := s.Get(KeyRunBackground)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetRunBackground sets whether Aerion should keep running when the window is closed
func (s *Store) SetRunBackground(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyRunBackground, value)
}

// GetStartHidden returns whether Aerion should start with the window hidden
func (s *Store) GetStartHidden() (bool, error) {
	value, err := s.Get(KeyStartHidden)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetStartHidden sets whether Aerion should start with the window hidden
func (s *Store) SetStartHidden(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyStartHidden, value)
}

// GetAutostart returns whether Aerion should start on login
func (s *Store) GetAutostart() (bool, error) {
	value, err := s.Get(KeyAutostart)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetAutostart sets whether Aerion should start on login
func (s *Store) SetAutostart(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyAutostart, value)
}

// GetLanguage returns the saved language preference (locale code)
// Returns empty string if not set (frontend uses system detection)
func (s *Store) GetLanguage() (string, error) {
	return s.Get(KeyLanguage)
}

// SetLanguage sets the language preference (locale code, e.g. "en", "zh-TW", "zh-CN")
func (s *Store) SetLanguage(language string) error {
	return s.Set(KeyLanguage, language)
}

// GetComposerMode returns the default compose mode ("inline" or "detached")
func (s *Store) GetComposerMode() (string, error) {
	value, err := s.Get(KeyComposerMode)
	if err != nil {
		return DefaultComposerMode, err
	}
	if value == "" {
		return DefaultComposerMode, nil
	}
	return value, nil
}

// SetComposerMode sets the default compose mode
func (s *Store) SetComposerMode(mode string) error {
	if mode != ComposerModeInline && mode != ComposerModeDetached {
		return fmt.Errorf("invalid composer mode: %s (must be 'inline' or 'detached')", mode)
	}
	return s.Set(KeyComposerMode, mode)
}

// GetMailtoMode returns the external mailto link handling mode ("inline" or "detached")
func (s *Store) GetMailtoMode() (string, error) {
	value, err := s.Get(KeyMailtoMode)
	if err != nil {
		return DefaultComposerMode, err
	}
	if value == "" {
		return DefaultComposerMode, nil
	}
	return value, nil
}

// SetMailtoMode sets the external mailto link handling mode
func (s *Store) SetMailtoMode(mode string) error {
	if mode != ComposerModeInline && mode != ComposerModeDetached {
		return fmt.Errorf("invalid mailto mode: %s (must be 'inline' or 'detached')", mode)
	}
	return s.Set(KeyMailtoMode, mode)
}

// GetComposerFormat returns the default composer format ("rich" or "plain")
func (s *Store) GetComposerFormat() (string, error) {
	value, err := s.Get(KeyComposerFormat)
	if err != nil {
		return DefaultComposerFormat, err
	}
	if value == "" {
		return DefaultComposerFormat, nil
	}
	return value, nil
}

// SetComposerFormat sets the default composer format
func (s *Store) SetComposerFormat(format string) error {
	if format != ComposerFormatRich && format != ComposerFormatPlain {
		return fmt.Errorf("invalid composer format: %s (must be 'rich' or 'plain')", format)
	}
	return s.Set(KeyComposerFormat, format)
}

// GetNativeTitleBar returns whether the native OS title bar should be used
func (s *Store) GetNativeTitleBar() (bool, error) {
	value, err := s.Get(KeyNativeTitleBar)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetNativeTitleBar sets whether the native OS title bar should be used
func (s *Store) SetNativeTitleBar(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyNativeTitleBar, value)
}

// GetAlwaysLoadImages returns whether remote images should always be loaded
func (s *Store) GetAlwaysLoadImages() (bool, error) {
	value, err := s.Get(KeyAlwaysLoadImages)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetAlwaysLoadImages sets whether remote images should always be loaded
func (s *Store) SetAlwaysLoadImages(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyAlwaysLoadImages, value)
}

// GetDarkMailContent returns whether email content should be visually darkened
// while Aerion is in dark mode. Off by default.
func (s *Store) GetDarkMailContent() (bool, error) {
	value, err := s.Get(KeyDarkMailContent)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetDarkMailContent persists the dark-mail-content toggle.
func (s *Store) SetDarkMailContent(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyDarkMailContent, value)
}

// GetOverrideEmailColors returns whether email content background is forced to transparent
// and text/link colors match the theme. Off by default.
func (s *Store) GetOverrideEmailColors() (bool, error) {
	value, err := s.Get(KeyOverrideEmailColors)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// SetOverrideEmailColors persists the override-email-colors toggle.
func (s *Store) SetOverrideEmailColors(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return s.Set(KeyOverrideEmailColors, value)
}

// ReadNativeTitleBar opens the database directly to read the native_titlebar setting.
// Used in main.go before wails.Run() when the full DB isn't initialized yet.
// Returns false on any error (first run, missing DB, etc.).
func ReadNativeTitleBar(dbPath string) bool {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return false
	}
	defer db.Close()

	var value string
	err = db.QueryRow("SELECT value FROM settings WHERE key = ?", KeyNativeTitleBar).Scan(&value)
	if err != nil {
		return false
	}
	return value == "true"
}

// WriteThemeMode opens the database directly to write the theme_mode setting.
// Used when updating the theme from CLI while the main window is not running.
func WriteThemeMode(dbPath string, mode string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
		INSERT INTO settings (key, value) VALUES ('theme_mode', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, mode)
	return err
}

