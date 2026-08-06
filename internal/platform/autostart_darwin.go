package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hkdb/aerion/internal/logging"
)

const (
	launchAgentLabel = "io.github.hkdb.Aerion"
	launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.hkdb.Aerion</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
)

// darwinAutostartManager manages macOS Launch Agent plist files.
type darwinAutostartManager struct{}

// NewAutostartManager creates a new autostart manager.
func NewAutostartManager() AutostartManager {
	return &darwinAutostartManager{}
}

// Enable creates the Launch Agent plist file.
func (m *darwinAutostartManager) Enable() error {
	log := logging.WithComponent("autostart")

	dir, err := m.launchAgentsDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	execPath := m.execCommand()
	content := fmt.Sprintf(launchAgentPlist, execPath)

	path := filepath.Join(dir, launchAgentLabel+".plist")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write Launch Agent plist: %w", err)
	}

	log.Info().Str("path", path).Msg("Autostart enabled")
	return nil
}

// Disable removes the Launch Agent plist file.
func (m *darwinAutostartManager) Disable() error {
	log := logging.WithComponent("autostart")

	dir, err := m.launchAgentsDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, launchAgentLabel+".plist")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove Launch Agent plist: %w", err)
	}

	log.Info().Str("path", path).Msg("Autostart disabled")
	return nil
}

// IsEnabled checks if the Launch Agent plist file exists.
func (m *darwinAutostartManager) IsEnabled() bool {
	dir, err := m.launchAgentsDir()
	if err != nil {
		return false
	}

	path := filepath.Join(dir, launchAgentLabel+".plist")
	_, err = os.Stat(path)
	return err == nil
}

// launchAgentsDir returns the user's LaunchAgents directory.
func (m *darwinAutostartManager) launchAgentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// execCommand returns the path to the current executable.
func (m *darwinAutostartManager) execCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "aerion"
	}
	return exe
}
