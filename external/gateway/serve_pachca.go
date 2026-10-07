//go:build gateway || gateway.pachca

package gateway

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/pachca"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// PachcaAvailable reports whether this binary carries the Pachca adapter.
const PachcaAvailable = true

// ServePachca builds the Pachca bot and runs it until ctx is cancelled.
func ServePachca(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Mgr == nil || opts.Log == nil {
		return errors.New("gateway: Cfg, Mgr and Log are required")
	}
	pc := &opts.Cfg.Gateways.Pachca
	if !pc.Enabled {
		return errors.New("gateway: the Pachca bot is not enabled; set gateways.pachca.enable: true in config")
	}
	if pc.EffectiveToken() == "" {
		return errors.New("gateways.pachca.enable is true but no token was found; set gateways.pachca.token or the " +
			config.PachcaBotTokenEnvVar + " environment variable")
	}
	// The Pachca bot keeps its own files: two stores rewriting one map on
	// every change would undo each other's writes.
	root := opts.Cfg.ResolvedSessionsRoot()
	bot := pachca.New(pc, opts.Mgr, opts.DefaultCWD,
		logger.Component(opts.Log, logger.ComponentGatewayPachca),
		filepath.Join(root, "gateway_pachca_sessions.json"),
		filepath.Join(root, "gateway_pachca_state.json"), opts.Mirror)
	if opts.Prompts != nil {
		bot.SetPromptSurfaces(opts.Prompts)
	}
	if opts.Wakes != nil {
		bot.SetWakeSurfaces(opts.Wakes)
	}
	NewHub(logger.Component(opts.Log, logger.ComponentGateway), bot).Start(ctx)
	return nil
}
