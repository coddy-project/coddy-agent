//go:build !(desktop && http)

// The desktop window needs both the surface it opens on and the HTTP server
// that serves it, so a binary built without either says so rather than
// starting something that cannot show a window. This mirrors what
// cmd/coddy/cli_stub.go and cmd/coddy/metrics_stub.go do for their surfaces.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "coddy-desktop was built without the desktop surface.")
	fmt.Fprintln(os.Stderr, "Rebuild it with `make desktop` (build tags: http ui desktop).")
	os.Exit(1)
}
