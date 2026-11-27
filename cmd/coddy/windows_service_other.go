//go:build !windows

package main

import "errors"

func runWindowsService(_ []string) error {
	return errors.New("Windows service is available only on Windows")
}
