//go:build !http

package main

import (
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func runtimeIPCSubsystems(all []serve.Subsystem, _ *serve.Runtime, _ subsystemDeps, _ *localipc.Owner) []serve.Subsystem {
	return all
}
