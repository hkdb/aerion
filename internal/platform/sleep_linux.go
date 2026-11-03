//go:build linux

package platform

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/hkdb/aerion/internal/logging"
)

// LinuxSleepWakeMonitor monitors sleep/wake events on Linux using D-Bus
type LinuxSleepWakeMonitor struct {
	conn     *dbus.Conn
	events   chan SleepWakeEvent
	stopChan chan struct{}
	running  bool
}

// NewSleepWakeMonitor creates a new sleep/wake monitor for Linux
func NewSleepWakeMonitor() SleepWakeMonitor {
	return &LinuxSleepWakeMonitor{
		events:   make(chan SleepWakeEvent, 10),
		stopChan: make(chan struct{}),
	}
}

// Start begins monitoring for sleep/wake events via D-Bus
func (m *LinuxSleepWakeMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("sleep-wake")

	if m.running {
		return nil
	}

	// Connect to the system D-Bus
	conn, err := dbus.SystemBus()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to connect to system D-Bus for sleep/wake monitoring")
		return err
	}
	m.conn = conn

	// Subscribe to PrepareForSleep signal from systemd-logind
	// Signal: org.freedesktop.login1.Manager.PrepareForSleep(boolean going_to_sleep)
	// - true = system is about to sleep
	// - false = system just woke up
	matchRule := "type='signal',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'"
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)
	if call.Err != nil {
		log.Warn().Err(call.Err).Msg("Failed to add D-Bus match rule for PrepareForSleep")
		conn.Close()
		m.conn = nil
		return call.Err
	}

	m.running = true

	// Start listening for signals in a goroutine
	go m.listenForSignals(ctx)

	log.Info().Msg("Sleep/wake monitor started (D-Bus)")
	return nil
}

// listenForSignals listens for D-Bus signals
func (m *LinuxSleepWakeMonitor) listenForSignals(ctx context.Context) {
	log := logging.WithComponent("sleep-wake")

	// Create signal channel
	signals := make(chan *dbus.Signal, 10)
	m.conn.Signal(signals)

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Context cancelled, stopping sleep/wake listener")
			return

		case <-m.stopChan:
			log.Info().Msg("Stop requested, stopping sleep/wake listener")
			return

		case signal := <-signals:
			if signal == nil {
				continue
			}

			// Check if this is the PrepareForSleep signal
			if signal.Name == "org.freedesktop.login1.Manager.PrepareForSleep" {
				if len(signal.Body) > 0 {
					if isSleeping, ok := signal.Body[0].(bool); ok {
						event := SleepWakeEvent{
							IsSleeping: isSleeping,
							Timestamp:  time.Now(),
						}

						if isSleeping {
							log.Info().Msg("System is going to sleep")
						} else {
							log.Info().Msg("System woke from sleep")
						}

						// Non-blocking send to events channel
						select {
						case m.events <- event:
						default:
							log.Warn().Msg("Sleep/wake event channel full, dropping event")
						}
					}
				}
			}
		}
	}
}

// Events returns the channel for receiving sleep/wake events
func (m *LinuxSleepWakeMonitor) Events() <-chan SleepWakeEvent {
	return m.events
}

// Stop stops the monitor and cleans up resources
func (m *LinuxSleepWakeMonitor) Stop() error {
	log := logging.WithComponent("sleep-wake")

	if !m.running {
		return nil
	}

	m.running = false

	// Signal stop
	close(m.stopChan)

	// Close D-Bus connection
	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}

	log.Info().Msg("Sleep/wake monitor stopped")
	return nil
}
