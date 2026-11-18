//go:build http

package main

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// runtimeIPCSubsystems adds the private handler only after Resolve has decided
// the configured surfaces. It is independent of httpserver.enable and never
// joins a relay or starts another subsystem of its own.
func runtimeIPCSubsystems(all []serve.Subsystem, rt *serve.Runtime, deps subsystemDeps, owner *localipc.Owner) []serve.Subsystem {
	if owner == nil || rt.Mgr == nil {
		return all
	}
	return append(all, serve.Subsystem{
		Kind:      serve.KindLocalIPC,
		ConfigKey: "coddy desktop IPC",
		BuildTag:  "http",
		Available: true,
		Enabled:   func(*config.Config) bool { return true },
		Run: func(ctx context.Context, cfg *config.Config) error {
			withdrawMirror := func() {}
			withdrawPrompts := func() {}
			handler, cleanup, err := httpserver.LocalHandler(ctx, httpserver.Options{
				Cfg: cfg, Mgr: rt.Mgr, Log: rt.Log,
				DefaultCWD: rt.Paths.CWD, Home: deps.home,
				ExtraAuthTokens: deps.httpAuthTokens, ExtraLogin: deps.httpLogin,
				NoAuthHeader: httpserver.DesktopNoAuthHeader,
				Mirror:       rt, DetachedPrompts: rt, Wakes: rt,
				UnbuiltSections: unbuiltSections(deps), Status: rt.StatusDocument,
				OnServer: func(s *httpserver.Server) {
					if s == nil {
						withdrawMirror()
						withdrawPrompts()
						return
					}
					withdrawMirror = rt.AddTurnMirror(s)
					withdrawPrompts = rt.AddDetachedPermissionApprover(s)
				},
			})
			if err != nil {
				return err
			}
			defer cleanup()
			return owner.Serve(ctx, handler)
		},
	})
}
