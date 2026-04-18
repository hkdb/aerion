//go:build windows

package platform

import (
	"os"
	"syscall"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procAllocConsole  = kernel32.NewProc("AllocConsole")
	procGetStdHandle  = kernel32.NewProc("GetStdHandle")
)

// AttachConsole attaches to the parent console or creates a new one for log output.
// On Windows, GUI applications have no console by default (built with -H windowsgui).
// Call this before logging initialization so os.Stderr output is visible.
func AttachConsole() {
	var attachParentProcess = ^uintptr(0)     // ATTACH_PARENT_PROCESS = (DWORD)-1
	var stdErrorHandle = ^uintptr(0) - 11     // STD_ERROR_HANDLE = -12

	// Try to attach to parent console (e.g., cmd.exe or PowerShell)
	r, _, _ := procAttachConsole.Call(attachParentProcess)
	if r == 0 {
		// No parent console — create a new one
		procAllocConsole.Call()
	}

	// Redirect stderr to the console so Go's log output is visible
	h, _, _ := procGetStdHandle.Call(stdErrorHandle)
	if h != 0 && h != ^uintptr(0) {
		os.Stderr = os.NewFile(h, "stderr")
	}
}
