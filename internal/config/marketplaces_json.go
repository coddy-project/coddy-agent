package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Skill marketplaces are declared in two files of one shape, never in
// config.yaml: the operator's <home>/marketplaces.json and the project's
// <cwd>/.coddy/marketplaces.json, which arrives with the checkout and is read
// only as skills.project_trust allows (internal/skills). What a declared
// marketplace listed when it was last read, and which of its plugins are
// installed, is state kept in the managed skills dir, not here.

// marketplacesJSONName is the file name of both declaration files.
const marketplacesJSONName = "marketplaces.json"

// MarketplacesFile is the shape of a marketplaces.json file.
type MarketplacesFile struct {
	// Sources are installed whole: every plugin a source publishes (or every
	// skill of a repository without a marketplace.json) is installed and kept
	// in sync. A GitHub owner/repo, a git URL or a marketplace.json URL.
	Sources []string `json:"sources,omitempty"`
	// Marketplaces are catalogs, as `plugin marketplace add` adds them: their
	// plugins are installed one by one with `plugin install <plugin>@<name>`,
	// and an update touches only the installed ones.
	Marketplaces []DeclaredMarketplace `json:"marketplaces,omitempty"`
}

// DeclaredMarketplace is one catalog of a marketplaces.json file.
type DeclaredMarketplace struct {
	// Name is the name its marketplace.json gives, what <plugin>@<name> uses.
	Name string `json:"name"`
	// Source is where it is read from: owner/repo, a git URL or a
	// marketplace.json URL.
	Source string `json:"source"`
}

// GlobalMarketplacesPath is the operator's declaration file in the coddy home.
func GlobalMarketplacesPath(home string) string {
	return filepath.Join(home, marketplacesJSONName)
}

// ProjectMarketplacesPath is the declaration file a project carries.
func ProjectMarketplacesPath(cwd string) string {
	return filepath.Join(cwd, ".coddy", marketplacesJSONName)
}

// ReadMarketplacesFile reads a declaration file. A missing file declares
// nothing; a file that does not parse is an error, so nothing writes over
// what it holds.
func ReadMarketplacesFile(path string) (MarketplacesFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path derives from the home or the workspace
	if errors.Is(err, os.ErrNotExist) {
		return MarketplacesFile{}, nil
	}
	if err != nil {
		return MarketplacesFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var file MarketplacesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return MarketplacesFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, nil
}

// WriteMarketplacesFile replaces a declaration file in one rename.
func WriteMarketplacesFile(path string, file MarketplacesFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o644)
}
