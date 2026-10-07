package skills

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
)

// TrustFileName is the receipts file of the project marketplace entries the
// operator approved, inside the coddy home directory. It is a sibling of
// mcp-trust.json and subagents-trust.json rather than one of them: one kind of
// approval must never read as another.
const TrustFileName = "skills-trust.json"

const trustFileVersion = 1

// TrustRecord is the receipt for one approved entry of a project's
// .coddy/marketplaces.json: the workspace it was approved in (the map key),
// what it is and where it is read from, and the digest it was approved under.
type TrustRecord struct {
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Source     string `json:"source"`
	Digest     string `json:"digest"`
	Path       string `json:"path,omitempty"`
	ApprovedAt string `json:"approved_at"`
}

type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists receipts at <home>/skills-trust.json. Every operation
// reads the file again, so an approval granted through the CLI or the HTTP
// route reaches a running server on its next sync.
type TrustStore struct {
	path string
}

// trustMu serialises the read-modify-write cycles of every TrustStore of the
// process: two stores of one home are the same file.
var trustMu sync.Mutex

// NewTrustStore returns the store backed by <home>/skills-trust.json.
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

// Path returns the receipts file path.
func (s *TrustStore) Path() string { return s.path }

// read loads the receipts. A missing file is no receipts; a damaged one is an
// error, so it never reads as "nothing approved" and the next approval never
// writes over the receipts it held (the MCP store does the same).
func (s *TrustStore) read() (trustFile, error) {
	data, err := os.ReadFile(s.path) //nolint:gosec // path derives from the coddy home directory
	if err != nil {
		if os.IsNotExist(err) {
			return trustFile{Version: trustFileVersion, Workspaces: map[string][]TrustRecord{}}, nil
		}
		return trustFile{}, fmt.Errorf("read %s: %w", s.path, err)
	}
	var parsed trustFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return trustFile{}, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if parsed.Workspaces == nil {
		parsed.Workspaces = map[string][]TrustRecord{}
	}
	parsed.Version = trustFileVersion
	return parsed, nil
}

func (s *TrustStore) write(file trustFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Dir(s.path), filepath.Base(s.path), append(data, '\n'))
}

// Records returns the receipts recorded for a canonical workspace; none when
// the file cannot be read.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	trustMu.Lock()
	defer trustMu.Unlock()
	file, err := s.read()
	if err != nil {
		return nil
	}
	out := append([]TrustRecord(nil), file.Workspaces[workspace]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// Approved reports whether a receipt binds this workspace to the entry's
// digest.
func (s *TrustStore) Approved(workspace, digest string) bool {
	if s == nil || strings.TrimSpace(digest) == "" {
		return false
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	file, err := s.read()
	if err != nil {
		return false
	}
	for _, r := range file.Workspaces[workspace] {
		if r.Digest == digest {
			return true
		}
	}
	return false
}

// Approve records a receipt for d in workspace, replacing an earlier receipt
// for the same entry.
func (s *TrustStore) Approve(workspace string, d Declaration) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	file, err := s.read()
	if err != nil {
		return err
	}
	kept := make([]TrustRecord, 0, len(file.Workspaces[workspace])+1)
	for _, r := range file.Workspaces[workspace] {
		if !sameEntry(r.Kind, r.Name, r.Source, d) {
			kept = append(kept, r)
		}
	}
	kept = append(kept, TrustRecord{
		Kind: d.Kind, Name: d.Name, Source: d.Source,
		Digest:     declarationDigest(d.Kind, d.Name, d.Source),
		Path:       d.Path,
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	})
	file.Workspaces[workspace] = kept
	return s.write(file)
}

// Revoke removes the receipts of the entries key names in workspace (a
// marketplace by name, either kind by source) and reports whether one existed.
func (s *TrustStore) Revoke(workspace, key string) (bool, error) {
	return s.revokeWhere(workspace, func(r TrustRecord) bool {
		return keyNames(key, r.Kind, r.Name, r.Source)
	})
}

// RevokeEntries removes the receipts of exactly these entries in workspace
// (sameEntry), the way a removal from the project file takes the approvals of
// what it removed with it.
func (s *TrustStore) RevokeEntries(workspace string, entries []Declaration) (bool, error) {
	if len(entries) == 0 {
		return false, nil
	}
	return s.revokeWhere(workspace, func(r TrustRecord) bool {
		for _, d := range entries {
			if sameEntry(r.Kind, r.Name, r.Source, d) {
				return true
			}
		}
		return false
	})
}

// revokeWhere drops the receipts of workspace that match and reports whether
// any did.
func (s *TrustStore) revokeWhere(workspace string, match func(TrustRecord) bool) (bool, error) {
	trustMu.Lock()
	defer trustMu.Unlock()
	file, err := s.read()
	if err != nil {
		return false, err
	}
	recs := file.Workspaces[workspace]
	kept := make([]TrustRecord, 0, len(recs))
	removed := false
	for _, r := range recs {
		if match(r) {
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

// declarationDigest is what an approval binds to: the kind of the entry, the
// marketplace name and the source, so a checkout that rewrites the address an
// approved entry is read from asks again.
func declarationDigest(kind, name, source string) string {
	payload := struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}{Kind: kind, Name: strings.TrimSpace(name), Source: strings.TrimSpace(source)}
	data, err := json.Marshal(payload)
	if err != nil {
		return "sha256:unmarshalable"
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sameEntry reports whether a recorded or declared entry is d: the same kind
// and source, and for a marketplace the same name.
func sameEntry(kind, name, source string, d Declaration) bool {
	if kind != d.Kind || !sameSource(source, d.Source) {
		return false
	}
	return kind != KindMarketplace || strings.EqualFold(name, d.Name)
}

// keyNames reports whether key names an entry: a marketplace by its name, any
// entry by its source.
func keyNames(key, kind, name, source string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if kind == KindMarketplace && strings.EqualFold(name, key) {
		return true
	}
	return sameSource(source, key)
}
