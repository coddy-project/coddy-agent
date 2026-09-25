package config

// The mcp_servers list of the active config as it is and as a staged UCI
// batch would leave it, read without expansion or validation: what the
// staged config tools compare to find the servers a batch registers, so an
// npx package among them can be pinned before the batch is committed.

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// StagedMCPServers reads the mcp_servers list of the active config file as
// it is now and as it would be once cmds are applied, without expanding
// ${VAR} placeholders and without touching the file. The staged config tools
// use it to find the servers a batch adds or changes, so an `npx -y
// <package>` among them can be pinned before the batch is committed.
func StagedMCPServers(paths Paths, cmds []UCICommand) (before, after []MCPServerConfig, err error) {
	raw, _, err := readExistingConfigBytes(paths.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	if before, err = decodeMCPServersRaw(raw); err != nil {
		return nil, nil, err
	}
	updated, err := applyUCICommandsToBytes(paths, raw, cmds)
	if err != nil {
		return nil, nil, err
	}
	if after, err = decodeMCPServersRaw(updated); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// decodeMCPServersRaw reads only the mcp_servers list of a config document,
// as written: no defaults, no expansion, no validation.
func decodeMCPServersRaw(raw []byte) ([]MCPServerConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var doc struct {
		MCPServers []MCPServerConfig `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.MCPServers, nil
}
