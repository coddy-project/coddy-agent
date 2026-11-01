//go:build !unix

package wallpaper

import "os"

// Non-Unix platforms have no FIFOs in the file namespace. The opened file's
// regular mode and identity are still checked against the preceding Lstat.
const readFlags = os.O_RDONLY
