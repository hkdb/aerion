//go:build !windows

package platform

import "path/filepath"

func downloadsDir() string {
	home := userHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}
