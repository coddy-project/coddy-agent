//go:build http && swarm

package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// startSwarmJoins registers this agent into every relay listed in swarm.join.
//
// It exists only under the swarm build tag; the stub beside it does nothing, so
// a plain http build starts no goroutine and opens no connection.
func startSwarmJoins(ctx context.Context, cfg *config.Config, home string, handler http.Handler, log *slog.Logger) func() {
	set, err := swarm.StartJoins(ctx, cfg, swarm.StartJoinsOptions{
		Kind: swarm.KindAgent, Home: home, Handler: handler, Log: log,
	})
	if err != nil {
		log.Error("swarm: could not start joining relays", "error", err)
		return func() {}
	}
	return set.Stop
}
