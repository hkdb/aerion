//go:build linux

package notification

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

const (
	linuxDesktopEntry = "io.github.hkdb.Aerion"

	// Portal backend (preferred when .desktop file is installed)
	dbusPortalDest      = "org.freedesktop.portal.Desktop"
	dbusPortalPath      = "/org/freedesktop/portal/desktop"
	dbusPortalInterface = "org.freedesktop.portal.Notification"

	// Direct D-Bus backend (fallback for non-.desktop launches)
	dbusNotifyDest      = "org.freedesktop.Notifications"
	dbusNotifyPath      = "/org/freedesktop/Notifications"
	dbusNotifyInterface = "org.freedesktop.Notifications"
)

type notificationBackend int

const (
	backendPortal notificationBackend = iota
	backendDirect
)

// portalIcon is the D-Bus (sv) representation of a serialized GIcon expected
// by the notification portal. The value for a themed icon is an array of icon
// names, wrapped in a variant.
type portalIcon struct {
	Kind  string
	Value dbus.Variant
}

type linuxNotifier struct {
	appName       string
	conn          *dbus.Conn
	clickHandler  ClickHandler
	notifications map[string]NotificationData // Portal uses string IDs
	notifyIDs     map[uint32]NotificationData // Direct uses uint32 IDs
	mu            sync.RWMutex
	log           zerolog.Logger
	cancel        context.CancelFunc
	idCounter     uint64

	// Backend selection
	backend       notificationBackend
	backendTested bool
}

func newPlatformNotifier(appName string, useDirectDBus bool) Notifier {
	backend := backendPortal // Try portal first by default
	if useDirectDBus {
		backend = backendDirect // Force direct D-Bus if flag is set
	}

	return &linuxNotifier{
		appName:       appName,
		notifications: make(map[string]NotificationData),
		notifyIDs:     make(map[uint32]NotificationData),
		log:           logging.WithComponent("notification"),
		backend:       backend,
	}
}

func (n *linuxNotifier) Start(ctx context.Context) error {
	var err error
	n.conn, err = dbus.ConnectSessionBus()
	if err != nil {
		return err
	}

	// If direct D-Bus was explicitly requested, use it
	if n.backend == backendDirect {
		n.log.Info().Msg("Using direct D-Bus notifications (--dbus-notify flag)")
		return n.tryDirectBackend(ctx)
	}

	// Try portal first
	if err := n.tryPortalBackend(ctx); err != nil {
		n.log.Debug().Err(err).Msg("Portal backend not available, falling back to direct D-Bus")
		return n.tryDirectBackend(ctx)
	}

	return nil
}

func (n *linuxNotifier) tryPortalBackend(ctx context.Context) error {
	// Check if portal is available
	obj := n.conn.Object(dbusPortalDest, dbusPortalPath)
	call := obj.Call("org.freedesktop.DBus.Peer.Ping", 0)
	if call.Err != nil {
		return fmt.Errorf("portal not available: %w", call.Err)
	}

	// Subscribe to portal signals
	if err := n.conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbusPortalPath),
		dbus.WithMatchInterface(dbusPortalInterface),
	); err != nil {
		return err
	}

	// Create cancellable context for signal handling
	signalCtx, cancel := context.WithCancel(ctx)
	n.cancel = cancel

	// Start listening for signals
	signals := make(chan *dbus.Signal, 10)
	n.conn.Signal(signals)

	go n.handleSignals(signalCtx, signals)

	n.backend = backendPortal
	n.backendTested = true
	n.log.Info().Msg("Linux notification listener started (using portal)")
	return nil
}

func (n *linuxNotifier) tryDirectBackend(ctx context.Context) error {
	// Subscribe to notification signals
	if err := n.conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbusNotifyPath),
		dbus.WithMatchInterface(dbusNotifyInterface),
	); err != nil {
		return err
	}

	// Create cancellable context for signal handling
	signalCtx, cancel := context.WithCancel(ctx)
	n.cancel = cancel

	// Start listening for signals
	signals := make(chan *dbus.Signal, 10)
	n.conn.Signal(signals)

	go n.handleSignals(signalCtx, signals)

	n.backend = backendDirect
	n.backendTested = true
	n.log.Info().Msg("Linux notification listener started (using direct D-Bus)")
	return nil
}

func (n *linuxNotifier) Stop() {
	if n.cancel != nil {
		n.cancel()
	}
	if n.conn != nil {
		n.conn.Close()
	}
	n.log.Info().Msg("Linux notification listener stopped")
}

// generateNotificationID creates a unique notification ID for portal backend
func (n *linuxNotifier) generateNotificationID() string {
	n.mu.Lock()
	n.idCounter++
	id := fmt.Sprintf("aerion-notification-%d-%d", time.Now().Unix(), n.idCounter)
	n.mu.Unlock()
	return id
}

func (n *linuxNotifier) Show(notif Notification) (uint32, error) {
	if n.conn == nil {
		// Fallback: not started, try to connect
		var err error
		n.conn, err = dbus.ConnectSessionBus()
		if err != nil {
			return 0, err
		}
	}

	// If we haven't tested which backend works, try portal first
	if !n.backendTested {
		id, err := n.showPortal(notif)
		if err != nil {
			n.log.Debug().Err(err).Msg("Portal notification failed, falling back to direct D-Bus")
			n.backend = backendDirect
			n.backendTested = true
			return n.showDirect(notif)
		}
		n.backendTested = true
		return id, nil
	}

	switch n.backend {
	case backendPortal:
		id, err := n.showPortal(notif)
		if err != nil {
			// Portal failed, try direct as fallback
			n.log.Debug().Err(err).Msg("Portal notification failed, falling back to direct D-Bus")
			n.backend = backendDirect
			return n.showDirect(notif)
		}
		return id, nil
	case backendDirect:
		return n.showDirect(notif)
	default:
		return n.showDirect(notif)
	}
}

func (n *linuxNotifier) showPortal(notif Notification) (uint32, error) {
	obj := n.conn.Object(dbusPortalDest, dbusPortalPath)

	// Generate unique string ID for this notification
	id := n.generateNotificationID()

	notification := portalNotificationPayload(notif)

	// Call AddNotification method with ID and notification vardict
	call := obj.Call(
		dbusPortalInterface+".AddNotification",
		0,
		id,
		notification,
	)

	if call.Err != nil {
		return 0, call.Err
	}

	// Store notification data for click handling
	n.mu.Lock()
	n.notifications[id] = notif.Data
	n.mu.Unlock()

	n.log.Debug().Str("id", id).Str("title", notif.Title).Msg("Notification shown via portal")

	// Return a hash of the ID as uint32 for API compatibility
	var numericID uint32
	for _, c := range id {
		numericID = numericID*31 + uint32(c)
	}
	return numericID, nil
}

func portalNotificationPayload(notif Notification) map[string]dbus.Variant {
	icon := notif.Icon
	if icon == "" {
		icon = linuxDesktopEntry
	}

	return map[string]dbus.Variant{
		"title":    dbus.MakeVariant(notif.Title),
		"body":     dbus.MakeVariant(notif.Body),
		"priority": dbus.MakeVariant("normal"),
		"icon": dbus.MakeVariant(portalIcon{
			Kind:  "themed",
			Value: dbus.MakeVariant([]string{icon}),
		}),
		"default-action": dbus.MakeVariant("open"),
	}
}

func (n *linuxNotifier) showDirect(notif Notification) (uint32, error) {
	obj := n.conn.Object(dbusNotifyDest, dbusNotifyPath)

	// Actions: "default" is the click action
	actions := []string{"default", "Open"}

	hints := directNotificationHints()

	// Icon - use mail icon
	icon := notif.Icon
	if icon == "" {
		icon = "mail-unread"
	}

	// Call Notify method
	call := obj.Call(
		dbusNotifyInterface+".Notify",
		0,
		n.appName,
		uint32(0),
		icon,
		notif.Title,
		notif.Body,
		actions,
		hints,
		int32(-1),
	)

	if call.Err != nil {
		return 0, call.Err
	}

	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, err
	}

	// Store notification data for click handling
	n.mu.Lock()
	n.notifyIDs[id] = notif.Data
	n.mu.Unlock()

	n.log.Debug().Uint32("id", id).Str("title", notif.Title).Msg("Notification shown via direct D-Bus")
	return id, nil
}

func directNotificationHints() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(byte(1)), // Normal urgency
		"category":      dbus.MakeVariant("email.arrived"),
		"desktop-entry": dbus.MakeVariant(linuxDesktopEntry),
	}
}

func (n *linuxNotifier) SetClickHandler(handler ClickHandler) {
	n.mu.Lock()
	n.clickHandler = handler
	n.mu.Unlock()
}

func (n *linuxNotifier) handleSignals(ctx context.Context, signals chan *dbus.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case signal := <-signals:
			if signal == nil {
				continue
			}
			n.handleSignal(signal)
		}
	}
}

func (n *linuxNotifier) handleSignal(signal *dbus.Signal) {
	switch signal.Name {
	case dbusPortalInterface + ".ActionInvoked":
		// Portal ActionInvoked(id string, action string, parameter variant)
		if len(signal.Body) >= 2 {
			id, ok1 := signal.Body[0].(string)
			action, ok2 := signal.Body[1].(string)
			if ok1 && ok2 {
				n.handlePortalAction(id, action)
			}
		}

	case dbusNotifyInterface + ".ActionInvoked":
		// Direct D-Bus ActionInvoked(id uint32, action_key string)
		if len(signal.Body) >= 2 {
			id, ok1 := signal.Body[0].(uint32)
			action, ok2 := signal.Body[1].(string)
			if ok1 && ok2 {
				n.handleDirectAction(id, action)
			}
		}

	case dbusNotifyInterface + ".NotificationClosed":
		// Direct D-Bus NotificationClosed(id uint32, reason uint32)
		if len(signal.Body) >= 1 {
			if id, ok := signal.Body[0].(uint32); ok {
				n.mu.Lock()
				delete(n.notifyIDs, id)
				n.mu.Unlock()
			}
		}
	}
}

func (n *linuxNotifier) handlePortalAction(id string, action string) {
	n.log.Debug().Str("id", id).Str("action", action).Msg("Portal notification action invoked")

	// Get notification data
	n.mu.RLock()
	data, exists := n.notifications[id]
	handler := n.clickHandler
	n.mu.RUnlock()

	if !exists {
		n.log.Debug().Str("id", id).Msg("Notification data not found")
		return
	}

	// The portal sends "open" when the notification body is clicked. Accept
	// "default" as well for notifications created by older Aerion versions
	// that may still be present in the notification history.
	if (action == "open" || action == "default") && handler != nil {
		n.log.Info().
			Str("accountId", data.AccountID).
			Str("folderId", data.FolderID).
			Str("threadId", data.ThreadID).
			Msg("Notification clicked, invoking handler")
		handler(data)
	}

	// Clean up
	n.mu.Lock()
	delete(n.notifications, id)
	n.mu.Unlock()

	// Remove the notification from the portal
	if n.conn != nil {
		obj := n.conn.Object(dbusPortalDest, dbusPortalPath)
		obj.Call(dbusPortalInterface+".RemoveNotification", 0, id)
	}
}

func (n *linuxNotifier) handleDirectAction(id uint32, action string) {
	n.log.Debug().Uint32("id", id).Str("action", action).Msg("Direct D-Bus notification action invoked")

	// Get notification data
	n.mu.RLock()
	data, exists := n.notifyIDs[id]
	handler := n.clickHandler
	n.mu.RUnlock()

	if !exists {
		n.log.Debug().Uint32("id", id).Msg("Notification data not found")
		return
	}

	// Handle "default" action (click on notification body)
	if action == "default" && handler != nil {
		n.log.Info().
			Str("accountId", data.AccountID).
			Str("folderId", data.FolderID).
			Str("threadId", data.ThreadID).
			Msg("Notification clicked, invoking handler")
		handler(data)
	}

	// Clean up
	n.mu.Lock()
	delete(n.notifyIDs, id)
	n.mu.Unlock()
}
