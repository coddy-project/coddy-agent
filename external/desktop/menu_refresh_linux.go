//go:build desktop && linux && !android

package desktop

import (
	"errors"
	"fmt"
	"os"
)

func menuEntryOpenFlags() (int, error) {
	receipt, err := ShortcutReceiptPath()
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(receipt); err == nil {
		return os.O_WRONLY | os.O_TRUNC, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("desktop: inspect shortcut receipt: %w", err)
	}
	return os.O_WRONLY | os.O_TRUNC | os.O_CREATE, nil
}
