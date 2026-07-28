package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const statsFileVersion = 1

type TokenUsageTotals struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type TokenUsageTurn struct {
	TurnIndex    int    `json:"turnIndex"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	TotalTokens  int    `json:"totalTokens"`
	Timestamp    string `json:"timestamp"`
}

type SessionStats struct {
	Version          int               `json:"version"`
	UpdatedAt        string            `json:"updatedAt"`
	TokenUsageTotal  TokenUsageTotals  `json:"tokenUsageTotal"`
	TokenUsageByTurn []TokenUsageTurn  `json:"tokenUsageByTurn,omitempty"`
	ContextBreakdown *ContextBreakdown `json:"contextBreakdown,omitempty"`
}

func statsPath(sessionDir string) (string, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", fmt.Errorf("session directory is empty")
	}
	return filepath.Join(sessionDir, "stats.json"), nil
}

func ReadSessionStats(sessionDir string) (*SessionStats, error) {
	p, err := statsPath(sessionDir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var st SessionStats
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func WriteSessionStats(sessionDir string, st SessionStats) error {
	p, err := statsPath(sessionDir)
	if err != nil {
		return err
	}
	if st.Version == 0 {
		st.Version = statsFileVersion
	}
	if strings.TrimSpace(st.UpdatedAt) == "" {
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return writeJSONAtomic(p, st)
}

// WriteSessionContextBreakdown updates only the live context estimate while
// preserving the provider token counters already stored for the session.
func WriteSessionContextBreakdown(sessionDir string, b *ContextBreakdown) error {
	if b == nil {
		return nil
	}
	st, err := ReadSessionStats(sessionDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		st = &SessionStats{}
	}
	cp := *b
	st.ContextBreakdown = &cp
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return WriteSessionStats(sessionDir, *st)
}
