package app

import (
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/hkdb/aerion/internal/appstate"
	"github.com/hkdb/aerion/internal/logging"
)

// RefreshWindowConstraints removes window max size constraints. This works
// around a Wails v2 Linux limitation where GTK geometry hints are set once at
// startup using the initial monitor's dimensions, causing the window to be
// stuck at that size when moving to a larger monitor.
// We use a large value instead of 0,0 because GTK may interpret zero as
// "use current hints" rather than "remove constraints".
func (a *App) RefreshWindowConstraints() {
	wailsRuntime.WindowSetMaxSize(a.ctx, 100000, 100000)
}

// RefreshWindowConstraints removes window max size constraints for the
// composer window. See App.RefreshWindowConstraints for details.
func (c *ComposerApp) RefreshWindowConstraints() {
	wailsRuntime.WindowSetMaxSize(c.ctx, 100000, 100000)
}

// saveWindowGeometry persists the main window's current geometry so the next
// launch can restore it. Called from every user-initiated close path while
// the window is still live (BeforeClose both branches, CloseWindow, QuitApp,
// InitiateShutdown) — NOT from Shutdown, where the window is mid-teardown.
// When maximized, only the flag is updated so the stored NORMAL size isn't
// clobbered by the maximized dimensions.
func (a *App) saveWindowGeometry() {
	if a.appStateStore == nil {
		return
	}
	log := logging.WithComponent("app")
	if wailsRuntime.WindowIsMaximised(a.ctx) {
		geo, _ := a.appStateStore.GetWindowGeometry()
		geo.Maximized = true
		if err := a.appStateStore.SetWindowGeometry(geo); err != nil {
			log.Warn().Err(err).Msg("Failed to save window geometry")
		}
		return
	}
	w, h := wailsRuntime.WindowGetSize(a.ctx)
	x, y := wailsRuntime.WindowGetPosition(a.ctx)
	if w <= 0 || h <= 0 {
		return
	}
	err := a.appStateStore.SetWindowGeometry(appstate.WindowGeometry{
		Width: w, Height: h, X: x, Y: y, Maximized: false,
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to save window geometry")
	}
}

// restoreWindowPosition applies the persisted window position on startup
// (size + maximized state are init-time wails.Run options; position can only
// be set post-creation). Skipped when maximized, never saved, or the stored
// position falls outside every connected screen — moving the window onto an
// unplugged monitor would strand it off-screen. Positioning is a WM decision
// on Wayland, where this is silently a no-op.
func (a *App) restoreWindowPosition() {
	geo, ok := a.appStateStore.GetWindowGeometry()
	if !ok || geo.Maximized {
		return
	}
	// Wails v2 exposes screen sizes but not their origins, so a precise
	// visibility check isn't possible. Coarse guard: the position must fall
	// within the virtual desktop's maximum possible extent (sum of screen
	// dimensions in either axis) — rejects stale coordinates from a
	// since-unplugged monitor layout without false-rejecting valid ones.
	if screens, err := wailsRuntime.ScreenGetAll(a.ctx); err == nil && len(screens) > 0 {
		totalW, totalH := 0, 0
		for _, s := range screens {
			totalW += s.Width
			totalH += s.Height
		}
		if geo.X < -totalW || geo.X >= totalW || geo.Y < -totalH || geo.Y >= totalH {
			return
		}
	}
	wailsRuntime.WindowSetPosition(a.ctx, geo.X, geo.Y)
}
