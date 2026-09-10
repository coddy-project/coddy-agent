//go:build gateway || gateway.telegram

package gateway

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/telegram"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Start builds all enabled gateway adapters and runs the hub. Blocks until ctx is cancelled.
func Start(ctx context.Context, cfg *config.Config, mgr *session.Manager, log *slog.Logger, defaultCWD string) {
	// Each adapter logs under its own component so logger.levels can raise one
	// bot to debug without the rest of the process following it. Adapter tags
	// are derived from the untagged logger, not stacked on the hub's own, so a
	// record carries exactly one component.
	hubLog := logger.Component(log, logger.ComponentGateway)
	var adapters []Adapter

	if cfg.Gateways.Telegram.Enabled {
		if cfg.Gateways.Telegram.EffectiveToken() == "" {
			hubLog.Warn("gateway: telegram enabled but no token; set gateways.telegram.token or the " +
				config.TelegramBotTokenEnvVar + " environment variable")
		} else {
			storePath := filepath.Join(cfg.ResolvedSessionsRoot(), "gateway_sessions.json")
			bot := telegram.New(&cfg.Gateways.Telegram, mgr, defaultCWD,
				logger.Component(log, logger.ComponentGatewayTelegram), storePath)
			adapters = append(adapters, bot)
		}
	}

	if len(adapters) == 0 {
		hubLog.Warn("gateway: no adapters enabled; set gateways.telegram.enabled: true in config")
		return
	}

	hub := NewHub(hubLog, adapters...)
	hub.Start(ctx)
}
