//go:build windows

package platform

import "syscall"

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procAllocConsole = kernel32.NewProc("AllocConsole")
)

// AttachConsole allocates a console window for log output.
// On Windows, GUI applications have no console by default (built with -H windowsgui).
// Call this before logging initialization so os.Stderr output is visible.
func AttachConsole() {
	procAllocConsole.Call()
}
