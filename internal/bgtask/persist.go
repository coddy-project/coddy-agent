package bgtask

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Layout of a persisted task inside the session bundle:
//
//	<sessionDir>/background/<taskID>/meta.json
//	<sessionDir>/background/<taskID>/output.log
const (
	backgroundDirName = "background"
	metaFileName      = "meta.json"
	outputFileName    = "output.log"
)

// persistedSnapshot adds the process identity that is intentionally hidden from
// the public Snapshot JSON. It is an internal authorization token for acting on
// a pid, not part of the HTTP background-task surface.
type persistedSnapshot struct {
	Snapshot
	ProcessStartedAt *time.Time `json:"process_started_at,omitempty"`
}

func prepareTaskDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// persist writes the current snapshot next to the task's output log. Persistence
// is best effort: a session without a bundle on disk still works, it just has no
// record after the process exits.
func (p *Pool) persist(t *task) {
	if t.dir == "" {
		return
	}
	snap := t.Snapshot(p.now())
	data, err := marshalPersistedSnapshot(snap)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(t.dir, metaFileName), data, 0o644)
}

// maxPersistedTailBytes bounds how much of a task log is read back. A watcher
// left running for a day can produce a log far larger than anything a caller
// wants in memory, so the tail is what gets returned.
const maxPersistedTailBytes = 256 * 1024

// LoadPersisted reads the task records of a session bundle. Tasks recorded as
// still in flight are reported as orphaned, because the process that owned them
// is gone: the operator sees what ran and how far it got instead of a row that
// claims to be running forever.
//
// This is a pure read. Writing the correction back would mean every poll of the
// HTTP list rewrites the metadata of tasks that are still running in this
// process, and would race the supervisor's own write of the same file.
func LoadPersisted(sessionDir string) []Snapshot {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return nil
	}
	root := filepath.Join(sessionDir, backgroundDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	out := make([]Snapshot, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), metaFileName)
		data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the session bundle we own
		if err != nil {
			continue
		}
		snap, err := unmarshalPersistedSnapshot(data)
		if err != nil || snap.ID == "" {
			continue
		}
		if !snap.Status.Finished() {
			snap.Status = StatusOrphaned
		}
		out = append(out, snap)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// taskIDPrefix is how generated ids are spelled: bg_ followed by a counter.
const taskIDPrefix = "bg_"

// highestPersistedTaskNumber returns the largest generated task number the
// session bundle already holds, so a fresh process can keep counting instead of
// reusing ids that already have a log on disk.
func highestPersistedTaskNumber(sessionDir string) int {
	entries, err := os.ReadDir(filepath.Join(sessionDir, backgroundDirName))
	if err != nil {
		return 0
	}
	highest := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		suffix, ok := strings.CutPrefix(entry.Name(), taskIDPrefix)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		highest = max(highest, n)
	}
	return highest
}

// writePersistedSnapshot rewrites a task record in the bundle. Used when a
// survivor is reaped, so the next read does not offer to kill it again.
func writePersistedSnapshot(sessionDir string, snap Snapshot) {
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(snap.ID) == "" {
		return
	}
	data, err := marshalPersistedSnapshot(snap)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(sessionDir, backgroundDirName, snap.ID, metaFileName), data, 0o644)
}

func marshalPersistedSnapshot(snap Snapshot) ([]byte, error) {
	record := persistedSnapshot{Snapshot: snap}
	if !snap.ProcessStartedAt.IsZero() {
		startedAt := snap.ProcessStartedAt
		record.ProcessStartedAt = &startedAt
	}
	return json.MarshalIndent(record, "", "  ")
}

func unmarshalPersistedSnapshot(data []byte) (Snapshot, error) {
	var record persistedSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return Snapshot{}, err
	}
	if record.ProcessStartedAt != nil {
		record.Snapshot.ProcessStartedAt = *record.ProcessStartedAt
	}
	return record.Snapshot, nil
}

// PersistedOutput returns the captured log of a persisted task, capped at the
// last maxPersistedTailBytes so an unbounded log cannot be pulled into memory
// wholesale. truncated reports whether earlier output was skipped.
func PersistedOutput(sessionDir, taskID string) (text string, truncated bool, ok bool) {
	sessionDir = strings.TrimSpace(sessionDir)
	taskID = strings.TrimSpace(taskID)
	if sessionDir == "" || taskID == "" {
		return "", false, false
	}
	path := filepath.Join(sessionDir, backgroundDirName, taskID, outputFileName)
	f, err := os.Open(path) // #nosec G304 -- path is derived from the session bundle we own
	if err != nil {
		return "", false, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", false, false
	}

	size := info.Size()
	if size <= maxPersistedTailBytes {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", false, false
		}
		return decodeOutput(data), false, true
	}

	if _, err := f.Seek(size-maxPersistedTailBytes, io.SeekStart); err != nil {
		return "", false, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", false, false
	}
	return decodeOutput(data), true, true
}
