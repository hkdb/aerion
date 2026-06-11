package platform

// DialogIcon controls the icon and severity styling of native OS dialogs
// shown via ShowDialog / ShowDialogAsync.
type DialogIcon int

const (
	// DialogIconError — red X / stop icon. Use for unrecoverable failures.
	DialogIconError DialogIcon = iota
	// DialogIconWarning — yellow triangle / caution icon. Use for recoverable
	// issues the user should know about.
	DialogIconWarning
	// DialogIconInfo — neutral info icon. Use for purely informational messages.
	DialogIconInfo
)

// ShowDialog displays a native OS dialog with a single OK button and blocks
// until the user dismisses it. Use for pre-Wails startup failures where there
// is no Svelte UI to render the error in, and the caller intends to exit
// immediately after the dialog closes.
//
// Linux: zenity → kdialog → xmessage → stderr fallback chain. macOS: osascript.
// Windows: user32!MessageBoxW. Display failures degrade silently to a stderr
// write so the underlying message is still surfaced somewhere.
//
// Does NOT call os.Exit — caller's responsibility.
func ShowDialog(icon DialogIcon, title, text string) {
	showDialog(icon, title, text, true)
}

// ShowDialogAsync is the non-blocking variant of ShowDialog. Use when the
// dialog is informational and the program should keep running while the user
// reads (e.g. background warnings detected mid-session).
func ShowDialogAsync(icon DialogIcon, title, text string) {
	showDialog(icon, title, text, false)
}

// ShowDialogWithLink displays a blocking native dialog with two buttons: a
// default close button ("Quit") and an action button labeled actionLabel
// that opens actionURL via the OS's default URL handler when clicked.
// Returns after the user dismisses the dialog. If the user clicked the
// action button, the URL is opened before this function returns.
//
// Use this for startup failures that have an associated docs URL — e.g.,
// the schema-too-new error pointing at docs/SQL_ROLLBACK.md on GitHub.
//
// Backend behavior:
//   - Linux (zenity): two-button dialog via --extra-button; URLs inline
//     in text are also clickable via Pango markup.
//   - Linux (kdialog / xmessage): two-button dialog with custom labels.
//   - macOS (osascript): two-button "display dialog".
//   - Windows: MB_YESNO with explanatory text appended.
//
// All backends degrade gracefully — if the chosen tool isn't available,
// the dialog falls back to a single-button display with actionURL
// included in the body as copyable plain text.
func ShowDialogWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) {
	showDialogWithLink(icon, title, text, actionLabel, actionURL)
}
