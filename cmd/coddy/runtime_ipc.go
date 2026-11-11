package main

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
)

// claimRuntimeOwner is called before Runtime.Init. A build without HTTP has no
// browser surface to share and starts no IPC endpoint.
func claimRuntimeOwner(home string, available bool) (*localipc.Owner, error) {
	if !available {
		return nil, nil
	}
	owner, err := localipc.Claim(home)
	if err != nil {
		return nil, fmt.Errorf("this agent home already has a runtime, or its local IPC cannot be secured: %w", err)
	}
	return owner, nil
}
