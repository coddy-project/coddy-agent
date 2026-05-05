package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

const (
	sessionMetaFile      = "session.json"
	messagesFile         = "messages.json"
	permissionGrantsFile = "permission_grants.json"
	todosDirName         = "todos"
	todosArchiveName     = "archive"
	activeTodoFile       = "active.md"
	sessionFileLayout    = 1
	messagesLayout       = 1
	permissionGrantsVer  = 1
)

// FileStore persists session state under Root/<sessionId>/.
type FileStore struct {
	Root string
}

// SessionPath returns the directory for a session id.
func (f *FileStore) SessionPath(sessionID string) string {
	return filepath.Join(f.Root, sessionID)
}

// ActiveTodoPath is the markdown file for the current todo list.
func ActiveTodoPath(sessionDir string) string {
	return filepath.Join(sessionDir, todosDirName, activeTodoFile)
}

// AssetsPath is the per-session assets directory.
func AssetsPath(sessionDir string) string {
	return filepath.Join(sessionDir, "assets")
}

// EnsureLayout creates session.json (if missing), messages.json, assets/, todos/, todos/archive/.
func (f *FileStore) EnsureLayout(sessionID string) (dir string, err error) {
	dir = f.SessionPath(sessionID)
	if err := os.MkdirAll(filepath.Join(dir, todosDirName, todosArchiveName), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(AssetsPath(dir), 0o755); err != nil {
		return "", err
	}
	metaPath := filepath.Join(dir, sessionMetaFile)
	if _, statErr := os.Stat(metaPath); os.IsNotExist(statErr) {
		m := sessionMetaFileData{
			Version:   sessionFileLayout,
			ID:        sessionID,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if wErr := writeJSONAtomic(metaPath, m); wErr != nil {
			return "", wErr
		}
	}
	msgPath := filepath.Join(dir, messagesFile)
	if _, statErr := os.Stat(msgPath); os.IsNotExist(statErr) {
		wrap := messagesFileData{Version: messagesLayout, Messages: []llm.Message{}}
		if wErr := writeJSONAtomic(msgPath, wrap); wErr != nil {
			return "", wErr
		}
	}
	return dir, nil
}

// sessionMetaFileData is persisted in session.json.
type sessionMetaFileData struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CWD             string `json:"cwd"`
	Mode            string `json:"mode"`
	SelectedModelID string `json:"selectedModelId,omitempty"`
	AgentMemory     string `json:"agentMemory,omitempty"`
	Title           string `json:"title,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

type messagesFileData struct {
	Version  int           `json:"version"`
	Messages []llm.Message `json:"messages"`
}

type permissionGrantsFileData struct {
	Version  int      `json:"version"`
	Commands []string `json:"commands,omitempty"`
	Writes   []string `json:"writes,omitempty"`
}

// LoadedSnapshot is session data read from disk (before MCP and skills are attached).
type LoadedSnapshot struct {
	Dir                 string
	Meta                sessionMetaFileData
	Messages            []llm.Message
	Plan                []acp.PlanEntry
	PermissionCommands  []string
	PermissionWriteKeys []string
}

// ReadSnapshot loads session.json, messages.json, and todos/active.md if present.
func (f *FileStore) ReadSnapshot(sessionID string) (*LoadedSnapshot, error) {
	dir := f.SessionPath(sessionID)
	metaPath := filepath.Join(dir, sessionMetaFile)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found on disk: %s", sessionID)
		}
		return nil, err
	}
	var meta sessionMetaFileData
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, fmt.Errorf("session.json: %w", err)
	}
	var msgs []llm.Message
	msgPath := filepath.Join(dir, messagesFile)
	if b, readErr := os.ReadFile(msgPath); readErr == nil {
		var wrap messagesFileData
		if jsonErr := json.Unmarshal(b, &wrap); jsonErr == nil {
			msgs = wrap.Messages
		}
	}

	var plan []acp.PlanEntry
	activePath := ActiveTodoPath(dir)
	if b, readErr := os.ReadFile(activePath); readErr == nil && strings.TrimSpace(string(b)) != "" {
		plan = todo.ParseTodoMarkdown(string(b))
	}

	var permCmds, permWrites []string
	pgPath := filepath.Join(dir, permissionGrantsFile)
	if b, readErr := os.ReadFile(pgPath); readErr == nil {
		var pg permissionGrantsFileData
		if jsonErr := json.Unmarshal(b, &pg); jsonErr == nil {
			permCmds = append(permCmds, pg.Commands...)
			permWrites = append(permWrites, pg.Writes...)
		}
	}

	return &LoadedSnapshot{
		Dir:                 dir,
		Meta:                meta,
		Messages:            msgs,
		Plan:                plan,
		PermissionCommands:  permCmds,
		PermissionWriteKeys: permWrites,
	}, nil
}

// SessionListEntry describes one row for CLI or session/list (subset of ACP SessionInfo).
type SessionListEntry struct {
	SessionID string
	CWD       string
	Title     string
	UpdatedAt string
}

// ListSnapshots scans Root for persisted sessions (requires session.json).
func (f *FileStore) ListSnapshots(cwdFilter string) ([]SessionListEntry, error) {
	var out []SessionListEntry
	if f.Root == "" {
		return out, nil
	}
	de, err := os.ReadDir(f.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, ent := range de {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		id := ent.Name()
		snap, err := f.ReadSnapshot(id)
		if err != nil {
			continue
		}
		if cwdFilter != "" && snap.Meta.CWD != cwdFilter {
			continue
		}
		out = append(out, SessionListEntry{
			SessionID: snap.Meta.ID,
			CWD:       snap.Meta.CWD,
			Title:     snap.Meta.Title,
			UpdatedAt: snap.Meta.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, ei := time.Parse(time.RFC3339, out[i].UpdatedAt)
		tj, ej := time.Parse(time.RFC3339, out[j].UpdatedAt)
		if ei != nil || ej != nil {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return ti.After(tj)
	})
	return out, nil
}

// Save persists session state into the directory named by state.ID.
func (f *FileStore) Save(state *State) error {
	if f == nil || f.Root == "" || state == nil {
		return nil
	}
	dir := state.SessionDir
	if dir == "" {
		return fmt.Errorf("session has no SessionDir")
	}
	msgs := state.GetMessages()
	title := deriveSessionTitle(state)
	meta := sessionMetaFileData{
		Version:         sessionFileLayout,
		ID:              state.ID,
		CWD:             state.CWD,
		Mode:            state.GetMode(),
		SelectedModelID: state.GetSelectedModelID(),
		AgentMemory:     state.GetAgentMemory(),
		Title:           title,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSONAtomic(filepath.Join(dir, sessionMetaFile), meta); err != nil {
		return err
	}
	wrap := messagesFileData{
		Version:  messagesLayout,
		Messages: msgs,
	}
	if err := writeJSONAtomic(filepath.Join(dir, messagesFile), wrap); err != nil {
		return err
	}
	pg := permissionGrantsFileData{
		Version:  permissionGrantsVer,
		Commands: state.GetPermissionCommandGrants(),
		Writes:   state.GetPermissionWriteGrants(),
	}
	if err := writeJSONAtomic(filepath.Join(dir, permissionGrantsFile), pg); err != nil {
		return err
	}
	return SyncActiveTodoFile(dir, state.GetPlan())
}

func deriveSessionTitle(s *State) string {
	for _, msg := range s.GetMessages() {
		if msg.Role == llm.RoleUser && strings.TrimSpace(msg.Content) != "" {
			return truncateRunes(strings.TrimSpace(msg.Content), 120)
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "..."
}

func writeJSONAtomic(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SyncActiveTodoFile writes todos/active.md from the current plan (empty file if no items).
func SyncActiveTodoFile(sessionDir string, plan []acp.PlanEntry) error {
	tdir := filepath.Join(sessionDir, todosDirName)
	if err := os.MkdirAll(filepath.Join(tdir, todosArchiveName), 0o755); err != nil {
		return err
	}
	path := filepath.Join(tdir, activeTodoFile)
	text := todo.FormatTodoMarkdown(plan)
	var data []byte
	if text != "" {
		data = []byte(text + "\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ArchiveActiveTodo moves todos/active.md to todos/archive/todo-<nanos>.md if it has content.
func ArchiveActiveTodo(sessionDir string) error {
	ap := ActiveTodoPath(sessionDir)
	b, err := os.ReadFile(ap)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	archDir := filepath.Join(sessionDir, todosDirName, todosArchiveName)
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(archDir, fmt.Sprintf("todo-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return err
	}
	return os.Remove(ap)
}
