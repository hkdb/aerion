//go:build windows

package platform

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// downloadsDir asks the shell for the Downloads Known Folder, so a relocated
// Downloads (Properties → Location → Move…) resolves to its real path instead
// of the no-longer-existing %USERPROFILE%\Downloads (#474).
func downloadsDir() string {
	path, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil || path == "" {
		home := userHomeDir()
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Downloads")
	}
	return path
}
