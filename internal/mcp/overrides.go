package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Project switches belong to the operator, not to the repository's MCP
// declaration. Keep them separate from trust receipts so approvals remain
// bound only to the executable declaration.
const overridesFileName = "mcp-overrides.json"

type projectSwitches struct {
	Disabled      *bool           `json:"disabled,omitempty"`
	DisabledTools map[string]bool `json:"disabled_tools,omitempty"`
}

type overridesFile struct {
	Workspaces map[string]map[string]projectSwitches `json:"workspaces"`
}

var overridesMu sync.Mutex

func overridesPath(home string) string { return filepath.Join(home, overridesFileName) }

func readOverrides(home string) (overridesFile, error) {
	var file overridesFile
	data, err := os.ReadFile(overridesPath(home)) //nolint:gosec // operator-owned home path
	if os.IsNotExist(err) {
		return overridesFile{Workspaces: make(map[string]map[string]projectSwitches)}, nil
	}
	if err != nil {
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("parse MCP overrides: %w", err)
	}
	if file.Workspaces == nil {
		file.Workspaces = make(map[string]map[string]projectSwitches)
	}
	return file, nil
}

func writeOverrides(home string, file overridesFile) error {
	path := overridesPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func updateProjectSwitch(cfg *config.Config, cwd, name string, change func(*projectSwitches)) error {
	if cfg.Paths.Home == "" {
		return fmt.Errorf("MCP home directory is required for project switches")
	}
	overridesMu.Lock()
	defer overridesMu.Unlock()
	file, err := readOverrides(cfg.Paths.Home)
	if err != nil {
		return err
	}
	workspace := CanonicalWorkspace(cwd)
	if file.Workspaces[workspace] == nil {
		file.Workspaces[workspace] = make(map[string]projectSwitches)
	}
	switches := file.Workspaces[workspace][name]
	change(&switches)
	file.Workspaces[workspace][name] = switches
	return writeOverrides(cfg.Paths.Home, file)
}

func applyProjectSwitches(home, cwd string, servers []ManagedServer) ([]ManagedServer, error) {
	if home == "" {
		return servers, nil
	}
	overridesMu.Lock()
	file, err := readOverrides(home)
	overridesMu.Unlock()
	if err != nil {
		return nil, err
	}
	entries := file.Workspaces[CanonicalWorkspace(cwd)]
	for i := range servers {
		if servers[i].Origin != OriginProject {
			continue
		}
		switches, ok := entries[servers[i].Config.Name]
		if !ok {
			continue
		}
		if switches.Disabled != nil {
			servers[i].Config.Disabled = *switches.Disabled
		}
		for name, disabled := range switches.DisabledTools {
			servers[i].Config.DisabledTools = config.SetToolDisabledList(servers[i].Config.DisabledTools, name, disabled)
		}
	}
	return servers, nil
}
