//go:build desktop && freebsd

package desktop

import "os"

func menuEntryOpenFlags() (int, error) { return os.O_WRONLY | os.O_TRUNC | os.O_CREATE, nil }
