//go:build !memory

package agent

import (
	"errors"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// registerMemoryChildTools registers nothing without the memory tag.
func (a *Agent) registerMemoryChildTools() {}

// memorySpawnCheck refuses a spawn under the bundled memory definition: the
// memory package that supplies the child's tools is not compiled in.
func (a *Agent) memorySpawnCheck(*config.Config) error {
	return errors.New("the memory subagent is not available in this build (the memory tag is not compiled in)")
}

// memorySpawnVisible hides the bundled memory definition in a binary built
// without the memory tag.
func (a *Agent) memorySpawnVisible(*config.Config) bool { return false }

// memorySpawnSpec is unreachable without the tag: the definition is hidden
// and memorySpawnCheck refuses a spawn addressed by name.
func (a *Agent) memorySpawnSpec(*config.Config, *session.SubagentSpec, *string, string, string) {}
