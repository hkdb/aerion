//go:build windows

package platform

import (
	"context"
	"net"
	"sync"
	"time"
	"unsafe"

	"github.com/hkdb/aerion/internal/logging"
	"golang.org/x/sys/windows"
)

var (
	iphlpapi                    = windows.NewLazySystemDLL("iphlpapi.dll")
	procNotifyIpInterfaceChange = iphlpapi.NewProc("NotifyIpInterfaceChange")
	procCancelMibChangeNotify2  = iphlpapi.NewProc("CancelMibChangeNotify2")
)

// AF_UNSPEC = 0 (all address families)
const afUnspec = 0

// WindowsNetworkMonitor monitors network connectivity on Windows
// using NotifyIpInterfaceChange from iphlpapi.dll.
type WindowsNetworkMonitor struct {
	events    chan NetworkEvent
	stopChan  chan struct{}
	notifyCh  chan struct{} // signaled on connectivity change for WaitForConnection
	running   bool
	connected bool
	handle    windows.Handle
	mu        sync.RWMutex
}

// package-level singleton so the callback can reach the Go instance
var windowsNetMon *WindowsNetworkMonitor

// NewNetworkMonitor creates a new network connectivity monitor for Windows
func NewNetworkMonitor() NetworkMonitor {
	return &WindowsNetworkMonitor{
		events:    make(chan NetworkEvent, 10),
		stopChan:  make(chan struct{}),
		notifyCh:  make(chan struct{}, 1),
		connected: true, // assume connected until proven otherwise
	}
}

// ipInterfaceChangeCallback is called by Windows when IP interface state changes.
// Parameters match PIPINTERFACE_CHANGE_CALLBACK signature:
//
//	(PVOID CallerContext, PMIB_IPINTERFACE_ROW Row, MIB_NOTIFICATION_TYPE NotificationType)
func ipInterfaceChangeCallback(callerContext, row, notificationType uintptr) uintptr {
	mon := windowsNetMon
	if mon == nil {
		return 0
	}

	connected := checkConnected()
	mon.updateState(connected)
	return 0
}

// checkConnected checks if there is at least one non-loopback, up interface
// with a unicast address. Returns true on error (assume connected).
func checkConnected() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true // assume connected on error
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if len(addrs) > 0 {
			return true
		}
	}

	return false
}

// Start begins monitoring for network connectivity changes
func (m *WindowsNetworkMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("network-monitor")

	if m.running {
		return nil
	}

	windowsNetMon = m

	// Get initial connectivity state
	m.connected = checkConnected()
	m.running = true

	// Register for IP interface change notifications
	var handle windows.Handle
	cb := windows.NewCallback(ipInterfaceChangeCallback)

	ret, _, err := procNotifyIpInterfaceChange.Call(
		afUnspec,                         // Family: AF_UNSPEC (all)
		cb,                               // Callback
		0,                                // CallerContext
		0,                                // InitialNotification: FALSE
		uintptr(unsafe.Pointer(&handle)), // NotificationHandle
	)
	if ret != 0 {
		m.running = false
		windowsNetMon = nil
		log.Warn().Err(err).Msg("Failed to register for IP interface change notifications")
		return err
	}

	m.handle = handle

	log.Info().Bool("connected", m.connected).Msg("Network monitor started (NotifyIpInterfaceChange)")
	return nil
}

// updateState updates the connectivity state and emits an event if it changed
func (m *WindowsNetworkMonitor) updateState(connected bool) {
	log := logging.WithComponent("network-monitor")

	m.mu.Lock()
	changed := m.connected != connected
	m.connected = connected
	m.mu.Unlock()

	if !changed {
		return
	}

	event := NetworkEvent{
		Connected: connected,
		Timestamp: time.Now(),
	}

	if connected {
		log.Info().Msg("Network connectivity restored")
	} else {
		log.Info().Msg("Network connectivity lost")
	}

	// Non-blocking send to events channel
	select {
	case m.events <- event:
	default:
		log.Warn().Msg("Network event channel full, dropping event")
	}

	// Signal WaitForConnection waiters
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

// Events returns the channel for receiving network connectivity events
func (m *WindowsNetworkMonitor) Events() <-chan NetworkEvent {
	return m.events
}

// IsConnected returns the current connectivity state
func (m *WindowsNetworkMonitor) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// WaitForConnection blocks until network is available or context is cancelled.
// Returns true if connected, false if context was cancelled.
func (m *WindowsNetworkMonitor) WaitForConnection(ctx context.Context) bool {
	log := logging.WithComponent("network-monitor")

	// Re-check current state
	connected := checkConnected()
	m.mu.Lock()
	m.connected = connected
	m.mu.Unlock()

	if connected {
		return true
	}

	log.Info().Msg("Network not available, waiting for connectivity signal...")

	for {
		select {
		case <-ctx.Done():
			return false
		case <-m.notifyCh:
			if m.IsConnected() {
				return true
			}
		}
	}
}

// Invalidate resets the cached connectivity state to disconnected
func (m *WindowsNetworkMonitor) Invalidate() {
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()
}

// Stop stops the monitor and cleans up resources
func (m *WindowsNetworkMonitor) Stop() error {
	log := logging.WithComponent("network-monitor")

	if !m.running {
		return nil
	}

	m.running = false

	if m.handle != 0 {
		procCancelMibChangeNotify2.Call(uintptr(m.handle))
		m.handle = 0
	}

	windowsNetMon = nil

	log.Info().Msg("Network monitor stopped (Windows)")
	return nil
}
