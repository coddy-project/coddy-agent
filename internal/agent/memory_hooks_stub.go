//go:build !memory

package agent

import "context"

// runMemoryBeforeTurn is a no-op in a binary built without the memory tag:
// the memory keys are accepted and nothing runs.
func (a *Agent) runMemoryBeforeTurn(context.Context, string, string) {}

// registerMemoryChildTools registers nothing without the memory tag.
func (a *Agent) registerMemoryChildTools() {}
