package ui

import (
	"sync"
	"testing"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

func TestRegistry_RegisterRailTab_OrdersByOrder(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterRailTab(coreapi.RailTabRequest{
		ExtensionID: "calendar", Label: "Calendar", Component: "CalendarPane", Order: 20,
	}); err != nil {
		t.Fatalf("register calendar: %v", err)
	}
	if _, err := r.RegisterRailTab(coreapi.RailTabRequest{
		ExtensionID: "contacts", Label: "Contacts", Component: "ContactsPane", Order: 10,
	}); err != nil {
		t.Fatalf("register contacts: %v", err)
	}

	tabs := r.ListRailTabs()
	if len(tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(tabs))
	}
	if tabs[0].ExtensionID != "contacts" || tabs[1].ExtensionID != "calendar" {
		t.Fatalf("expected contacts, calendar — got %s, %s", tabs[0].ExtensionID, tabs[1].ExtensionID)
	}
}

func TestRegistry_RegisterRailTab_Validation(t *testing.T) {
	r := NewRegistry()
	cases := []struct {
		name string
		req  coreapi.RailTabRequest
	}{
		{"missing ext id", coreapi.RailTabRequest{Label: "L", Component: "C"}},
		{"missing label", coreapi.RailTabRequest{ExtensionID: "x", Component: "C"}},
		{"missing component", coreapi.RailTabRequest{ExtensionID: "x", Label: "L"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := r.RegisterRailTab(c.req); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestRegistry_Unregister_RemovesEntry(t *testing.T) {
	r := NewRegistry()
	unreg, err := r.RegisterRailTab(coreapi.RailTabRequest{
		ExtensionID: "contacts", Label: "Contacts", Component: "ContactsPane",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := r.ListRailTabs(); len(got) != 1 {
		t.Fatalf("expected 1 before unregister, got %d", len(got))
	}
	unreg()
	if got := r.ListRailTabs(); len(got) != 0 {
		t.Fatalf("expected 0 after unregister, got %d", len(got))
	}
}

func TestRegistry_AccountSetupHook_ProviderFilter(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
		ExtensionID: "contacts", Providers: []string{"google", "microsoft"},
		ButtonLabel: "Also set up contacts", Component: "AccountContactsHookPanel",
	}); err != nil {
		t.Fatalf("register contacts hook: %v", err)
	}
	if _, err := r.RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
		ExtensionID: "calendar", Providers: []string{"google"},
		ButtonLabel: "Also set up calendar", Component: "AccountCalendarHookPanel",
	}); err != nil {
		t.Fatalf("register calendar hook: %v", err)
	}

	google := r.ListAccountSetupHooksForProvider("google")
	if len(google) != 2 {
		t.Fatalf("expected 2 google hooks, got %d", len(google))
	}

	microsoft := r.ListAccountSetupHooksForProvider("microsoft")
	if len(microsoft) != 1 {
		t.Fatalf("expected 1 microsoft hook, got %d", len(microsoft))
	}
	if microsoft[0].ExtensionID != "contacts" {
		t.Fatalf("expected contacts hook for microsoft, got %s", microsoft[0].ExtensionID)
	}

	imap := r.ListAccountSetupHooksForProvider("imap")
	if len(imap) != 0 {
		t.Fatalf("expected 0 imap hooks, got %d", len(imap))
	}
	// Must return non-nil even when empty
	if imap == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
}

func TestRegistry_AccountSetupHook_Validation(t *testing.T) {
	r := NewRegistry()
	cases := []struct {
		name string
		req  coreapi.AccountSetupHookRequest
	}{
		{"missing ext id", coreapi.AccountSetupHookRequest{
			Providers: []string{"google"}, ButtonLabel: "L", Component: "C",
		}},
		{"missing providers", coreapi.AccountSetupHookRequest{
			ExtensionID: "x", ButtonLabel: "L", Component: "C",
		}},
		{"missing label", coreapi.AccountSetupHookRequest{
			ExtensionID: "x", Providers: []string{"google"}, Component: "C",
		}},
		{"missing component", coreapi.AccountSetupHookRequest{
			ExtensionID: "x", Providers: []string{"google"}, ButtonLabel: "L",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := r.RegisterAccountSetupHook(c.req); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestRegistry_ContextMenuItems_TargetFilter(t *testing.T) {
	r := NewRegistry()
	for _, target := range []coreapi.ContextMenuTarget{
		coreapi.ContextMenuMessageRow,
		coreapi.ContextMenuMessageRow,
		coreapi.ContextMenuContactRow,
	} {
		if _, err := r.RegisterContextMenuItem(coreapi.ContextMenuRequest{
			ExtensionID: "x", Target: target, Label: "L", HandlerID: "h",
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	msg := r.ListContextMenuItems(coreapi.ContextMenuMessageRow)
	if len(msg) != 2 {
		t.Fatalf("expected 2 message-row items, got %d", len(msg))
	}
	contact := r.ListContextMenuItems(coreapi.ContextMenuContactRow)
	if len(contact) != 1 {
		t.Fatalf("expected 1 contact-row item, got %d", len(contact))
	}
	all := r.ListContextMenuItems("")
	if len(all) != 3 {
		t.Fatalf("expected 3 total, got %d", len(all))
	}
}

func TestRegistry_ConcurrentRegisterUnregister(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	const n = 50
	unregs := make([]coreapi.Unregister, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			unreg, err := r.RegisterRailTab(coreapi.RailTabRequest{
				ExtensionID: "x", Label: "L", Component: "C", Order: i,
			})
			if err != nil {
				t.Errorf("register %d: %v", i, err)
				return
			}
			unregs[i] = unreg
		}(i)
	}
	wg.Wait()
	if got := len(r.ListRailTabs()); got != n {
		t.Fatalf("expected %d, got %d", n, got)
	}

	for _, u := range unregs {
		wg.Add(1)
		go func(u coreapi.Unregister) {
			defer wg.Done()
			u()
		}(u)
	}
	wg.Wait()
	if got := len(r.ListRailTabs()); got != 0 {
		t.Fatalf("expected 0 after unregister, got %d", got)
	}
}
