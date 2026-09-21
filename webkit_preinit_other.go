//go:build !linux || !webkit2_41

package main

// preinitWebKit is a no-op outside Linux webkit2_41 builds: the WebKitGTK
// 2.54.0 main-thread assertion (#431) only exists in the GTK port, and every
// shipping Linux build sets the webkit2_41 tag (Makefile BUILD_TAGS, flatpak
// manifest). The untagged variant exists so plain `go test`/`go vet`/lint
// runs don't require webkit2gtk-4.1 headers.
func preinitWebKit() {}
