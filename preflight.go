//go:build !bindings

package main

import (
	"os"

	"github.com/hkdb/aerion/app"
	"github.com/hkdb/aerion/internal/platform"
)

// runPreflight performs pre-Wails startup checks (paths, DB open, migrations,
// credential store). On failure it surfaces a native error dialog and exits
// before wails.Run is called — otherwise the user would see a half-rendered
// app window briefly flash before the dialog appears.
//
// Compiled out under the `bindings` build tag so it does NOT run during
// Wails' binding-generation pass (`wails generate module` builds the binary
// with `-tags bindings` and executes it once to emit TS bindings; that pass
// must not touch the user's real DB).
func runPreflight(application *app.App) {
	err := application.Preflight()
	if err == nil {
		return
	}
	info := app.StartupDialogInfoFor(err)
	if info.ActionURL != "" {
		platform.ShowDialogWithLink(platform.DialogIconError, info.Title, info.Text, info.ActionLabel, info.ActionURL)
		os.Exit(1)
	}
	platform.ShowDialog(platform.DialogIconError, info.Title, info.Text)
	os.Exit(1)
}
