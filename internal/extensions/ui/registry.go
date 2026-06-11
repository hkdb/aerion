package ui

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Registry is the in-memory store of all extension UI registrations. Safe
// for concurrent Register/Unregister/List from multiple goroutines.
type Registry struct {
	mu                sync.RWMutex
	nextID            atomic.Uint64
	railTabs          map[uint64]coreapi.RailTabRequest
	settingsTabs      map[uint64]coreapi.SettingsTabRequest
	contextMenuItems  map[uint64]coreapi.ContextMenuRequest
	inboxViews        map[uint64]coreapi.InboxViewRequest
	accountSetupHooks map[uint64]coreapi.AccountSetupHookRequest
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		railTabs:          make(map[uint64]coreapi.RailTabRequest),
		settingsTabs:      make(map[uint64]coreapi.SettingsTabRequest),
		contextMenuItems:  make(map[uint64]coreapi.ContextMenuRequest),
		inboxViews:        make(map[uint64]coreapi.InboxViewRequest),
		accountSetupHooks: make(map[uint64]coreapi.AccountSetupHookRequest),
	}
}

// RegisterRailTab adds a rail tab. Returns an Unregister func that removes it.
func (r *Registry) RegisterRailTab(req coreapi.RailTabRequest) (coreapi.Unregister, error) {
	if req.ExtensionID == "" {
		return nil, fmt.Errorf("ui.RegisterRailTab: ExtensionID is required")
	}
	if req.Label == "" || req.Component == "" {
		return nil, fmt.Errorf("ui.RegisterRailTab: Label and Component are required")
	}
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.railTabs[id] = req
	r.mu.Unlock()
	return r.unregisterFunc(&r.railTabs, id), nil
}

// RegisterSettingsTab adds a settings tab. Accepted in Phase 2a but no
// consumer reads it until Phase 3+.
func (r *Registry) RegisterSettingsTab(req coreapi.SettingsTabRequest) (coreapi.Unregister, error) {
	if req.ExtensionID == "" {
		return nil, fmt.Errorf("ui.RegisterSettingsTab: ExtensionID is required")
	}
	if req.Label == "" || req.Component == "" {
		return nil, fmt.Errorf("ui.RegisterSettingsTab: Label and Component are required")
	}
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.settingsTabs[id] = req
	r.mu.Unlock()
	return r.unregisterFunc(&r.settingsTabs, id), nil
}

// RegisterContextMenuItem adds a context menu entry. Accepted in Phase 2a
// but no consumer reads it until Phase 3+.
func (r *Registry) RegisterContextMenuItem(req coreapi.ContextMenuRequest) (coreapi.Unregister, error) {
	if req.ExtensionID == "" || req.HandlerID == "" {
		return nil, fmt.Errorf("ui.RegisterContextMenuItem: ExtensionID and HandlerID are required")
	}
	if req.Label == "" {
		return nil, fmt.Errorf("ui.RegisterContextMenuItem: Label is required")
	}
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.contextMenuItems[id] = req
	r.mu.Unlock()
	return r.unregisterFunc(&r.contextMenuItems, id), nil
}

// RegisterInboxView adds an alternate inbox rendering. Accepted in Phase 2a
// but no consumer reads it until Phase 3+.
func (r *Registry) RegisterInboxView(req coreapi.InboxViewRequest) (coreapi.Unregister, error) {
	if req.ExtensionID == "" {
		return nil, fmt.Errorf("ui.RegisterInboxView: ExtensionID is required")
	}
	if req.Component == "" {
		return nil, fmt.Errorf("ui.RegisterInboxView: Component is required")
	}
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.inboxViews[id] = req
	r.mu.Unlock()
	return r.unregisterFunc(&r.inboxViews, id), nil
}

// RegisterAccountSetupHook adds a post-account-add panel. The hook is offered
// to the user when an account with a provider in req.Providers is created.
func (r *Registry) RegisterAccountSetupHook(req coreapi.AccountSetupHookRequest) (coreapi.Unregister, error) {
	if req.ExtensionID == "" || req.ButtonLabel == "" || req.Component == "" {
		return nil, fmt.Errorf("ui.RegisterAccountSetupHook: ExtensionID, ButtonLabel, and Component are required")
	}
	if len(req.Providers) == 0 {
		return nil, fmt.Errorf("ui.RegisterAccountSetupHook: at least one provider is required")
	}
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.accountSetupHooks[id] = req
	r.mu.Unlock()
	return r.unregisterFunc(&r.accountSetupHooks, id), nil
}

// ListRailTabs returns all registered rail tabs in Order ASC then registration
// order. The returned slice is a copy — callers may not mutate the registry
// state via it.
func (r *Registry) ListRailTabs() []coreapi.RailTabRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]coreapi.RailTabRequest, 0, len(r.railTabs))
	for _, t := range r.railTabs {
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Order < out[j].Order
	})
	return out
}

// ListSettingsTabs returns all registered settings tabs.
func (r *Registry) ListSettingsTabs() []coreapi.SettingsTabRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]coreapi.SettingsTabRequest, 0, len(r.settingsTabs))
	for _, t := range r.settingsTabs {
		out = append(out, t)
	}
	return out
}

// ListContextMenuItems returns all context-menu items registered for the
// given target (e.g., ContextMenuMessageRow). Pass an empty target to list all.
func (r *Registry) ListContextMenuItems(target coreapi.ContextMenuTarget) []coreapi.ContextMenuRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]coreapi.ContextMenuRequest, 0)
	for _, item := range r.contextMenuItems {
		if target != "" && item.Target != target {
			continue
		}
		out = append(out, item)
	}
	return out
}

// ListInboxViews returns all registered inbox views.
func (r *Registry) ListInboxViews() []coreapi.InboxViewRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]coreapi.InboxViewRequest, 0, len(r.inboxViews))
	for _, v := range r.inboxViews {
		out = append(out, v)
	}
	return out
}

// ListAccountSetupHooksForProvider returns all hooks whose Providers list
// contains the given provider string. Returns an empty slice when none match
// (never nil — frontends can iterate without a nil check).
func (r *Registry) ListAccountSetupHooksForProvider(provider string) []coreapi.AccountSetupHookRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]coreapi.AccountSetupHookRequest, 0)
	for _, hook := range r.accountSetupHooks {
		if !containsProvider(hook.Providers, provider) {
			continue
		}
		out = append(out, hook)
	}
	return out
}

func containsProvider(providers []string, p string) bool {
	for _, v := range providers {
		if v == p {
			return true
		}
	}
	return false
}

// unregisterFunc returns an Unregister func that deletes the entry with the
// given id from a typed registry map. mapPtr is a pointer to the typed map
// so the closure can dereference it without the generic-types gymnastics.
func (r *Registry) unregisterFunc(mapPtr interface{}, id uint64) coreapi.Unregister {
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch m := mapPtr.(type) {
		case *map[uint64]coreapi.RailTabRequest:
			delete(*m, id)
		case *map[uint64]coreapi.SettingsTabRequest:
			delete(*m, id)
		case *map[uint64]coreapi.ContextMenuRequest:
			delete(*m, id)
		case *map[uint64]coreapi.InboxViewRequest:
			delete(*m, id)
		case *map[uint64]coreapi.AccountSetupHookRequest:
			delete(*m, id)
		}
	}
}

// Registry implements the Register* portion of coreapi.UI. The full
// coreapi.UI surface (including OpenURL and other platform actions) is
// composed in app/coreimpl.go's uiCoreImpl wrapper, which embeds this
// Registry's registration methods alongside host-only actions.
