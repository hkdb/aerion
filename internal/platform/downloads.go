package platform

import "os"

// DownloadsDir returns the user's Downloads folder as the OS reports it:
// on Windows the Known Folder (which the user may have relocated), elsewhere
// ~/Downloads. Empty when it cannot be determined. Existence is not checked.
func DownloadsDir() string {
	return downloadsDir()
}

// DefaultSaveDir returns the folder a save dialog should open in: Downloads
// if it exists, else the home directory if it exists, else "" (the dialog
// then falls back to the OS default). Wails refuses to open a dialog whose
// default directory is missing (#474), so this never returns a path that
// does not exist.
func DefaultSaveDir() string {
	for _, dir := range []string{downloadsDir(), userHomeDir()} {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
