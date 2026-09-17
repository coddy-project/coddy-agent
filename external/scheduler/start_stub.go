//go:build !scheduler

package scheduler

import "context"

// Start is a no-op when built without the scheduler tag.
func Start(context.Context, Options) {}
