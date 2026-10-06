//go:build scheduler

package storage

// Workspace trust receipts for project scheduler jobs.
//
// A <workspace>/.coddy/scheduler/<id>.md is repository content: it names an
// instruction the daemon would run as an agent, with the operator's
// permissions, on a timer. Approvals are therefore recorded out of band, in
// the operator's own home, and bound to the canonical workspace, the job id
// and a digest of the file bytes, so an approved job that is later rewritten
// (a git pull, a hand edit) needs approving again. The store is a sibling of
// the MCP, subagent and hooks stores rather than a reuse of any of them: one
// kind of approval must never read as another.

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

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// TrustFileName is the receipts file inside the coddy home directory.
const TrustFileName = "scheduler-trust.json"

const trustFileVersion = 1

// TrustRecord is the receipt for one approved project job: its id, the
// digest the approval is bound to, and when it was granted.
type TrustRecord struct {
	JobID      string `json:"job_id"`
	Digest     string `json:"digest"`
	ApprovedAt string `json:"approved_at"`
}

type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists receipts at <home>/scheduler-trust.json. Every
// operation re-reads the file, so an approval granted over HTTP reaches the
// daemon on its next tick. Instances are cheap; a write is a transaction under
// an in-process mutex shared by every instance of the same path plus a file
// lock shared with other processes, and the file is replaced atomically
// through a unique temporary.
type TrustStore struct {
	path string
}

var trustLocks sync.Map

// NewTrustStore returns the store backed by <home>/scheduler-trust.json.
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

// Path returns the receipts file path.
func (s *TrustStore) Path() string { return s.path }

func (s *TrustStore) lock() *sync.Mutex {
	m, _ := trustLocks.LoadOrStore(s.path, &sync.Mutex{})
	return m.(*sync.Mutex)
}

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
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("scheduler trust store: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("scheduler trust store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, TrustFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("scheduler trust store: %w", err)
	}
	tmpPath := tmp.Name()
	fail := func(err error) error {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("scheduler trust store: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fail(err)
	}
	return nil
}

// transaction serialises a read-modify-write cycle against every other writer
// of the same file and returns the release function.
func (s *TrustStore) transaction() (func(), error) {
	mu := s.lock()
	mu.Lock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		mu.Unlock()
		return nil, fmt.Errorf("scheduler trust store: %w", err)
	}
	unlockFile, err := platform.LockFile(s.path + ".lock")
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		unlockFile()
		mu.Unlock()
	}, nil
}

// Workspaces lists every workspace that holds at least one receipt, sorted:
// the project roots the daemon scans besides its own.
func (s *TrustStore) Workspaces() []string {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	file := s.read()
	out := make([]string, 0, len(file.Workspaces))
	for ws, recs := range file.Workspaces {
		if strings.TrimSpace(ws) != "" && len(recs) > 0 {
			out = append(out, ws)
		}
	}
	sort.Strings(out)
	return out
}

// Records returns the receipts of a workspace, sorted by job id.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	out := append([]TrustRecord(nil), s.read().Workspaces[workspace]...)
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out
}

// Approved reports whether a receipt binds this workspace, job id and digest.
func (s *TrustStore) Approved(workspace, jobID, digest string) bool {
	if s == nil || workspace == "" || jobID == "" || digest == "" {
		return false
	}
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	for _, rec := range s.read().Workspaces[workspace] {
		if rec.JobID == jobID && rec.Digest == digest {
			return true
		}
	}
	return false
}

// Approve records a receipt for a job's digest, replacing an earlier receipt
// for the same id.
func (s *TrustStore) Approve(workspace, jobID, digest string) error {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(jobID) == "" || strings.TrimSpace(digest) == "" {
		return fmt.Errorf("scheduler trust store: workspace, job id and digest are required")
	}
	release, err := s.transaction()
	if err != nil {
		return err
	}
	defer release()
	file := s.read()
	kept := make([]TrustRecord, 0, len(file.Workspaces[workspace])+1)
	for _, rec := range file.Workspaces[workspace] {
		if rec.JobID != jobID {
			kept = append(kept, rec)
		}
	}
	kept = append(kept, TrustRecord{JobID: jobID, Digest: digest, ApprovedAt: time.Now().UTC().Format(time.RFC3339)})
	file.Workspaces[workspace] = kept
	return s.write(file)
}

// Revoke removes the receipt of a job and reports whether one was on file.
func (s *TrustStore) Revoke(workspace, jobID string) (bool, error) {
	release, err := s.transaction()
	if err != nil {
		return false, err
	}
	defer release()
	file := s.read()
	recs := file.Workspaces[workspace]
	kept := make([]TrustRecord, 0, len(recs))
	removed := false
	for _, rec := range recs {
		if rec.JobID == jobID {
			removed = true
			continue
		}
		kept = append(kept, rec)
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

// Rename moves the receipt of a job to its new id, keeping its digest; the
// caller re-issues it afterwards when the content changed too.
func (s *TrustStore) Rename(workspace, oldID, newID string) error {
	release, err := s.transaction()
	if err != nil {
		return err
	}
	defer release()
	file := s.read()
	recs := file.Workspaces[workspace]
	changed := false
	kept := make([]TrustRecord, 0, len(recs))
	for _, rec := range recs {
		switch rec.JobID {
		case newID:
			changed = true
			continue
		case oldID:
			rec.JobID = newID
			changed = true
		}
		kept = append(kept, rec)
	}
	if !changed {
		return nil
	}
	file.Workspaces[workspace] = kept
	return s.write(file)
}

// Digest is the digest a receipt binds: sha256 of the job file bytes.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
