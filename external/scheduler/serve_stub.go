//go:build !scheduler

package scheduler

import (
	"context"
	"errors"
)

// Available reports whether this binary can run the scheduler.
const Available = false

// Serve reports that the scheduler was left out of this build.
func Serve(context.Context, Options) error {
	return errors.New("scheduler: not built in (rebuild with -tags scheduler)")
}
