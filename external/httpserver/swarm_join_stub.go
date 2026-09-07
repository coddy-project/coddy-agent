//go:build http && !swarm

package httpserver

import (
	"context"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// startSwarmJoins does nothing without the swarm build tag: the configuration
// block may be present, but a binary built without swarm support joins nothing
// and behaves exactly as it did before the feature existed.
func startSwarmJoins(_ context.Context, _ *config.Config, _ string, _ *slog.Logger) func() {
	return func() {}
}
