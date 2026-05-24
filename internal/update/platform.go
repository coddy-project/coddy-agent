package update

import (
	"fmt"
	"runtime"
)

// AssetFileName returns the release archive name for CI-published binaries.
func AssetFileName(version, goos, goarch string) (string, error) {
	switch goos {
	case "linux", "darwin":
		switch goarch {
		case "amd64", "arm64":
			return fmt.Sprintf("coddy_%s_%s_%s.tar.gz", version, goos, goarch), nil
		}
	case "windows":
		if goarch == "amd64" {
			return fmt.Sprintf("coddy_%s_windows_amd64.zip", version), nil
		}
	}
	return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
}

// BinaryName inside release archives.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "coddy.exe"
	}
	return "coddy"
}

// CurrentPlatform returns goos/goarch for this process.
func CurrentPlatform() (goos, goarch string) {
	return runtime.GOOS, runtime.GOARCH
}
