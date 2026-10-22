//go:build !windows

package main

import (
	"context"
	"fmt"
)

func execute(_ context.Context, _ request) error {
	return fmt.Errorf("the native Firefox input adapter currently requires Windows")
}
