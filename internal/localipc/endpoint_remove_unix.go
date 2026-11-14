//go:build !windows

package localipc

import (
	"errors"
	"os"
	"strings"
)

func removeEndpoint(rec record) error {
	if rec.Network != "unix" || strings.HasPrefix(rec.Address, "@") {
		return nil
	}
	if err := os.Remove(rec.Address); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
