//go:build !swarm

package main

import "fmt"

func runSwarm([]string) error {
	return fmt.Errorf("swarm support is not built in (rebuild with: go build -tags=swarm)")
}
