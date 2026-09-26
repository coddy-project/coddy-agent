package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

func overridesPath(home string) string { return filepath.Join(home, overridesFileName) }

func readOverrides(home string) (overridesFile, error) {
	var file overridesFile
	path := overridesPath(home)
	data, err := os.ReadFile(path) //nolint:gosec // operator-owned home path
	if os.IsNotExist(err) {
		return overridesFile{Workspaces: make(map[string]map[string]projectSwitches)}, nil
	}
	if err != nil {
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		// The path is named: this is the file an operator has to repair.
		return file, fmt.Errorf("parse MCP overrides %s: %w", path, err)
	}
	if file.Workspaces == nil {
		file.Workspaces = make(map[string]map[string]projectSwitches)
	}
	return file, nil
}

func writeOverrides(home string, file overridesFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return writeStateFile(overridesPath(home), append(data, '\n'))
}

// updateProjectSwitch changes the operator's switches for one project server
// of a workspace. The read-modify-write holds the file's lock, so a console
// and coddy serve sharing one home never write over each other's switches.
func updateProjectSwitch(cfg *config.Config, cwd, name string, change func(*projectSwitches)) error {
	if cfg.Paths.Home == "" {
		return fmt.Errorf("MCP home directory is required for project switches")
	}
	return updateStateFile(overridesPath(cfg.Paths.Home), func() error {
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
	})
}

// dropProjectSwitches forgets the operator's switches for one project server
// of a workspace, and the workspace entry once it holds none.
func dropProjectSwitches(cfg *config.Config, cwd, name string) error {
	if cfg.Paths.Home == "" {
		return nil
	}
	return updateStateFile(overridesPath(cfg.Paths.Home), func() error {
		file, err := readOverrides(cfg.Paths.Home)
		if err != nil {
			return err
		}
		workspace := CanonicalWorkspace(cwd)
		entries, ok := file.Workspaces[workspace]
		if !ok {
			return nil
		}
		if _, ok := entries[name]; !ok {
			return nil
		}
		delete(entries, name)
		if len(entries) == 0 {
			delete(file.Workspaces, workspace)
		}
		return writeOverrides(cfg.Paths.Home, file)
	})
}

func applyProjectSwitches(home, cwd string, servers []ManagedServer) ([]ManagedServer, error) {
	if home == "" {
		return servers, nil
	}
	file, err := readOverrides(home)
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
