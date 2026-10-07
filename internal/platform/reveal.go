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
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return ErrRevealUnsupported
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return ErrRevealHeadless
	}
	argv, err := revealFileArgv(runtime.GOOS, path)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is fixed by platform and verified artifact path
	AdaptCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start file reveal: %w", err)
	}
	return nil
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
	case "linux":
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
