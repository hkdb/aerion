//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// showDialog uses osascript to render a native AppKit dialog. AppleScript's
// `display dialog` blocks the calling osascript process until the user
// dismisses it; we drive blocking/non-blocking via cmd.Run vs cmd.Start.
func showDialog(icon DialogIcon, title, text string, block bool) {
	var iconName string
	switch icon {
	case DialogIconError:
		iconName = "stop"
	case DialogIconWarning:
		iconName = "caution"
	case DialogIconInfo:
		iconName = "note"
	default:
		iconName = "note"
	}

	// %q produces Go-syntax quoted strings, which are also valid AppleScript
	// quoted strings (both honor \" and \\ inside double quotes).
	script := fmt.Sprintf(
		`display dialog %q with title %q buttons {"OK"} default button "OK" with icon %s`,
		text, title, iconName,
	)
	cmd := exec.Command("osascript", "-e", script)

	if block {
		_ = cmd.Run()
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", title, text)
		return
	}
	go func() { _ = cmd.Wait() }()
}

// showDialogWithLink renders a two-button AppleScript "display dialog"
// (action button + Quit). osascript prints `button returned:<label>` to
// stdout on success; we parse that to decide whether to open the URL.
func showDialogWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) {
	var iconName string
	switch icon {
	case DialogIconError:
		iconName = "stop"
	case DialogIconWarning:
		iconName = "caution"
	case DialogIconInfo:
		iconName = "note"
	default:
		iconName = "note"
	}

	script := fmt.Sprintf(
		`display dialog %q with title %q buttons {%q, "Quit"} default button "Quit" cancel button "Quit" with icon %s`,
		text, title, actionLabel, iconName,
	)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		// User pressed Cancel / closed the dialog. osascript exits non-zero
		// for the cancel button when one is declared; nothing to do.
		return
	}
	if strings.Contains(string(out), "button returned:"+actionLabel) {
		_ = exec.Command("open", actionURL).Start()
	}
}
