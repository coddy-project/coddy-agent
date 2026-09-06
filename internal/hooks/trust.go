package hooks

// Workspace trust receipts for project-scope hooks files.
//
// A <workspace>/.coddy/hooks.json (or the Claude Code settings file next to
// it) is repository content: it names commands Coddy would run with the
// operator's permissions before every tool call. Approvals are therefore
// recorded out of band, in the operator's own home directory, and bound to
// both the canonical workspace and a digest of the file bytes, so an approved
// file that is later rewritten needs approving again. The store is a sibling
// of the MCP and subagent stores rather than a reuse of either: one kind of
// approval must never read as another.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TrustFileName is the receipts file inside the coddy home directory.
const TrustFileName = "hooks-trust.json"

const trustFileVersion = 1

// TrustRecord is the receipt for one approved project-scope file: its
// workspace-relative path, the digest the approval is bound to, and when it
// was granted.
type TrustRecord struct {
	File       string `json:"file"`
	Digest     string `json:"digest"`
	ApprovedAt string `json:"approved_at"`
}

type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists receipts at <home>/hooks-trust.json. Every operation
// re-reads the file, so an approval granted through the CLI or the HTTP route
// reaches a running agent on its next turn. Instances are cheap and created
// per request; the lock that serialises read-modify-write cycles is shared by
// every instance of the same path, and the file is replaced atomically.
type TrustStore struct {
	path string
}

// storeLocks holds one mutex per receipts file path for the whole process.
var storeLocks sync.Map

// NewTrustStore returns the store backed by <home>/hooks-trust.json.
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

func (s *TrustStore) lock() *sync.Mutex {
	m, _ := storeLocks.LoadOrStore(s.path, &sync.Mutex{})
	return m.(*sync.Mutex)
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
		return fmt.Errorf("hooks trust store: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("hooks trust store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("hooks trust store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hooks trust store: %w", err)
	}
	return nil
}

// Records returns the receipts recorded for a canonical workspace, sorted by
// file.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	out := append([]TrustRecord(nil), s.read().Workspaces[workspace]...)
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// Approved reports whether a receipt binds this workspace, file and digest.
func (s *TrustStore) Approved(workspace, file, digest string) bool {
	if workspace == "" || file == "" || digest == "" {
		return false
	}
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	for _, rec := range s.read().Workspaces[workspace] {
		if rec.File == file && rec.Digest == digest {
			return true
		}
	}
	return false
}

// Approve records a receipt for the current content of a project-scope
// source, replacing an earlier receipt for the same file. A user-scope file
// needs no receipt and an invalid file cannot be approved: both are refused
// rather than silently recorded.
func (s *TrustStore) Approve(workspace string, src *Source) error {
	switch {
	case src == nil:
		return fmt.Errorf("hooks trust store: no source to approve")
	case src.Scope != ScopeProject:
		return fmt.Errorf("hooks file %s is %s scope and needs no approval", src.Display, src.Scope)
	case src.Err != nil:
		return fmt.Errorf("hooks file %s cannot be approved: %v", src.Display, src.Err)
	case strings.TrimSpace(workspace) == "" || src.Digest == "":
		return fmt.Errorf("hooks trust store: workspace and digest are required")
	}
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	file := s.read()
	records := file.Workspaces[workspace]
	kept := records[:0]
	for _, rec := range records {
		if rec.File != src.Display {
			kept = append(kept, rec)
		}
	}
	kept = append(kept, TrustRecord{
		File:       src.Display,
		Digest:     src.Digest,
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	})
	file.Workspaces[workspace] = kept
	return s.write(file)
}

// Revoke removes the receipt of a file in a workspace and reports whether one
// was on file.
func (s *TrustStore) Revoke(workspace, file string) (bool, error) {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	tf := s.read()
	records := tf.Workspaces[workspace]
	kept := make([]TrustRecord, 0, len(records))
	removed := false
	for _, rec := range records {
		if rec.File == file {
			removed = true
			continue
		}
		kept = append(kept, rec)
	}
	if !removed {
		return false, nil
	}
	if len(kept) == 0 {
		delete(tf.Workspaces, workspace)
	} else {
		tf.Workspaces[workspace] = kept
	}
	return true, s.write(tf)
}

// WithStore makes the loader consult a receipts store when it decides the
// trust of a project-scope file under ask.
func (l *Loader) WithStore(store *TrustStore) *Loader {
	if store != nil {
		l.Approved = store.Approved
	}
	return l
}
