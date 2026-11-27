package main

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func shouldClaimRuntimeOwner(available bool) bool {
	// A Windows service runs under a virtual account. Its owner-only named
	// pipe cannot be used by the interactive desktop and resolving a user's
	// profile as that account can block SCM startup.
	return available && serve.Role() != serve.RoleService
}

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
