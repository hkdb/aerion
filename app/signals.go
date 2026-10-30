package app

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/hkdb/aerion/internal/logging"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// watchTerminationSignals makes SIGTERM/SIGINT/SIGHUP quit the app even when
// "Run in Background" is on.
//
// Wails subscribes to SIGTERM/SIGINT itself and routes them through the same
// quit path as the window close button, which consults BeforeClose. In
// background mode BeforeClose hides the window and cancels the quit, so a
// system shutdown's SIGTERM was swallowed and systemd waited out its stop
// timeout before SIGKILLing the process (#460). Setting shuttingDown first
// makes BeforeClose let the quit through, whichever handler runs first.
func (a *App) watchTerminationSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		defer recoverPanic("app", "termination signal")
		for sig := range ch {
			if shuttingDown {
				continue
			}
			shuttingDown = true
			if !a.windowHidden {
				a.saveWindowGeometry()
			}
			log := logging.WithComponent("app")
			log.Info().Str("signal", sig.String()).Msg("Termination signal received, shutting down")
			wailsRuntime.Quit(a.ctx)
		}
	}()
}
