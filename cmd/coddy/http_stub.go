//go:build !http

package main

import "fmt"

func runHTTP([]string) error {
	return fmt.Errorf("http support is not built in (rebuild with: go build -tags=http)")
}
