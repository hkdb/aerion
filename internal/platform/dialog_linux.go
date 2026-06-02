//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// urlRegex matches plain http(s):// URLs so they can be wrapped in Pango
// anchor markup before being passed to zenity. The regex is intentionally
// permissive on the trailing characters — Pango trims common trailing
// punctuation when rendering. Callers should not pass URLs with embedded
// whitespace.
var urlRegex = regexp.MustCompile(`https?://[^\s<>"']+`)

// linkifyForPango wraps bare URLs in Pango <a href="..."> anchor markup so
// zenity renders them as clickable links. Assumes the input is plain text
// (no existing markup) and contains no Pango-special characters outside
// URLs — true for all Aerion-internal callers as of writing. If we ever
// need to surface user-supplied strings here, this helper would need a
// proper Pango escape pass first.
func linkifyForPango(text string) string {
	return urlRegex.ReplaceAllStringFunc(text, func(url string) string {
		return fmt.Sprintf(`<a href="%s">%s</a>`, url, url)
	})
}

// showDialog displays a native dialog by walking the zenity → kdialog →
// xmessage → stderr fallback chain. Each step is tried in order; the first
// tool found on PATH is used.
func showDialog(icon DialogIcon, title, text string, block bool) {
	if tryZenity(icon, title, text, block) {
		return
	}
	if tryKdialog(icon, title, text, block) {
		return
	}
	if tryXmessage(title, text, block) {
		return
	}
	fmt.Fprintf(os.Stderr, "[%s] %s\n", title, text)
}

func tryZenity(icon DialogIcon, title, text string, block bool) bool {
	if _, err := exec.LookPath("zenity"); err != nil {
		return false
	}
	var iconFlag string
	switch icon {
	case DialogIconError:
		iconFlag = "--error"
	case DialogIconWarning:
		iconFlag = "--warning"
	case DialogIconInfo:
		iconFlag = "--info"
	default:
		iconFlag = "--info"
	}
	cmd := exec.Command("zenity", iconFlag,
		"--title="+title,
		"--text="+linkifyForPango(text),
		"--width=500",
	)
	return runDialogCmd(cmd, block)
}

func tryKdialog(icon DialogIcon, title, text string, block bool) bool {
	if _, err := exec.LookPath("kdialog"); err != nil {
		return false
	}
	var iconFlag string
	switch icon {
	case DialogIconError:
		iconFlag = "--error"
	case DialogIconWarning:
		// kdialog uses --sorry for warning-style dialogs.
		iconFlag = "--sorry"
	case DialogIconInfo:
		iconFlag = "--msgbox"
	default:
		iconFlag = "--msgbox"
	}
	cmd := exec.Command("kdialog", iconFlag, text, "--title", title)
	return runDialogCmd(cmd, block)
}

// tryXmessage falls back to xmessage (no icon support, but almost always
// present on X11 systems). Wayland-only sessions without Xwayland will skip
// straight to stderr.
func tryXmessage(title, text string, block bool) bool {
	if _, err := exec.LookPath("xmessage"); err != nil {
		return false
	}
	cmd := exec.Command("xmessage",
		"-center",
		"-title", title,
		"-buttons", "OK:0",
		text,
	)
	return runDialogCmd(cmd, block)
}

func runDialogCmd(cmd *exec.Cmd, block bool) bool {
	if block {
		// Run blocks until the dialog is dismissed. Exit code is ignored —
		// any non-zero exit (user closed without OK, killed window, etc.) is
		// treated the same as OK; we just want acknowledgement.
		_ = cmd.Run()
		return true
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	// Reap the child in a goroutine so we don't leave a zombie if the parent
	// outlives the dialog.
	go func() { _ = cmd.Wait() }()
	return true
}

// showDialogWithLink walks the same zenity → kdialog → xmessage → stderr
// fallback chain as showDialog, but each backend is configured for two
// buttons (close + action). If the user clicks the action button, the URL
// is opened via xdg-open (after a portal attempt on Flatpak).
func showDialogWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) {
	if tryZenityWithLink(icon, title, text, actionLabel, actionURL) {
		return
	}
	if tryKdialogWithLink(icon, title, text, actionLabel, actionURL) {
		return
	}
	if tryXmessageWithLink(title, text, actionLabel, actionURL) {
		return
	}
	fmt.Fprintf(os.Stderr, "[%s] %s\n%s: %s\n", title, text, actionLabel, actionURL)
}

func tryZenityWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) bool {
	if _, err := exec.LookPath("zenity"); err != nil {
		return false
	}
	var iconFlag string
	switch icon {
	case DialogIconError:
		iconFlag = "--error"
	case DialogIconWarning:
		iconFlag = "--warning"
	case DialogIconInfo:
		iconFlag = "--info"
	default:
		iconFlag = "--info"
	}
	cmd := exec.Command("zenity", iconFlag,
		"--title="+title,
		"--text="+linkifyForPango(text),
		"--ok-label=Quit",
		"--extra-button="+actionLabel,
		"--width=500",
	)
	// Zenity behavior with --extra-button:
	//   - OK button clicked:           exit 0, no stdout
	//   - Extra button clicked:        exit 1, prints button label to stdout
	//   - Window closed / Esc pressed: exit 1, empty stdout
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) == actionLabel {
		openURLBestEffort(actionURL)
	}
	return true
}

func tryKdialogWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) bool {
	if _, err := exec.LookPath("kdialog"); err != nil {
		return false
	}
	// kdialog --warningyesno renders a warning/error icon with two custom
	// labels. We use --yes-label for the action and --no-label for Quit so
	// "Open Docs" is the affirmative choice.
	dialogFlag := "--warningyesno"
	switch icon {
	case DialogIconInfo:
		dialogFlag = "--yesno"
	}
	cmd := exec.Command("kdialog", dialogFlag, text,
		"--title", title,
		"--yes-label", actionLabel,
		"--no-label", "Quit",
	)
	err := cmd.Run()
	// kdialog exits 0 when yes-button is clicked, 1 otherwise.
	if err == nil {
		openURLBestEffort(actionURL)
	}
	return true
}

func tryXmessageWithLink(title, text, actionLabel, actionURL string) bool {
	if _, err := exec.LookPath("xmessage"); err != nil {
		return false
	}
	cmd := exec.Command("xmessage",
		"-center",
		"-title", title,
		"-buttons", actionLabel+":101,Quit:0",
		text+"\n\n"+actionURL,
	)
	if err := cmd.Run(); err != nil {
		// Non-zero exit = the user picked the non-default button. xmessage
		// reports the button's value (101 for action) via exit code.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 101 {
			openURLBestEffort(actionURL)
		}
	}
	return true
}

// openURLBestEffort opens the given URL via the OS default handler. Best
// effort: failures are silent (we've already shown the URL to the user as
// text in the dialog, so they can copy/paste it manually if needed).
//
// Prefers the XDG OpenURI portal so Flatpak builds reach the host's URL
// handler, falling back to xdg-open.
func openURLBestEffort(url string) {
	if err := PortalOpenURI(url); err == nil {
		return
	}
	_ = exec.Command("xdg-open", url).Start()
}
