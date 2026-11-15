package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"regexp"
	"runtime"
	"strings"
)

var ErrRevealUnsupported = errors.New("revealing files is not supported on this server")
var ErrRevealHeadless = errors.New("revealing files requires a local graphical desktop")

// RevealFile selects path in the local desktop file manager. It deliberately
// accepts a path only from a server-side owner; HTTP handlers must resolve an
// artifact id before calling it.
func RevealFile(path string) error {
	if !revealSupported(runtime.GOOS) {
		return ErrRevealUnsupported
	}
	if revealNeedsDisplay(runtime.GOOS) && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return ErrRevealHeadless
	}
	argv, err := revealFileArgv(runtime.GOOS, path)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...) // argv is fixed by platform and verified artifact path
	AdaptCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start file reveal: %w", err)
	}
	return nil
}

// revealSupported reports whether this platform has a file manager to show a
// file in. macOS and Windows have one of their own; Linux and FreeBSD run the
// same freedesktop desktops, so both reach it through xdg-open.
func revealSupported(goos string) bool {
	switch goos {
	case "darwin", "windows", "linux", "freebsd":
		return true
	default:
		return false
	}
}

// revealNeedsDisplay reports whether a session without a display has no file
// manager to reach. It is the desktops that run on a display server, not the
// two platforms whose shell is always there.
func revealNeedsDisplay(goos string) bool {
	return goos == "linux" || goos == "freebsd"
}

func revealFileArgv(goos, path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !isAbsolutePathForOS(goos, path) {
		return nil, errors.New("artifact source path is invalid")
	}
	switch goos {
	case "darwin":
		return []string{"open", "-R", path}, nil
	case "windows":
		return []string{"explorer.exe", "/select," + path}, nil
	case "linux", "freebsd":
		return []string{"xdg-open", pathpkg.Dir(path)}, nil
	default:
		return nil, ErrRevealUnsupported
	}
}

var windowsAbsolutePath = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)

func isAbsolutePathForOS(goos, path string) bool {
	if goos == "windows" {
		return windowsAbsolutePath.MatchString(path) || strings.HasPrefix(path, `\\`)
	}
	return strings.HasPrefix(path, "/")
}
