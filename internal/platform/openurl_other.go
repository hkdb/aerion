//go:build !windows

package platform

import "errors"

// OpenURLWindows is the Windows-only ShellExecute-based URL opener (see
// openurl_windows.go). This stub exists so cross-platform callers in app/ link
// on all OSes; it is never invoked off Windows.
func OpenURLWindows(string) error {
	return errors.New("platform: OpenURLWindows is windows-only")
}
