//go:build !memory

package agent

import "context"

func (a *Agent) runMemoryBeforeTurn(context.Context, string, string) {}
