//go:build windows

package main

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func runWindowsService(args []string) error {
	return serve.RunWindowsService("Coddy", args, func(ctx context.Context, serviceArgs []string) error {
		return runServeContext(ctx, serviceArgs)
	})
}
