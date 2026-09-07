//go:build swarm

package main

import "github.com/EvilFreelancer/coddy-agent/external/swarm"

func runSwarm(args []string) error {
	return swarm.Run(args, swarm.CommandDeps{EnsureHome: ensureCoddyHomeLayout})
}
