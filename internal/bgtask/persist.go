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

// SessionTasks lists every task of a session: what this pool holds, and under it what
// the session bundle recorded for an earlier process, so a task that outlived or died
// with that process still appears (as orphaned) instead of vanishing after a restart.
// The pool wins where both know a task. Every surface that shows a session's tasks -
// the HTTP rows, the console - reads this one list.
func (p *Pool) SessionTasks(sessionID, sessionDir string) []Snapshot {
	live := p.List(sessionID)
	seen := make(map[string]bool, len(live))
	rows := make([]Snapshot, 0, len(live))
	for _, snap := range live {
		seen[snap.ID] = true
		rows = append(rows, snap)
	}
	for _, snap := range LoadPersisted(sessionDir) {
		if seen[snap.ID] {
			continue
		}
		snap.SessionID = sessionID
		rows = append(rows, snap)
	}
	return rows
}

// SessionTaskOutput reads the captured output of one task of a session, from the pool
// or, for a task of an earlier process, from the log the bundle kept. tailLines trims
// it to its last lines the way Pool.Output does; zero returns everything.
func (p *Pool) SessionTaskOutput(sessionID, sessionDir, taskID string, tailLines int) (string, Snapshot, error) {
	output, snap, err := p.Output(sessionID, taskID, tailLines)
	if err == nil {
		return output, snap, nil
	}
	for _, row := range LoadPersisted(sessionDir) {
		if row.ID != taskID {
			continue
		}
		row.SessionID = sessionID
		persisted, dropped, _ := PersistedOutput(sessionDir, taskID)
		row.OutputTruncated = row.OutputTruncated || dropped
		return TailLines(persisted, tailLines), row, nil
	}
	return "", Snapshot{}, ErrNotFound
}

// TailLines trims text to its last n lines, matching what the pool does for a live
// task so a recorded log answers in the same shape. n <= 0 returns the text whole.
func TailLines(text string, n int) string {
	if n <= 0 || text == "" {
		return text
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
