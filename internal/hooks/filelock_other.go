//go:build !unix && !windows

package hooks

// lockFile is a no-op where no file locking primitive is available; the
// in-process mutex still serialises writers of one process.
func lockFile(string) (func(), error) {
	return func() {}, nil
}
