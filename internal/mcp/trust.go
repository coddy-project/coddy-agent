package mcp

// Workspace trust bookkeeping for project-local MCP declarations.
//
// A <workspace>/.coddy/mcp.json entry is repository content: it names the
// command, arguments, and environment of a process Coddy would start while
// bootstrapping a session, long before the model or any tool permission
// prompt is involved. Approvals are therefore recorded out of band, in the
// operator's own home directory, and bound to both the canonical workspace
// and a digest of the command-bearing declaration, so an approved entry that
// is later rewritten needs approving again.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TrustFileName is the approvals file inside the coddy home directory.
const TrustFileName = "mcp-trust.json"

// trustFileVersion is the schema version of the approvals file.
const trustFileVersion = 1

// TrustRecord is the receipt for one approved declaration: what the operator
// was shown, and the digest that approval is bound to. Values of env vars and
// headers are deliberately not stored (they routinely hold secrets); the
// digest still covers them, so changing one withdraws the approval.
type TrustRecord struct {
	Server     string   `json:"server"`
	Digest     string   `json:"digest"`
	Transport  string   `json:"transport"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	EnvKeys    []string `json:"env_keys,omitempty"`
	URL        string   `json:"url,omitempty"`
	HeaderKeys []string `json:"header_keys,omitempty"`
	Source     string   `json:"source,omitempty"`
	ApprovedAt string   `json:"approved_at"`
}

// trustFile is the on-disk layout: canonical workspace path -> approvals.
type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists approvals at <home>/mcp-trust.json. Every operation
// re-reads the file, so an approval granted through the HTTP API or the CLI
// reaches an already running agent on its next session.
type TrustStore struct {
	path string
	mu   sync.Mutex
}

// NewTrustStore returns the store backed by <home>/mcp-trust.json. home is
// the coddy state directory (~/.coddy).
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

// Path returns the approvals file path (shown to operators alongside errors).
func (s *TrustStore) Path() string { return s.path }

// CanonicalWorkspace normalises a session cwd into the key approvals are
// stored under: absolute, symlink-resolved where possible, and cleaned. A
// path that cannot be resolved (it may not exist yet) still yields a stable
// absolute form.
func CanonicalWorkspace(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = filepath.Clean(cwd)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// Fingerprint returns a stable digest of everything in a declaration that
// decides what gets executed or contacted: the transport, the command line,
// the environment, the URL, and the headers. Operational switches (disabled,
// disabledTools) are excluded, so toggling a tool off does not withdraw an
// approval, while editing the command line does.
func Fingerprint(srv config.MCPServerConfig) string {
	payload := struct {
		Name      string   `json:"name"`
		Transport string   `json:"transport"`
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		Env       []string `json:"env"`
		URL       string   `json:"url"`
		Headers   []string `json:"headers"`
	}{
		Name:      srv.Name,
		Transport: EffectiveTransport(srv),
		Command:   srv.Command,
		Args:      append([]string(nil), srv.Args...),
		Env:       pairsForDigest(len(srv.Env), func(i int) (string, string) { return srv.Env[i].Name, srv.Env[i].Value }),
		URL:       srv.URL,
		Headers:   pairsForDigest(len(srv.Headers), func(i int) (string, string) { return srv.Headers[i].Name, srv.Headers[i].Value }),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// Marshalling plain strings cannot fail; fall back to a value that
		// never matches a stored digest rather than to an empty one.
		return "sha256:unmarshalable"
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pairsForDigest renders name/value pairs sorted, so YAML list order and JSON
// object order produce the same digest for the same declaration.
func pairsForDigest(n int, at func(int) (string, string)) []string {
	out := make([]string, 0, n)
	for i := range n {
		name, value := at(i)
		out = append(out, name+"="+value)
	}
	sort.Strings(out)
	return out
}

// keysOf returns the sorted names of name/value pairs, for receipts that must
// not record secret values.
func keysOf(n int, at func(int) string) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, at(i))
	}
	sort.Strings(out)
	return out
}

// NewTrustRecord builds the receipt for srv without storing secret values.
func NewTrustRecord(srv config.MCPServerConfig, source string, approvedAt time.Time) TrustRecord {
	return TrustRecord{
		Server:    srv.Name,
		Digest:    Fingerprint(srv),
		Transport: EffectiveTransport(srv),
		Command:   srv.Command,
		Args:      append([]string(nil), srv.Args...),
		EnvKeys:   keysOf(len(srv.Env), func(i int) string { return srv.Env[i].Name }),
		URL:       srv.URL,
		HeaderKeys: keysOf(len(srv.Headers), func(i int) string {
			return srv.Headers[i].Name
		}),
		Source:     source,
		ApprovedAt: approvedAt.UTC().Format(time.RFC3339),
	}
}

// read loads the approvals file. A missing file is not an error; a corrupt
// one is, so a damaged store never silently reads as "nothing approved" in a
// context where that would look like a fresh install.
func (s *TrustStore) read() (trustFile, error) {
	data, err := os.ReadFile(s.path) //nolint:gosec // path derives from the coddy home directory
	if err != nil {
		if os.IsNotExist(err) {
			return trustFile{Version: trustFileVersion, Workspaces: map[string][]TrustRecord{}}, nil
		}
		return trustFile{}, fmt.Errorf("read %s: %w", s.path, err)
	}
	var file trustFile
	if err := json.Unmarshal(data, &file); err != nil {
		return trustFile{}, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if file.Workspaces == nil {
		file.Workspaces = map[string][]TrustRecord{}
	}
	return file, nil
}

func (s *TrustStore) write(file trustFile) error {
	file.Version = trustFileVersion
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(s.path), err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	// 0o600: the receipts name local commands and workspaces of this operator only.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	return nil
}

// Records returns the approvals recorded for a workspace, newest write order
// preserved. A corrupt store yields no records.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		return nil
	}
	return append([]TrustRecord(nil), file.Workspaces[CanonicalWorkspace(workspace)]...)
}

// Approved reports whether this exact declaration is approved for workspace.
// A corrupt store reads as "not approved": failing closed is the only safe
// direction here.
func (s *TrustStore) Approved(workspace string, srv config.MCPServerConfig) bool {
	digest := Fingerprint(srv)
	for _, rec := range s.Records(workspace) {
		if rec.Server == srv.Name && rec.Digest == digest {
			return true
		}
	}
	return false
}

// Approve records srv as approved for workspace, replacing any earlier
// approval of the same server name (an edited declaration supersedes the one
// it replaced instead of accumulating).
func (s *TrustStore) Approve(workspace, source string, srv config.MCPServerConfig) error {
	key := CanonicalWorkspace(workspace)
	if key == "" {
		return fmt.Errorf("mcp trust: empty workspace")
	}
	if strings.TrimSpace(srv.Name) == "" {
		return fmt.Errorf("mcp trust: empty server name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		return err
	}
	kept := make([]TrustRecord, 0, len(file.Workspaces[key])+1)
	for _, rec := range file.Workspaces[key] {
		if rec.Server != srv.Name {
			kept = append(kept, rec)
		}
	}
	kept = append(kept, NewTrustRecord(srv, source, time.Now()))
	file.Workspaces[key] = kept
	return s.write(file)
}

// Revoke drops every approval of one server name in a workspace and reports
// whether anything was removed.
func (s *TrustStore) Revoke(workspace, name string) (bool, error) {
	key := CanonicalWorkspace(workspace)
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		return false, err
	}
	records := file.Workspaces[key]
	kept := make([]TrustRecord, 0, len(records))
	for _, rec := range records {
		if rec.Server != name {
			kept = append(kept, rec)
		}
	}
	if len(kept) == len(records) {
		return false, nil
	}
	if len(kept) == 0 {
		delete(file.Workspaces, key)
	} else {
		file.Workspaces[key] = kept
	}
	return true, s.write(file)
}
