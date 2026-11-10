//go:build desktop && !(linux && !android)

package desktop

// The application entry is a file of the XDG desktop specification, which
// Linux reads. macOS has its bundle and Windows its Start menu shortcut, both
// written by an installer rather than by the program; neither has anything
// this would write.

func writeMenuEntry(string, []string) error { return nil }
