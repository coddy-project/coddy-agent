//go:build !memory

package agent

import (
	"context"
	"errors"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// runMemoryBeforeTurn is a no-op in a binary built without the memory tag:
// the memory keys are accepted and nothing runs.
func (a *Agent) runMemoryBeforeTurn(context.Context, string, string) {}

// registerMemoryChildTools registers nothing without the memory tag.
func (a *Agent) registerMemoryChildTools() {}

// memorySpawnCheck refuses a spawn under the bundled memory definition: the
// memory package that supplies the child's tools is not compiled in.
func (a *Agent) memorySpawnCheck(*config.Config) error {
	return errors.New("the memory subagent is not available in this build (the memory tag is not compiled in)")
}

// memorySpawnSpec is unreachable without the tag: memorySpawnCheck refuses
// the spawn first.
func (a *Agent) memorySpawnSpec(*config.Config, *session.SubagentSpec, *string, string, string) {}
