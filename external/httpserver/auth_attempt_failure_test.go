//go:build http

package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestAuthDrainInvalidationFailureDoesNotLeakOrStopOtherAttempts(t *testing.T) {
	for _, kind := range []string{"codex", "neuraldeep", "opencode", "devin"} {
		t.Run(kind, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
			runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
				return "", nil
			}
			s := New(cfg, session.NewManager(cfg, noopSender{}, runner, logger, t.TempDir(), nil), logger, t.TempDir())
			calls := 0
			mk := func(fail bool) *codexAuthLoginAttempt {
				return &codexAuthLoginAttempt{ProviderName: "test", CreatedAt: time.Now(), cancelDurable: func() error {
					calls++
					if fail {
						return errors.New("private-token-detail")
					}
					return nil
				}}
			}
			var target map[string]*codexAuthLoginAttempt
			switch kind {
			case "codex":
				target = s.codexAuthLogins
			case "neuraldeep":
				target = s.neuralDeepAuthLogins
			case "opencode":
				target = s.opencodeAuthLogins
			case "devin":
				target = s.devinAuthLogins
			}
			target["a"] = mk(true)
			target["b"] = mk(false)
			switch kind {
			case "codex":
				s.cancelCodexAuthLogins()
			case "neuraldeep":
				s.cancelNeuralDeepAuthLogins()
			case "opencode":
				s.cancelOpenCodeAuthLogins("")
			case "devin":
				s.cancelDevinAuthLogins("")
			}
			if calls != 2 {
				t.Fatalf("cancelled %d attempts, want 2", calls)
			}
			if !strings.Contains(logs.String(), "invalidation failed") || strings.Contains(logs.String(), "private-token-detail") {
				t.Fatalf("unsafe or missing log: %q", logs.String())
			}
		})
	}
}
