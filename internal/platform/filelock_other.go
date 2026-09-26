//go:build !unix && !windows

package platform

// LockFile is a no-op where no file locking primitive is available; the
// in-process mutex of the caller still serialises writers of one process.
func LockFile(string) (func(), error) {
	return func() {}, nil
}
