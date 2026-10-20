//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hkdb/aerion/internal/logging"
)

// LinuxPowerMonitor monitors power state on Linux
type LinuxPowerMonitor struct {
	callback func(PowerInfo)
	stopChan chan struct{}
}

// NewPowerMonitor creates a new power monitor for the current platform
func NewPowerMonitor() PowerMonitor {
	return &LinuxPowerMonitor{
		stopChan: make(chan struct{}),
	}
}

// GetPowerInfo returns the current power state
func (m *LinuxPowerMonitor) GetPowerInfo() (*PowerInfo, error) {
	info := &PowerInfo{
		State:             PowerStateUnknown,
		BatteryPercentage: -1,
		IsCharging:        false,
	}

	// Find power supply devices
	powerSupplyPath := "/sys/class/power_supply"
	entries, err := os.ReadDir(powerSupplyPath)
	if err != nil {
		return info, nil // Not an error, just no power info available
	}

	var batteryCapacity int = -1
	var onAC bool
	var hasBattery bool
	var isCharging bool

	for _, entry := range entries {
		devicePath := filepath.Join(powerSupplyPath, entry.Name())
		typePath := filepath.Join(devicePath, "type")

		typeBytes, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}
		deviceType := strings.TrimSpace(string(typeBytes))

		switch deviceType {
		case "Mains":
			// AC adapter
			onlinePath := filepath.Join(devicePath, "online")
			onlineBytes, err := os.ReadFile(onlinePath)
			if err == nil {
				onAC = strings.TrimSpace(string(onlineBytes)) == "1"
			}

		case "Battery":
			hasBattery = true

			// Read capacity
			capacityPath := filepath.Join(devicePath, "capacity")
			capacityBytes, err := os.ReadFile(capacityPath)
			if err == nil {
				capacity, err := strconv.Atoi(strings.TrimSpace(string(capacityBytes)))
				if err == nil {
					batteryCapacity = capacity
				}
			}

			// Read status
			statusPath := filepath.Join(devicePath, "status")
			statusBytes, err := os.ReadFile(statusPath)
			if err == nil {
				status := strings.TrimSpace(string(statusBytes))
				isCharging = status == "Charging"
			}
		}
	}

	// Determine power state
	if onAC {
		info.State = PowerStateAC
	} else if hasBattery {
		if batteryCapacity >= 0 && batteryCapacity <= 20 {
			info.State = PowerStateLowBattery
		} else {
			info.State = PowerStateBattery
		}
	} else {
		// Desktop without battery, assume AC
		info.State = PowerStateAC
	}

	info.BatteryPercentage = batteryCapacity
	info.IsCharging = isCharging

	return info, nil
}

// Subscribe registers a callback for power state changes
func (m *LinuxPowerMonitor) Subscribe(callback func(PowerInfo)) error {
	log := logging.WithComponent("power")
	m.callback = callback

	// TODO: Implement DBus subscription to UPower for real-time updates
	// For now, we'll rely on polling from the scheduler
	log.Info().Msg("Power monitor subscribed (polling mode)")

	return nil
}

// Unsubscribe removes the callback
func (m *LinuxPowerMonitor) Unsubscribe() error {
	m.callback = nil
	return nil
}

// Close cleans up resources
func (m *LinuxPowerMonitor) Close() error {
	close(m.stopChan)
	return nil
}
