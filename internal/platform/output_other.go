//go:build !windows

package platform

// DecodeOutput returns captured child-process output unchanged. Outside Windows
// there is no console code page to undo: the tooling coddy runs already writes
// UTF-8.
func DecodeOutput(b []byte) string {
	return string(b)
}
