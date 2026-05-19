//go:build !linux

package platform

import "fmt"

// PortalSaveFile is not supported on non-Linux platforms.
func PortalSaveFile(title, suggestedName, directory string) (string, error) {
	return "", fmt.Errorf("portal file chooser not supported on this platform")
}

// PortalSaveFiles is not supported on non-Linux platforms.
func PortalSaveFiles(title string, filenames []string, directory string) ([]string, error) {
	return nil, fmt.Errorf("portal file chooser not supported on this platform")
}

// PortalOpenDirectory is not supported on non-Linux platforms.
func PortalOpenDirectory(filePath string) error {
	return fmt.Errorf("portal not supported on this platform")
}

// PortalOpenFile is not supported on non-Linux platforms.
func PortalOpenFile(filePath string) error {
	return fmt.Errorf("portal not supported on this platform")
}

// PortalOpenURI is not supported on non-Linux platforms.
func PortalOpenURI(uri string) error {
	return fmt.Errorf("portal not supported on this platform")
}

// IsDocPortalPath always returns false on non-Linux platforms.
func IsDocPortalPath(path string) bool {
	return false
}
