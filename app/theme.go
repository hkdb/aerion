package app

import (
	"context"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/platform"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// initThemeMonitor initializes the system theme monitor for portal-based theme detection.
// On Linux, this uses the XDG Settings Portal. On other platforms, it's a no-op
// and the frontend falls back to matchMedia.
func (a *App) initThemeMonitor(ctx context.Context) {
	log := logging.WithComponent("app.theme")

	a.themeMonitor = platform.NewThemeMonitor()

	if err := a.themeMonitor.Start(ctx); err != nil {
		log.Debug().Err(err).Msg("System theme monitor not available, frontend will use matchMedia fallback")
		a.themeMonitor = nil
		return
	}

	// Emit initial theme value so the frontend can use it immediately
	initialTheme := a.themeMonitor.GetTheme()
	if initialTheme != platform.SystemThemeNoPreference {
		wailsRuntime.EventsEmit(ctx, "theme:system-preference", string(initialTheme))
	}

	go a.processThemeEvents(ctx)

	log.Info().Msg("System theme monitor initialized")
}

// processThemeEvents listens for system theme changes and emits events to the frontend
func (a *App) processThemeEvents(ctx context.Context) {
	defer recoverPanic("app.theme", "process theme events")
	if a.themeMonitor == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case theme, ok := <-a.themeMonitor.Events():
			if !ok {
				return
			}
			wailsRuntime.EventsEmit(a.ctx, "theme:system-preference", string(theme))
		}
	}
}

// GetSystemTheme returns the current system theme preference detected via
// the XDG Settings Portal on Linux. Returns "light", "dark", or "" if not available.
func (a *App) GetSystemTheme() string {
	if a.themeMonitor == nil {
		return ""
	}
	return string(a.themeMonitor.GetTheme())
}
