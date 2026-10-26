//go:build !browser

package browser

import (
	"context"
	"errors"
)

// Available reports whether this binary carries the browser module.
const Available = false

// Serve reports that the browser module was left out of this build.
func Serve(context.Context, Options) error {
	return errors.New("browser: not built in (rebuild with -tags browser)")
}
