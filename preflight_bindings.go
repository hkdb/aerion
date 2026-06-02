//go:build bindings

package main

import "github.com/hkdb/aerion/app"

// runPreflight is a no-op under the `bindings` build tag. Wails' binding
// generator builds the binary with this tag and executes it once to emit
// TypeScript bindings; we must not run real startup (DB open, migrations,
// keyring access) during that pass.
func runPreflight(_ *app.App) {}
