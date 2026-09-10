//go:build gateway || gateway.telegram

package gateway

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/telegram"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// Available reports whether this binary carries any messenger adapter.
const Available = true

// Serve builds every enabled adapter and runs the hub until ctx is cancelled.
func Serve(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Mgr == nil || opts.Log == nil {
		return errors.New("gateway: Cfg, Mgr and Log are required")
	}
	// Each adapter logs under its own component so logger.levels can raise one
	// bot to debug without the rest of the process following it. Adapter tags
	// are derived from the untagged logger, not stacked on the hub's own, so a
	// record carries exactly one component.
	log := logger.Component(opts.Log, logger.ComponentGateway)
	var adapters []Adapter

	if opts.Cfg.Gateways.Telegram.Enabled {
		if opts.Cfg.Gateways.Telegram.EffectiveToken() == "" {
			return errors.New("gateways.telegram.enable is true but no token was found; set gateways.telegram.token or the " +
				config.TelegramBotTokenEnvVar + " environment variable")
		}
		storePath := filepath.Join(opts.Cfg.ResolvedSessionsRoot(), "gateway_sessions.json")
		adapters = append(adapters, telegram.New(&opts.Cfg.Gateways.Telegram, opts.Mgr, opts.DefaultCWD,
			logger.Component(opts.Log, logger.ComponentGatewayTelegram), storePath, opts.Mirror))
	}

	if len(adapters) == 0 {
		return errors.New("gateway: no adapter is enabled; set gateways.telegram.enable: true in config")
	}

	NewHub(log, adapters...).Start(ctx)
	return nil
}
