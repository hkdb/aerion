//go:build linux

package platform

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/hkdb/aerion/internal/logging"
)

const (
	portalDest     = "org.freedesktop.portal.Desktop"
	portalPath     = "/org/freedesktop/portal/desktop"
	settingsIface  = "org.freedesktop.portal.Settings"
	appearanceNS   = "org.freedesktop.appearance"
	colorSchemeKey = "color-scheme"
)

// LinuxThemeMonitor monitors system theme changes via the XDG Settings Portal
type LinuxThemeMonitor struct {
	conn     *dbus.Conn
	events   chan SystemTheme
	stopChan chan struct{}
	running  bool
	current  SystemTheme
}

// NewThemeMonitor creates a new theme monitor for Linux
func NewThemeMonitor() ThemeMonitor {
	return &LinuxThemeMonitor{
		events:   make(chan SystemTheme, 10),
		stopChan: make(chan struct{}),
	}
}

// Start begins monitoring for theme changes via the XDG Settings Portal
func (m *LinuxThemeMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("theme-monitor")

	if m.running {
		return nil
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to connect to session D-Bus for theme monitoring")
		return err
	}
	m.conn = conn

	// Read initial color-scheme value from the portal
	theme, err := m.readColorScheme()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read color-scheme from Settings Portal")
		conn.Close()
		m.conn = nil
		return err
	}
	m.current = theme
	log.Info().Str("theme", string(theme)).Msg("Initial system theme from Settings Portal")

	// Subscribe to SettingChanged signal for live updates
	matchRule := fmt.Sprintf(
		"type='signal',sender='%s',interface='%s',member='SettingChanged',path='%s'",
		portalDest, settingsIface, portalPath,
	)
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)
	if call.Err != nil {
		log.Warn().Err(call.Err).Msg("Failed to subscribe to SettingChanged signal")
		conn.Close()
		m.conn = nil
		return call.Err
	}

	m.running = true
	go m.listenForSignals(ctx)

	log.Info().Msg("Theme monitor started (XDG Settings Portal)")
	return nil
}

// readColorScheme reads the current color-scheme from the Settings Portal
func (m *LinuxThemeMonitor) readColorScheme() (SystemTheme, error) {
	obj := m.conn.Object(portalDest, dbus.ObjectPath(portalPath))
	call := obj.Call(settingsIface+".Read", 0, appearanceNS, colorSchemeKey)
	if call.Err != nil {
		return SystemThemeNoPreference, call.Err
	}

	var outerVariant dbus.Variant
	if err := call.Store(&outerVariant); err != nil {
		return SystemThemeNoPreference, err
	}

	return parseColorScheme(outerVariant)
}

// parseColorScheme unwraps a D-Bus variant containing the color-scheme value.
// The Read method returns variant(variant(uint32)), while SettingChanged
// signals return variant(uint32). This handles both cases.
func parseColorScheme(v dbus.Variant) (SystemTheme, error) {
	inner := v.Value()

	// Unwrap nested variant if present (from Read method)
	if innerVariant, ok := inner.(dbus.Variant); ok {
		inner = innerVariant.Value()
	}

	val, ok := inner.(uint32)
	if !ok {
		return SystemThemeNoPreference, fmt.Errorf("unexpected color-scheme type: %T", inner)
	}

	// 0 = no preference, 1 = prefer dark, 2 = prefer light
	switch val {
	case 1:
		return SystemThemeDark, nil
	case 2:
		return SystemThemeLight, nil
	default:
		return SystemThemeLight, nil
	}
}

// listenForSignals listens for SettingChanged D-Bus signals
func (m *LinuxThemeMonitor) listenForSignals(ctx context.Context) {
	log := logging.WithComponent("theme-monitor")

	signals := make(chan *dbus.Signal, 10)
	m.conn.Signal(signals)

	for {
		select {
		case <-ctx.Done():
			log.Debug().Msg("Context cancelled, stopping theme monitor listener")
			return

		case <-m.stopChan:
			log.Debug().Msg("Stop requested, stopping theme monitor listener")
			return

		case signal := <-signals:
			if signal == nil {
				continue
			}

			if signal.Name != settingsIface+".SettingChanged" {
				continue
			}

			// SettingChanged(namespace string, key string, value variant)
			if len(signal.Body) < 3 {
				continue
			}

			ns, ok1 := signal.Body[0].(string)
			key, ok2 := signal.Body[1].(string)
			if !ok1 || !ok2 || ns != appearanceNS || key != colorSchemeKey {
				continue
			}

			val, ok := signal.Body[2].(dbus.Variant)
			if !ok {
				continue
			}

			theme, err := parseColorScheme(val)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to parse SettingChanged color-scheme value")
				continue
			}

			if theme == m.current {
				continue
			}

			m.current = theme
			log.Info().Str("theme", string(theme)).Msg("System theme changed via Settings Portal")

			// Non-blocking send to events channel
			select {
			case m.events <- theme:
			default:
				log.Warn().Msg("Theme event channel full, dropping event")
			}
		}
	}
}

// GetTheme returns the current system theme preference
func (m *LinuxThemeMonitor) GetTheme() SystemTheme {
	return m.current
}

// Events returns the channel for receiving theme change events
func (m *LinuxThemeMonitor) Events() <-chan SystemTheme {
	return m.events
}

// Stop stops the monitor and cleans up resources
func (m *LinuxThemeMonitor) Stop() error {
	log := logging.WithComponent("theme-monitor")

	if !m.running {
		return nil
	}

	m.running = false
	close(m.stopChan)

	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}

	log.Info().Msg("Theme monitor stopped")
	return nil
}

// ReadSystemTheme performs a one-shot read of the system color-scheme
// from the XDG Settings Portal. Useful for processes that don't need
// live monitoring (e.g., detached composer windows).
func ReadSystemTheme() string {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return string(SystemThemeNoPreference)
	}
	defer conn.Close()

	obj := conn.Object(portalDest, dbus.ObjectPath(portalPath))
	call := obj.Call(settingsIface+".Read", 0, appearanceNS, colorSchemeKey)
	if call.Err != nil {
		return string(SystemThemeNoPreference)
	}

	var outerVariant dbus.Variant
	if err := call.Store(&outerVariant); err != nil {
		return string(SystemThemeNoPreference)
	}

	theme, err := parseColorScheme(outerVariant)
	if err != nil {
		return string(SystemThemeNoPreference)
	}
	return string(theme)
}
