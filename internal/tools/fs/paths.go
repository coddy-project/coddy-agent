package fs

import "path/filepath"

// ResolvePath returns an absolute path, resolving relative to cwd.
func ResolvePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

