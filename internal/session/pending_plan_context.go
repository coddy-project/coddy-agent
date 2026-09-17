package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pendingPlanContextFileName = "pending_plan_context.json"

// PendingPlanContextRecord is the design plan hand-off of the turn in flight,
// kept in the bundle for as long as that turn can still be continued.
//
// A turn started from a saved plan can stop on a permission prompt and be
// answered minutes later, by which time the process may have been restarted and
// nothing is left in memory. What resumes is the same turn, and it renders its
// system prompt again, so the hand-off has to outlive the process the way the
// permission gate beside it does.
type PendingPlanContextRecord struct {
	Version int    `json:"version"`
	Text    string `json:"text"`
}

// WritePendingPlanContext stores the hand-off for a session bundle.
func WritePendingPlanContext(sessionDir, text string) error {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return fmt.Errorf("session directory is empty")
	}
	return writeJSONAtomic(filepath.Join(dir, pendingPlanContextFileName),
		PendingPlanContextRecord{Version: 1, Text: text})
}

// ReadPendingPlanContext returns the stored hand-off, or "" when there is none.
// A bundle written by an older version, or one somebody truncated, reads as
// absent rather than as an error: a missing hand-off costs the model context,
// never correctness, and is not worth refusing a resume over.
func ReadPendingPlanContext(sessionDir string) string {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, pendingPlanContextFileName))
	if err != nil {
		return ""
	}
	var rec PendingPlanContextRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return ""
	}
	return strings.TrimSpace(rec.Text)
}

// ClearPendingPlanContext removes the stored hand-off.
func ClearPendingPlanContext(sessionDir string) error {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(dir, pendingPlanContextFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
