//go:build unix

package wallpaper

import (
	"os"
	"syscall"
)

// Nonblocking opens prevent FIFO substitutions from waiting for a writer.
// openChecked also rejects links that os.Root resolves within the gallery.
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
