//go:build !memory

package agent

import "context"

func (a *Agent) runMemoryRecall(ctx context.Context, userText string) {}

func (a *Agent) runMemoryPersist(ctx context.Context, userText, assistant string) {}
