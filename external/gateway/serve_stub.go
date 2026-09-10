//go:build !(gateway || gateway.telegram)

package gateway

import (
	"context"
	"errors"
)

// Available reports whether this binary carries any messenger adapter.
const Available = false

// Serve reports that every adapter was left out of this build.
func Serve(context.Context, Options) error {
	return errors.New("gateway: not built in (rebuild with -tags gateway.telegram for Telegram, or -tags gateway for every adapter)")
}
