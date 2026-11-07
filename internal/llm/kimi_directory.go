package llm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ensureKimiDirectory synchronizes the directory hierarchy before a durable
// credential mutation. Synchronizing existing ancestors also covers a
// directory another process created but has not yet flushed.
func ensureKimiDirectory(dir string, syncDir func(string) error) error {
	if strings.TrimSpace(dir) == "" || syncDir == nil {
		return errors.New("kimi credentials: directory and sync function required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return err
	}
	for path := abs; ; path = filepath.Dir(path) {
		if err := syncDir(path); err != nil {
			return fmt.Errorf("kimi credentials: sync directory %s: %w", path, err)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return nil
}
