package main

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/dryrun"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// runConsoleDryRun is what the console (and its lean stub) hand --dry-run to:
// the static check, then the probes, printed where the config check prints.
func runConsoleDryRun(cli config.CLIPaths, remoteArg, remoteToken string, customize func(*config.Config) error) error {
	return dryrun.RunConsole(configTestOutput, dryrun.SurfaceConsole, cli, remoteArg, remoteToken, customize)
}

// runServeDryRun resolves the subsystems exactly as a start would - the
// configuration with the typed flags applied - and probes the addresses they
// would bind, on top of everything the config names.
func runServeDryRun(cli config.CLIPaths, apply func(*config.Config) error, httpListenAddr, swarmListenAddr func(*config.Config) string) error {
	return dryrun.RunAndReport(configTestOutput, cli, apply, func(prep *dryrun.Prepared) (dryrun.Request, error) {
		rt := &serve.Runtime{}
		all := subsystems(rt, subsystemDeps{
			httpListenAddr:  httpListenAddr,
			swarmListenAddr: swarmListenAddr,
			home:            prep.Paths.Home,
		})
		enabled, rerr := serve.Resolve(prep.Cfg, all)
		req := dryrun.Request{Surface: dryrun.SurfaceServe, SubsystemErr: rerr}
		for _, sub := range enabled {
			switch sub.Kind {
			case serve.KindHTTP:
				req.Listeners = append(req.Listeners, dryrun.Listener{Path: "httpserver", Addr: httpListenAddr(prep.Cfg)})
			case serve.KindSwarm:
				req.Listeners = append(req.Listeners, dryrun.Listener{Path: "swarm", Addr: swarmListenAddr(prep.Cfg)})
			}
		}
		return req, nil
	})
}
