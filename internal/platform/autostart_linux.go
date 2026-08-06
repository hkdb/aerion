package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/hkdb/aerion/internal/logging"
)

const (
	autostartFilename = "io.github.hkdb.Aerion.desktop"
	desktopEntryTmpl  = `[Desktop Entry]
Type=Application
Name=Aerion
Comment=Aerion Email Client
Exec=%s
Icon=io.github.hkdb.Aerion
Terminal=false
Categories=Network;Email;
X-GNOME-Autostart-enabled=true
`
)

// linuxAutostartManager manages autostart on Linux.
// Non-Flatpak: XDG autostart .desktop files.
// Flatpak: org.freedesktop.portal.Background portal.
type linuxAutostartManager struct {
	isFlatpak bool
}

// NewAutostartManager creates a new autostart manager.
func NewAutostartManager() AutostartManager {
	return &linuxAutostartManager{
		isFlatpak: os.Getenv("FLATPAK_ID") != "",
	}
}

// Enable creates the autostart entry.
func (m *linuxAutostartManager) Enable() error {
	if m.isFlatpak {
		return m.portalRequestBackground(true)
	}
	return m.xdgEnable()
}

// Disable removes the autostart entry.
func (m *linuxAutostartManager) Disable() error {
	if m.isFlatpak {
		return m.portalRequestBackground(false)
	}
	return m.xdgDisable()
}

// IsEnabled checks if autostart is currently configured.
func (m *linuxAutostartManager) IsEnabled() bool {
	if m.isFlatpak {
		// The Background portal doesn't provide a query method.
		// The app tracks state via the settings store instead.
		return false
	}
	return m.xdgIsEnabled()
}

// portalRequestBackground requests autostart via the Background portal.
// The portal may show a system dialog for user confirmation.
func (m *linuxAutostartManager) portalRequestBackground(autostart bool) error {
	log := logging.WithComponent("autostart")

	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("failed to connect to session bus: %w", err)
	}
	// Don't close conn — it's the shared session bus used by GTK/WebKit

	// Check if the portal service is actually running before calling it.
	var hasOwner bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.portal.Desktop").Store(&hasOwner); err != nil || !hasOwner {
		return fmt.Errorf("portal service not available")
	}

	// Generate handle token so we know the request object path in advance.
	handleToken := fmt.Sprintf("aerion_%d", time.Now().UnixNano())

	// Compute expected request path from our unique bus name and handle token.
	// Unique name is like ":1.42" → remove ":" and replace "." with "_" → "1_42"
	sender := conn.Names()[0]
	senderPath := strings.ReplaceAll(sender[1:], ".", "_")
	requestPath := dbus.ObjectPath(fmt.Sprintf(
		"/org/freedesktop/portal/desktop/request/%s/%s", senderPath, handleToken,
	))

	// Subscribe to Response signal BEFORE calling the method to avoid races.
	matchRule := fmt.Sprintf(
		"type='signal',interface='org.freedesktop.portal.Request',member='Response',path='%s'",
		requestPath,
	)
	if err := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule).Err; err != nil {
		return fmt.Errorf("failed to subscribe to portal response: %w", err)
	}
	defer conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, matchRule)

	signals := make(chan *dbus.Signal, 1)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	// Build options for RequestBackground.
	// Pass commandline explicitly to avoid broken escaping from the portal's
	// auto-generated flatpak run command. The start-hidden behavior is
	// controlled by the app's own settings, not a CLI flag.
	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(handleToken),
		"reason":       dbus.MakeVariant("Start automatically on login and sync email in the background"),
		"autostart":    dbus.MakeVariant(autostart),
		"commandline":  dbus.MakeVariant([]string{"aerion"}),
	}

	obj := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	call := obj.Call("org.freedesktop.portal.Background.RequestBackground", 0, "", options)
	if call.Err != nil {
		return fmt.Errorf("RequestBackground failed: %w", call.Err)
	}

	// Wait for the Response signal with a timeout.
	timeout := time.After(30 * time.Second)
	for {
		select {
		case signal := <-signals:
			if signal == nil {
				continue
			}
			if signal.Path != requestPath || signal.Name != "org.freedesktop.portal.Request.Response" {
				continue
			}
			if len(signal.Body) == 0 {
				return fmt.Errorf("empty response from Background portal")
			}
			response, ok := signal.Body[0].(uint32)
			if !ok {
				return fmt.Errorf("unexpected response type from Background portal")
			}
			// 0 = success, 1 = user cancelled, 2 = other error
			if response != 0 {
				return fmt.Errorf("autostart request denied (response: %d)", response)
			}

			if autostart {
				log.Info().Msg("Autostart enabled via Background portal")
			} else {
				log.Info().Msg("Autostart disabled via Background portal")
			}
			return nil

		case <-timeout:
			return fmt.Errorf("Background portal request timed out")
		}
	}
}

// xdgEnable creates the autostart .desktop file.
func (m *linuxAutostartManager) xdgEnable() error {
	log := logging.WithComponent("autostart")

	dir, err := m.autostartDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create autostart directory: %w", err)
	}

	execCmd := m.execCommand()
	content := fmt.Sprintf(desktopEntryTmpl, execCmd)

	path := filepath.Join(dir, autostartFilename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write autostart file: %w", err)
	}

	log.Info().Str("path", path).Msg("Autostart enabled")
	return nil
}

// xdgDisable removes the autostart .desktop file.
func (m *linuxAutostartManager) xdgDisable() error {
	log := logging.WithComponent("autostart")

	dir, err := m.autostartDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, autostartFilename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove autostart file: %w", err)
	}

	log.Info().Str("path", path).Msg("Autostart disabled")
	return nil
}

// xdgIsEnabled checks if the autostart .desktop file exists.
func (m *linuxAutostartManager) xdgIsEnabled() bool {
	dir, err := m.autostartDir()
	if err != nil {
		return false
	}

	path := filepath.Join(dir, autostartFilename)
	_, err = os.Stat(path)
	return err == nil
}

// autostartDir returns the XDG autostart directory.
func (m *linuxAutostartManager) autostartDir() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "autostart"), nil
}

// execCommand returns the path to the current executable.
func (m *linuxAutostartManager) execCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "aerion"
	}
	return exe
}
