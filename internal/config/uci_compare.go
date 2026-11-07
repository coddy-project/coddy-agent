package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// CommitUCICommandsIfUnchanged compares the config bytes under the same
// process-local lock used by other Coddy config writes, then commits commands.
// External writers do not participate in this lock.
func CommitUCICommandsIfUnchanged(paths Paths, expected []byte, commands []UCICommand) (*ConfigCommitResult, error) {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	current, err := os.ReadFile(paths.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if (errors.Is(err, os.ErrNotExist) != (expected == nil)) || !bytes.Equal(current, expected) {
		return nil, fmt.Errorf("config changed while preparing Kimi sign-in")
	}
	return commitUCICommandsLocked(paths, commands)
}
