package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

const pendingPermissionFileName = "pending_permission.json"

// PendingPermissionRecord is persisted while the agent waits on POST /coddy/sessions/{id}/permission.
type PendingPermissionRecord struct {
	Version   int                          `json:"version"`
	SessionID string                       `json:"sessionId"`
	ToolCall  acp.PermissionToolCall       `json:"toolCall"`
	Options   []acp.PermissionOption       `json:"options"`
	ToolName  string                       `json:"toolName,omitempty"`
	ArgsJSON  string                       `json:"argsJson,omitempty"`
}

// WritePendingPermission stores an in-flight permission gate on disk (survives process restart).
func WritePendingPermission(sessionDir string, params acp.PermissionRequestParams, toolName, argsJSON string) error {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return fmt.Errorf("session directory is empty")
	}
	tcid := strings.TrimSpace(params.ToolCall.ToolCallID)
	if tcid == "" {
		return fmt.Errorf("toolCallId is empty")
	}
	rec := PendingPermissionRecord{
		Version:   1,
		SessionID: strings.TrimSpace(params.SessionID),
		ToolCall:  params.ToolCall,
		Options:   params.Options,
		ToolName:  strings.TrimSpace(toolName),
		ArgsJSON:  argsJSON,
	}
	return writeJSONAtomic(filepath.Join(dir, pendingPermissionFileName), rec)
}

// ReadPendingPermission loads the persisted permission gate for a session.
func ReadPendingPermission(sessionDir string) (*PendingPermissionRecord, error) {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return nil, fmt.Errorf("session directory is empty")
	}
	b, err := os.ReadFile(filepath.Join(dir, pendingPermissionFileName))
	if err != nil {
		return nil, err
	}
	var rec PendingPermissionRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rec.ToolCall.ToolCallID) == "" {
		return nil, fmt.Errorf("pending permission missing toolCallId")
	}
	return &rec, nil
}

// ClearPendingPermission removes the on-disk permission gate.
func ClearPendingPermission(sessionDir string) error {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(dir, pendingPermissionFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// PendingPermissionHeld reports whether the session directory has a persisted permission gate.
func PendingPermissionHeld(sessionDir string) bool {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, pendingPermissionFileName))
	return err == nil
}
