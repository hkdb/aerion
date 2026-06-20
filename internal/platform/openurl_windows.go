//go:build windows

package platform

import "golang.org/x/sys/windows"

// OpenURLWindows opens a URL in the default browser via the Win32 ShellExecute
// API rather than `cmd /c start`. cmd.exe treats `&` as a command separator, so
// launching a URL with multiple query parameters through cmd truncates it at the
// first `&` (issue #261). ShellExecute receives the full URL as a single
// argument with no shell reparsing.
func OpenURLWindows(url string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
