//go:build windows

package platform

import "golang.org/x/sys/windows"

// OpenPathWindows opens a local file/path with its default application via the
// Win32 ShellExecute API rather than `cmd /c start`. cmd.exe re-parses its
// command line and treats `&`/`|`/`^` as command separators, so opening an
// attachment whose filename contains those characters would inject commands
// (same class as issue #261, which fixed the URL path). ShellExecute receives
// the path as a single argument with no shell re-parsing.
func OpenPathWindows(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
