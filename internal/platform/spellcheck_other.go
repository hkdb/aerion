//go:build !linux

package platform

// EnableSpellChecking is a no-op on non-Linux platforms, where the native
// WebView handles spell checking automatically.
func EnableSpellChecking() {}
