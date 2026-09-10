//go:build !swarm

package swarm

import (
	"context"
	"errors"
)

// Available reports whether this binary can run the relay.
const Available = false

// Serve reports that the relay was left out of this build.
func Serve(context.Context, Options) error {
	return errors.New("swarm: not built in (rebuild with -tags swarm)")
}
