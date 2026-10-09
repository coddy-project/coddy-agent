//go:build scheduler

package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// RegistryFileName lists the workspaces whose project jobs the daemon scans
// although they hold no receipt: every workspace a session asked the
// scheduler about. Under scheduler.project_trust allow no receipt is ever
// written, so this is how a project job there comes to run.
const RegistryFileName = "scheduler-workspaces.json"

type registryFile struct {
	Version    int               `json:"version"`
	Workspaces map[string]string `json:"workspaces"`
}

// Registry persists the known scheduler workspaces at
// <home>/scheduler-workspaces.json, each with the time it was last seen.
type Registry struct {
	path string
}

var registryLocks sync.Map

// NewRegistry returns the registry of a coddy home.
func NewRegistry(home string) *Registry {
	return &Registry{path: filepath.Join(home, RegistryFileName)}
}

func (r *Registry) lock() *sync.Mutex {
	m, _ := registryLocks.LoadOrStore(r.path, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (r *Registry) read() registryFile {
	file := registryFile{Version: 1, Workspaces: map[string]string{}}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return file
	}
	var parsed registryFile
	if json.Unmarshal(data, &parsed) != nil || parsed.Workspaces == nil {
		return file
	}
	parsed.Version = 1
	return parsed
}

// List returns the registered workspaces, sorted.
func (r *Registry) List() []string {
	mu := r.lock()
	mu.Lock()
	defer mu.Unlock()
	file := r.read()
	out := make([]string, 0, len(file.Workspaces))
	for ws := range file.Workspaces {
		out = append(out, ws)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a workspace is registered.
func (r *Registry) Has(workspace string) bool {
	mu := r.lock()
	mu.Lock()
	defer mu.Unlock()
	_, ok := r.read().Workspaces[workspace]
	return ok
}

// Add registers a canonical workspace; a workspace already there is not
// rewritten more than once a day.
func (r *Registry) Add(workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	mu := r.lock()
	mu.Lock()
	defer mu.Unlock()
	file := r.read()
	now := time.Now().UTC()
	if seen, ok := file.Workspaces[workspace]; ok {
		if t, err := time.Parse(time.RFC3339, seen); err == nil && now.Sub(t) < 24*time.Hour {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("scheduler registry: %w", err)
	}
	unlock, err := platform.LockFile(r.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	file = r.read()
	file.Workspaces[workspace] = now.Format(time.RFC3339)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return replaceAtomically(r.path, append(data, '\n'))
}
