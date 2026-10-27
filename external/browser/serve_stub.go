//go:build !browser

package browser

import "net/http"

// Available reports whether this binary carries the browser module.
const Available = false

// Mount takes no route in a binary without the browser module.
func Mount(*http.ServeMux, MountOptions) Mounted {
	return Mounted{Disconnect: func() {}, Close: func() {}}
}
