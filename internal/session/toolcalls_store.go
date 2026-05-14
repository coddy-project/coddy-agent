package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const toolCallMetaVersion = 1

type ToolCallMeta struct {
	Version    int    `json:"version"`
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

func toolCallDir(sessionDir, toolCallID string) (string, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", fmt.Errorf("session directory is empty")
	}
	id := strings.TrimSpace(toolCallID)
	if id == "" {
		return "", fmt.Errorf("toolCallId is empty")
	}
	return filepath.Join(sessionDir, toolCallsDirName, id), nil
}

func ensureToolCallDir(sessionDir, toolCallID string) (string, error) {
	dir, err := toolCallDir(sessionDir, toolCallID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteToolCallArgs(sessionDir, toolCallID, argsJSON string) error {
	dir, err := ensureToolCallDir(sessionDir, toolCallID)
	if err != nil {
		return err
	}
	var tmp any
	if err := json.Unmarshal([]byte(argsJSON), &tmp); err != nil {
		return writeTextAtomic(filepath.Join(dir, "args.json"), argsJSON)
	}
	return writeJSONAtomic(filepath.Join(dir, "args.json"), tmp)
}

func WriteToolCallResult(sessionDir, toolCallID, resultMarkdown string) error {
	dir, err := ensureToolCallDir(sessionDir, toolCallID)
	if err != nil {
		return err
	}
	return writeTextAtomic(filepath.Join(dir, "result.md"), resultMarkdown)
}

func WriteToolCallMeta(sessionDir, toolCallID string, meta ToolCallMeta) error {
	dir, err := ensureToolCallDir(sessionDir, toolCallID)
	if err != nil {
		return err
	}
	if meta.Version == 0 {
		meta.Version = toolCallMetaVersion
	}
	if meta.ToolCallID == "" {
		meta.ToolCallID = strings.TrimSpace(toolCallID)
	}
	return writeJSONAtomic(filepath.Join(dir, "meta.json"), meta)
}

func ReadToolCallMeta(sessionDir, toolCallID string) (*ToolCallMeta, error) {
	dir, err := toolCallDir(sessionDir, toolCallID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta ToolCallMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func ReadToolCallArgs(sessionDir, toolCallID string) (string, error) {
	dir, err := toolCallDir(sessionDir, toolCallID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "args.json"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ReadToolCallResult(sessionDir, toolCallID string) (string, error) {
	dir, err := toolCallDir(sessionDir, toolCallID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "result.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ListToolCalls(sessionDir string) ([]string, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("session directory is empty")
	}
	root := filepath.Join(sessionDir, toolCallsDirName)
	de, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(de))
	for _, e := range de {
		if !e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func MarkToolCallStarted(sessionDir, toolCallID, name, kind, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	meta := ToolCallMeta{
		Version:    toolCallMetaVersion,
		ToolCallID: strings.TrimSpace(toolCallID),
		Name:       strings.TrimSpace(name),
		Kind:       strings.TrimSpace(kind),
		Status:     strings.TrimSpace(status),
		StartedAt:  now,
	}
	return WriteToolCallMeta(sessionDir, toolCallID, meta)
}

func MarkToolCallFinished(sessionDir, toolCallID, name, kind, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	meta := ToolCallMeta{
		Version:    toolCallMetaVersion,
		ToolCallID: strings.TrimSpace(toolCallID),
		Name:       strings.TrimSpace(name),
		Kind:       strings.TrimSpace(kind),
		Status:     strings.TrimSpace(status),
		FinishedAt: now,
	}
	if prev, err := ReadToolCallMeta(sessionDir, toolCallID); err == nil && prev != nil {
		if prev.Version != 0 {
			meta.Version = prev.Version
		}
		if strings.TrimSpace(meta.Name) == "" {
			meta.Name = prev.Name
		}
		if strings.TrimSpace(meta.Kind) == "" {
			meta.Kind = prev.Kind
		}
		if strings.TrimSpace(prev.StartedAt) != "" {
			meta.StartedAt = prev.StartedAt
		}
	}
	return WriteToolCallMeta(sessionDir, toolCallID, meta)
}

func writeTextAtomic(path, text string) error {
	var data []byte
	if strings.TrimSpace(text) != "" {
		data = []byte(text)
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
	}
	return writeBytesAtomic(path, data)
}
