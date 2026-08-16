package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// Staged UCI commands live next to the session bundle so they survive process
// restarts and HTTP permission resumes; sessions without a persisted directory
// fall back to a process-local map keyed by session id. configStagingMu also
// serializes the whole stage/commit/rollback tool family.

const configStagingFileName = "config_staging.json"

var (
	configStagingMu  sync.Mutex
	configStagingMem = map[string][]string{}
)

type configStagingRecord struct {
	Commands []string `json:"commands"`
}

func configStagingFilePath(env *tooling.Env) string {
	if env == nil || strings.TrimSpace(env.SessionDir) == "" {
		return ""
	}
	return filepath.Join(env.SessionDir, configStagingFileName)
}

// loadStagedConfigCommands returns the session's pending commands. Callers hold configStagingMu.
func loadStagedConfigCommands(env *tooling.Env) ([]string, error) {
	if path := configStagingFilePath(env); path != "" {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read staged config commands: %w", err)
		}
		var rec configStagingRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("parse staged config commands: %w", err)
		}
		return rec.Commands, nil
	}
	return append([]string(nil), configStagingMem[stagingKey(env)]...), nil
}

// saveStagedConfigCommands replaces the session's pending commands. Callers hold configStagingMu.
func saveStagedConfigCommands(env *tooling.Env, commands []string) error {
	if path := configStagingFilePath(env); path != "" {
		if len(commands) == 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("clear staged config commands: %w", err)
			}
			return nil
		}
		raw, err := json.Marshal(configStagingRecord{Commands: commands})
		if err != nil {
			return fmt.Errorf("encode staged config commands: %w", err)
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o600); err != nil {
			return fmt.Errorf("write staged config commands: %w", err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("write staged config commands: %w", err)
		}
		return nil
	}
	key := stagingKey(env)
	if len(commands) == 0 {
		delete(configStagingMem, key)
		return nil
	}
	configStagingMem[key] = append([]string(nil), commands...)
	return nil
}

func stagingKey(env *tooling.Env) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env.SessionID)
}
