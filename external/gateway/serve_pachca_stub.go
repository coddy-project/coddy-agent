//go:build !(gateway || gateway.pachca)

package gateway

import (
	"context"
	"errors"
)

// PachcaAvailable reports whether this binary carries the Pachca adapter.
const PachcaAvailable = false

// ServePachca reports that the Pachca adapter was left out of this build.
func ServePachca(context.Context, Options) error {
	return errors.New("gateway: Pachca is not built in (rebuild with -tags gateway.pachca, or -tags gateway for every adapter)")
}
