package mention

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Location is a path reading resolved against a session's workspace.
type Location struct {
	// Abs is the cleaned absolute path.
	Abs string
	// Display is what the model and the transcript see: the workspace-relative
	// path with forward slashes for a path inside the workspace ("./" for the
	// workspace itself), the absolute path otherwise.
	Display string
	// Inside reports whether the path lies in the workspace.
	Inside bool
}

// Resolve turns a path as typed into a location: "~" and "~/..." expand to
// home, a file:// URI to its path, an absolute path stays as it is, and
// anything else - "./x", "../x", "x" - is taken relative to cwd. It reports
// false for an empty path, or "~" when home is unknown. Resolve does not look
// at the disk except to compare symlinked spellings of the workspace.
func Resolve(cwd, home, typed string) (Location, bool) {
	p := strings.TrimSpace(typed)
	if p == "" {
		return Location{}, false
	}
	if strings.HasPrefix(p, "file:") {
		fp, ok := filePathFromURI(p)
		if !ok {
			return Location{}, false
		}
		p = fp
	}
	switch {
	case p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		if strings.TrimSpace(home) == "" {
			return Location{}, false
		}
		p = filepath.Join(home, filepath.FromSlash(p[1:]))
	case isAbsolute(p):
		p = filepath.FromSlash(p)
	default:
		p = filepath.Join(cwd, filepath.FromSlash(p))
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return Location{}, false
	}
	loc := Location{Abs: abs, Display: filepath.ToSlash(abs)}
	if rel, ok := workspaceRel(cwd, abs); ok {
		loc.Inside = true
		loc.Display = rel
	}
	return loc, true
}

// isAbsolute accepts the host's absolute form, plus a path rooted at "/" on
// Windows (the current drive's root). A drive path such as "C:/x" names no
// file on a Unix host, so there it stays relative and resolves to nothing.
func isAbsolute(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return true
	}
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(p, `\\`)
	}
	return false
}

// workspaceRel returns abs relative to cwd with forward slashes when abs lies
// inside cwd. Both are compared as written and, when that fails, with symlinks
// resolved: a workspace opened through a symlinked folder still claims a path
// typed in its physical spelling.
func workspaceRel(cwd, abs string) (string, bool) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", false
	}
	root, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", false
	}
	if rel, ok := relInside(root, abs); ok {
		return rel, true
	}
	rootReal, err1 := filepath.EvalSymlinks(root)
	absReal, err2 := evalExistingPrefix(abs)
	if err1 != nil || err2 != nil {
		return "", false
	}
	return relInside(rootReal, absReal)
}

func relInside(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "./", true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// evalExistingPrefix resolves the symlinks of the longest existing prefix of
// p and keeps the rest as written, so a path that does not exist yet still
// compares against its workspace.
func evalExistingPrefix(p string) (string, error) {
	rest := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// filePathFromURI turns a file:// URI into a filesystem path. On Windows the
// authority-less form file:///C:/x carries a leading slash before the drive.
func filePathFromURI(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if p == "" {
		p = u.Opaque
	}
	if p == "" {
		return "", false
	}
	if host := u.Host; host != "" && !strings.EqualFold(host, "localhost") {
		// A share on another machine: a UNC path on Windows, and nothing a
		// Unix host can open - never the local file of the same path.
		if runtime.GOOS != "windows" {
			return "", false
		}
		return `\\` + host + filepath.FromSlash(p), true
	}
	if len(p) >= 3 && p[0] == '/' && isASCIILetter(rune(p[1])) && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

// HomeDir is the operator's home directory, or "" when it cannot be told.
func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
