//go:build !windows

package platform

// DecodeOutput returns captured child-process output unchanged. Outside Windows
// there is no console code page to undo: the tooling coddy runs already writes
// UTF-8.
func DecodeOutput(b []byte) string {
	return string(b)
}

// DecodeANSI always reports a miss. There is no system ANSI code page outside
// Windows: files elsewhere carry no implied legacy encoding to fall back on.
func DecodeANSI(_ []byte) (text string, codePage uint32, ok bool) {
	return "", 0, false
}
