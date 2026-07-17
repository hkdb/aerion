//go:build linux

package notification

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog"
)

func TestPortalNotificationPayloadIncludesIconAndDefaultAction(t *testing.T) {
	payload := portalNotificationPayload(Notification{
		Title: "New email",
		Body:  "Test",
		Icon:  "mail-unread",
	})

	icon, ok := payload["icon"].Value().(portalIcon)
	if !ok {
		t.Fatalf("icon has type %T, want portalIcon", payload["icon"].Value())
	}
	if icon.Kind != "themed" {
		t.Errorf("icon kind = %q, want themed", icon.Kind)
	}
	if got := dbus.SignatureOf(icon).String(); got != "(sv)" {
		t.Errorf("icon signature = %q, want (sv)", got)
	}
	if got := icon.Value.Value(); !equalStrings(got, []string{"mail-unread"}) {
		t.Errorf("icon names = %#v, want [mail-unread]", got)
	}

	if got := payload["default-action"].Value(); got != "open" {
		t.Errorf("default action = %#v, want open", got)
	}
	if _, exists := payload["buttons"]; exists {
		t.Error("portal payload should rely on the notification body action, not render a separate button")
	}
}

func TestPortalNotificationPayloadUsesApplicationIconByDefault(t *testing.T) {
	payload := portalNotificationPayload(Notification{})
	icon := payload["icon"].Value().(portalIcon)

	if got := icon.Value.Value(); !equalStrings(got, []string{linuxDesktopEntry}) {
		t.Errorf("icon names = %#v, want [%s]", got, linuxDesktopEntry)
	}
}

func TestDirectNotificationHintsIdentifyDesktopEntry(t *testing.T) {
	hints := directNotificationHints()

	if got := hints["desktop-entry"].Value(); got != linuxDesktopEntry {
		t.Errorf("desktop entry = %#v, want %s", got, linuxDesktopEntry)
	}
}

func TestHandlePortalActionRoutesOpenAndLegacyDefault(t *testing.T) {
	for _, action := range []string{"open", "default"} {
		t.Run(action, func(t *testing.T) {
			want := NotificationData{ThreadID: "thread-1"}
			var got NotificationData
			notifier := &linuxNotifier{
				notifications: map[string]NotificationData{"notification-1": want},
				clickHandler:  func(data NotificationData) { got = data },
				log:           zerolog.Nop(),
			}

			notifier.handlePortalAction("notification-1", action)

			if got != want {
				t.Errorf("click data = %#v, want %#v", got, want)
			}
			if _, exists := notifier.notifications["notification-1"]; exists {
				t.Error("handled notification was not removed")
			}
		})
	}
}

func equalStrings(value any, want []string) bool {
	got, ok := value.([]string)
	if !ok || len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
