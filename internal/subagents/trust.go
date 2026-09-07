package subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TrustFileName is the receipts file inside the coddy home directory. It is a
// sibling of mcp-trust.json rather than the same file: those records are typed
// for MCP declarations, and one kind of approval must never read as the other.
const TrustFileName = "subagents-trust.json"

const trustFileVersion = 1

// TrustState is what the catalog and the runtime report for a definition.
type TrustState string

const (
	// TrustTrusted means the definition may be spawned: built-in, user scope,
	// project scope under allow, or project scope with a matching receipt.
	TrustTrusted TrustState = "trusted"
	// TrustNeedsApproval means a project-scope definition under ask has no
	// receipt for this workspace and this file content.
	TrustNeedsApproval TrustState = "needs_approval"
)

// TrustRecord is the receipt for one approved project-scope definition: the
// workspace it was approved in (the map key), its name, the digest of the
// file bytes, and where the file was when the operator approved it.
type TrustRecord struct {
	Name       string `json:"name"`
	Digest     string `json:"digest"`
	Path       string `json:"path,omitempty"`
	ApprovedAt string `json:"approved_at"`
}

type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists receipts at <home>/subagents-trust.json. Every
// operation re-reads the file, so an approval granted through the CLI or the
// HTTP route reaches a running agent on its next spawn.
type TrustStore struct {
	path string
	mu   sync.Mutex
}

// NewTrustStore returns the store backed by <home>/subagents-trust.json.
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

// Path returns the receipts file path.
func (s *TrustStore) Path() string { return s.path }

func (s *TrustStore) read() trustFile {
	file := trustFile{Version: trustFileVersion, Workspaces: map[string][]TrustRecord{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return file
	}
	var parsed trustFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return file
	}
	if parsed.Workspaces == nil {
		parsed.Workspaces = map[string][]TrustRecord{}
	}
	parsed.Version = trustFileVersion
	return parsed
}

func (s *TrustStore) write(file trustFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Records returns the receipts recorded for a canonical workspace.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.read().Workspaces[workspace]
	out := append([]TrustRecord(nil), recs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Approved reports whether a receipt binds this workspace, name and digest.
func (s *TrustStore) Approved(workspace, name, digest string) bool {
	if s == nil || strings.TrimSpace(digest) == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.read().Workspaces[workspace] {
		if r.Name == name && r.Digest == digest {
			return true
		}
	}
	return false
}

// Approve records a receipt for def in workspace, replacing any earlier
// receipt for the same name.
func (s *TrustStore) Approve(workspace string, def *Definition) error {
	if def == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.read()
	kept := make([]TrustRecord, 0, len(file.Workspaces[workspace])+1)
	for _, r := range file.Workspaces[workspace] {
		if r.Name != def.Name {
			kept = append(kept, r)
		}
	}
	kept = append(kept, TrustRecord{
		Name:       def.Name,
		Digest:     def.Digest,
		Path:       def.Path,
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	})
	file.Workspaces[workspace] = kept
	return s.write(file)
}

// Revoke removes the receipt for name in workspace and reports whether one
// existed.
func (s *TrustStore) Revoke(workspace, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.read()
	recs := file.Workspaces[workspace]
	kept := make([]TrustRecord, 0, len(recs))
	removed := false
	for _, r := range recs {
		if r.Name == name {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	if !removed {
		return false, nil
	}
	if len(kept) == 0 {
		delete(file.Workspaces, workspace)
	} else {
		file.Workspaces[workspace] = kept
	}
	return true, s.write(file)
}

// Decide returns the trust state of a definition for a workspace under the
// subagents.project_trust policy. Built-ins and user-scope files are always
// trusted; project-scope files are trusted under allow, or under ask once a
// receipt matches their digest.
func Decide(def *Definition, policy, workspace string, store *TrustStore) TrustState {
	if def == nil {
		return TrustNeedsApproval
	}
	if def.Builtin || def.Scope != ScopeProject {
		return TrustTrusted
	}
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "allow":
		return TrustTrusted
	default:
		if store != nil && store.Approved(workspace, def.Name, def.Digest) {
			return TrustTrusted
		}
		return TrustNeedsApproval
	}
}
