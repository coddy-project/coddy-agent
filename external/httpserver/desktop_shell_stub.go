//go:build http && ui && !desktop

package httpserver

import "net/http"

// Without the desktop surface there is no shell to serve, and /desktop/ is an
// ordinary address: the SPA answers it as it answers any other, exactly as a
// build of an older version does.
//
// The constraint is the exact negation of desktop_shell.go within `http && ui`,
// and not within `http` alone, because the one caller is the SPA's catch-all
// (spa_embed_ui.go, `http && ui`). A stub that also existed in an `http`-only
// build would be a constant and a function nothing can call.
const desktopShellPrefix = "/desktop/"

func desktopShellHandler(*Server) http.Handler { return nil }
