//go:build !windows

package main

import "errors"

func runWindowsService(_ []string) error {
	return errors.New("windows service is available only on Windows")
}

func traceServeStartup(uint32) {}
