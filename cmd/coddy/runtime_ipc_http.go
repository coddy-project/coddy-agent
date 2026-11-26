//go:build http

package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
			return owner.Serve(ctx, sessionManagementIPCHandler(rt.Mgr, handler))
		},
	})
}

func sessionManagementIPCHandler(mgr *session.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, sessionManagementIPCPath) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set(sessionManagementResponseHeader, "1")
		id := strings.TrimPrefix(r.URL.Path, sessionManagementIPCPath)
		if !validMutationSessionID(id) {
			http.Error(w, "invalid session ID", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		root := r.Header.Get(sessionManagementRootHeader)
		if mgr == nil || mgr.FileStore() == nil || !filepath.IsAbs(root) || !filepath.IsAbs(mgr.FileStore().Root) || !session.SameWorkspacePath(root, mgr.FileStore().Root) {
			http.Error(w, "session store root does not match runtime", http.StatusConflict)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/coddy/sessions/" + id
		clone.URL.RawPath = ""
		clone.Header.Del(sessionManagementRootHeader)
		clone.Header.Set(httpserver.DesktopNoAuthHeader, "1")
		next.ServeHTTP(w, clone)
	})
}
