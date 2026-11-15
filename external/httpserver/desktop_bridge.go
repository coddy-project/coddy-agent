//go:build http && ui && desktop

package httpserver

import (
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// DesktopBridgeHandler keeps window actions in the desktop process while every
// other route goes to the service's runtime. In particular the browser never
// talks to the service's public origin: its same-origin and opaque-origin
// checks still run against this loopback bridge, and canvas capabilities keep
// their ordinary signed paths.
func DesktopBridgeHandler(upstream http.Handler, window any) http.Handler {
	ctl, _ := window.(shell.WindowControl)
	desktop := shell.Handler(ctl)
	return DesktopOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == strings.TrimSuffix(shell.Prefix, "/") {
			http.Redirect(w, r, shell.Prefix, http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(r.URL.Path, shell.Prefix) {
			desktop.ServeHTTP(w, r)
			return
		}
		upstream.ServeHTTP(w, r)
	}))
}
