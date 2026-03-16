//go:build linux

package platform

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// MonitorGBMErrors intercepts stderr to detect WebKitGTK DMABUF rendering
// failures (black window in Flatpak). Detected patterns:
//   - "Failed to create GBM buffer" (GBM allocation failure)
//   - "Protocol error) dispatching to Wayland display" (Wayland protocol error)
//   - "Failed to submit review to ODRS" (ODRS review submission error)
//
// If any pattern is detected, shows a zenity dialog with the permanent fix
// command. Only runs in Flatpak. All stderr output is still forwarded to
// the original stderr.
func MonitorGBMErrors() {
	if !IsFlatpak() {
		return
	}

	// Save original stderr fd
	origFd, err := unix.Dup(2)
	if err != nil {
		return
	}
	origStderr := os.NewFile(uintptr(origFd), "orig-stderr")

	// Create pipe to intercept stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		origStderr.Close()
		return
	}

	// Redirect fd 2 to the pipe write end
	if err := unix.Dup2(int(pw.Fd()), 2); err != nil {
		pr.Close()
		pw.Close()
		origStderr.Close()
		return
	}

	// Close extra pipe write fd (fd 2 now holds a dup) and update os.Stderr
	pw.Close()
	os.Stderr = os.NewFile(2, "/dev/stderr")

	// Goroutine: tee pipe output to original stderr, scan for GBM error
	go func() {
		var once sync.Once
		scanner := bufio.NewScanner(io.TeeReader(pr, origStderr))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Failed to create GBM buffer") ||
			strings.Contains(line, "Protocol error) dispatching to Wayland display") ||
			strings.Contains(line, "Failed to submit review to ODRS") {
				once.Do(func() {
					showGBMFixDialog()
				})
			}
		}
	}()
}

// showGBMFixDialog displays a zenity warning dialog with the GBM fix command.
func showGBMFixDialog() {
	cmd := exec.Command("zenity", "--warning",
		"--title=Aerion - Display Issue Detected",
		"--text="+
			"A display rendering error was detected that may cause a blank window or crash.\n\n"+
			"To fix this permanently, close Aerion and run:\n\n"+
			"flatpak override --user --env=WEBKIT_DISABLE_DMABUF_RENDERER=1 io.github.hkdb.Aerion\n\n"+
			"Then restart Aerion.",
		"--width=500",
	)
	cmd.Start()
}
