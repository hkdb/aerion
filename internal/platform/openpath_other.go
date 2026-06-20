//go:build !windows

package platform

import "errors"

// OpenPathWindows is the Windows-only ShellExecute-based file opener (see
// openpath_windows.go). This stub exists so cross-platform callers in app/ link
// on all OSes; it is never invoked off Windows.
func OpenPathWindows(string) error {
	return errors.New("platform: OpenPathWindows is windows-only")
}
