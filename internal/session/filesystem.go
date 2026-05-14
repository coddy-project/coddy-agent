package session

import (
	"bytes"
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
	uiLogFile            = "ui_log.json"
	permissionGrantsFile = "permission_grants.json"
	todosDirName         = "todos"
	todosArchiveName     = "archive"
	activeTodoFile       = "active.md"
	toolCallsDirName     = "tool_calls"
	sessionFileLayout    = 1
	messagesLayout       = 1
	uiLogLayout          = 1
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

// HasPersistedSnapshot reports whether session.json exists for the id under Root.
func (f *FileStore) HasPersistedSnapshot(sessionID string) bool {
	if f == nil || f.Root == "" {
		return false
	}
	meta := filepath.Join(f.SessionPath(sessionID), sessionMetaFile)
	fi, err := os.Stat(meta)
	return err == nil && !fi.IsDir()
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
	if err := os.MkdirAll(filepath.Join(dir, toolCallsDirName), 0o755); err != nil {
		return "", err
	}
	metaPath := filepath.Join(dir, sessionMetaFile)
	if _, statErr := os.Stat(metaPath); os.IsNotExist(statErr) {
		m := SessionMeta{
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

// SessionMeta is persisted in session.json.
type SessionMeta struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CWD             string `json:"cwd"`
	Mode            string `json:"mode"`
	SelectedModelID string `json:"selectedModelId,omitempty"`
	AgentMemory     string `json:"agentMemory,omitempty"`
	Title           string `json:"title,omitempty"`
	TitlePinned     string `json:"titlePinned,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
	// Scheduler-run bundle (cron / manual scheduler); omitted for normal chats.
	SchedulerRun        bool   `json:"schedulerRun,omitempty"`
	SchedulerJobID      string `json:"schedulerJobId,omitempty"`
	SchedulerStartedAt  string `json:"schedulerStartedAt,omitempty"`
	SchedulerEndedAt    string `json:"schedulerEndedAt,omitempty"`
	SchedulerStopStatus string `json:"schedulerStopStatus,omitempty"`
	// ActivitySeq increments when an agent turn completes (multi-surface unread indicator).
	ActivitySeq uint64 `json:"activitySeq,omitempty"`
	// ReadActivitySeq tracks the last activity generation the user marked as read.
	ReadActivitySeq uint64 `json:"readActivitySeq,omitempty"`
}

// ExcludedFromComposerSessionList reports whether this session should not appear on default composer UI lists (GET /coddy/sessions).
func (m SessionMeta) ExcludedFromComposerSessionList(sessionFolderName string) bool {
	if m.SchedulerRun {
		return true
	}
	s := strings.TrimSpace(sessionFolderName)
	return strings.HasPrefix(s, "sched_")
}

type messagesFileData struct {
	Version  int           `json:"version"`
	Messages []llm.Message `json:"messages"`
}

type uiLogFileData struct {
	Version int          `json:"version"`
	Entries []UILogEntry `json:"entries"`
}

type permissionGrantsFileData struct {
	Version  int      `json:"version"`
	Commands []string `json:"commands,omitempty"`
	Writes   []string `json:"writes,omitempty"`
}

// LoadedSnapshot is session data read from disk (before MCP and skills are attached).
type LoadedSnapshot struct {
	Dir                 string
	Meta                SessionMeta
	Messages            []llm.Message
	UILog               []UILogEntry
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
	var meta SessionMeta
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

	var uiEntries []UILogEntry
	ulPath := filepath.Join(dir, uiLogFile)
	if b, readErr := os.ReadFile(ulPath); readErr == nil {
		var wrap uiLogFileData
		if jsonErr := json.Unmarshal(b, &wrap); jsonErr == nil {
			uiEntries = wrap.Entries
		}
	}

	var plan []acp.PlanEntry
	activePath := ActiveTodoPath(dir)
	if b, readErr := os.ReadFile(activePath); readErr == nil && strings.TrimSpace(string(b)) != "" {
		plan = todo.ParsePlanMarkdown(string(b))
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
		UILog:               uiEntries,
		Plan:                plan,
		PermissionCommands:  permCmds,
		PermissionWriteKeys: permWrites,
	}, nil
}

// ReadDiskActivity returns activitySeq and readActivitySeq from session.json only.
func (f *FileStore) ReadDiskActivity(sessionID string) (activitySeq, readActivitySeq uint64, err error) {
	if f == nil || f.Root == "" {
		return 0, 0, nil
	}
	metaPath := filepath.Join(f.SessionPath(sessionID), sessionMetaFile)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return 0, 0, err
	}
	var meta SessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return 0, 0, err
	}
	return meta.ActivitySeq, meta.ReadActivitySeq, nil
}

// SessionListEntry describes one row for CLI or session/list (subset of ACP SessionInfo).
type SessionListEntry struct {
	SessionID string
	CWD       string
	Title     string
	UpdatedAt string
}

// ListSnapshots scans Root for persisted sessions (requires session.json).
// When includeSchedulerRuns is false, sessions marked schedulerRun in session.json (or folder id prefix sched_) are omitted (default composer list).
func (f *FileStore) ListSnapshots(cwdFilter string, includeSchedulerRuns bool) ([]SessionListEntry, error) {
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
		if !includeSchedulerRuns && snap.Meta.ExcludedFromComposerSessionList(id) {
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
			if out[i].UpdatedAt != out[j].UpdatedAt {
				return out[i].UpdatedAt > out[j].UpdatedAt
			}
			return out[i].SessionID < out[j].SessionID
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out, nil
}

// FirstUserMessageContent returns trimmed content of the first message with role user
// in messages.json order (skipping system, assistant, tool, etc. before it).
func (f *FileStore) FirstUserMessageContent(sessionID string) (content string, found bool, err error) {
	if f == nil || sessionID == "" {
		return "", false, nil
	}
	msgPath := filepath.Join(f.SessionPath(sessionID), messagesFile)
	b, readErr := os.ReadFile(msgPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return "", false, nil
		}
		return "", false, readErr
	}
	var wrap messagesFileData
	if jsonErr := json.Unmarshal(b, &wrap); jsonErr != nil {
		return "", false, nil
	}
	for _, m := range wrap.Messages {
		if m.Role == llm.RoleUser {
			c := strings.TrimSpace(m.Content)
			return c, c != "", nil
		}
	}
	return "", false, nil
}

// FilterSnapshotListForSearch keeps sessions where title matches needle (case-insensitive substring)
// or the first role-user message content matches (title match checked first).
func (f *FileStore) FilterSnapshotListForSearch(entries []SessionListEntry, q string) ([]SessionListEntry, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return entries, nil
	}
	out := make([]SessionListEntry, 0)
	for _, row := range entries {
		title := strings.ToLower(strings.TrimSpace(row.Title))
		if strings.Contains(title, needle) {
			out = append(out, row)
			continue
		}
		content, _, err := f.FirstUserMessageContent(row.SessionID)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(content), needle) {
			out = append(out, row)
		}
	}
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
	title := persistedConversationTitle(state)
	metaPath := filepath.Join(dir, sessionMetaFile)
	msgPath := filepath.Join(dir, messagesFile)

	var prevMeta SessionMeta
	if prevMetaBytes, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(prevMetaBytes, &prevMeta)
	}

	newActivitySeq := state.GetActivitySeq()
	newReadSeq := state.GetReadActivitySeq()

	wrapPreview := messagesFileData{
		Version:  messagesLayout,
		Messages: msgs,
	}
	newMsgBytes, encErr := json.Marshal(wrapPreview)
	oldMsgBytes, oldMsgErr := os.ReadFile(msgPath)
	preserveUpdatedAt := encErr == nil && oldMsgErr == nil &&
		bytes.Equal(oldMsgBytes, newMsgBytes) &&
		newActivitySeq == prevMeta.ActivitySeq

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if preserveUpdatedAt && strings.TrimSpace(prevMeta.UpdatedAt) != "" {
		updatedAt = prevMeta.UpdatedAt
	}

	meta := SessionMeta{
		Version:         sessionFileLayout,
		ID:              state.ID,
		CWD:             state.CWD,
		Mode:            state.GetMode(),
		SelectedModelID: state.GetSelectedModelID(),
		AgentMemory:     state.GetAgentMemory(),
		Title:           title,
		TitlePinned:     strings.TrimSpace(state.GetTitlePinned()),
		UpdatedAt:       updatedAt,
	}
	if state.GetSchedulerRun() {
		meta.SchedulerRun = true
		meta.SchedulerJobID = strings.TrimSpace(state.GetSchedulerJobID())
		meta.SchedulerStartedAt = strings.TrimSpace(state.GetSchedulerStartedAt())
		meta.SchedulerEndedAt = strings.TrimSpace(state.GetSchedulerEndedAt())
		meta.SchedulerStopStatus = strings.TrimSpace(state.GetSchedulerStopStatus())
	}
	meta.ActivitySeq = newActivitySeq
	meta.ReadActivitySeq = newReadSeq
	if err := writeJSONAtomic(metaPath, meta); err != nil {
		return err
	}
	wrap := messagesFileData{
		Version:  messagesLayout,
		Messages: msgs,
	}
	if err := writeJSONAtomic(msgPath, wrap); err != nil {
		return err
	}
	uiWrap := uiLogFileData{
		Version: uiLogLayout,
		Entries: state.GetUILog(),
	}
	if err := writeJSONAtomic(filepath.Join(dir, uiLogFile), uiWrap); err != nil {
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

// PatchSessionMetaActivitySync writes only activitySeq and readActivitySeq into session.json,
// preserving updatedAt and all other meta fields. It does not write messages.json.
func (f *FileStore) PatchSessionMetaActivitySync(st *State) error {
	if f == nil || st == nil {
		return nil
	}
	dir := strings.TrimSpace(st.SessionDir)
	if dir == "" {
		return fmt.Errorf("session has no SessionDir")
	}
	path := filepath.Join(dir, sessionMetaFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var meta SessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return fmt.Errorf("session.json: %w", err)
	}
	meta.ActivitySeq = st.GetActivitySeq()
	meta.ReadActivitySeq = st.GetReadActivitySeq()
	return writeJSONAtomic(path, meta)
}

func deriveSessionTitle(s *State) string {
	for _, msg := range s.GetMessages() {
		if msg.Role == llm.RoleUser && strings.TrimSpace(msg.Content) != "" {
			return truncateRunes(strings.TrimSpace(msg.Content), 120)
		}
	}
	return ""
}

// persistedConversationTitle selects the snapshot title saved to session.json.
func persistedConversationTitle(s *State) string {
	if p := strings.TrimSpace(s.GetTitlePinned()); p != "" {
		return p
	}
	return deriveSessionTitle(s)
}

func truncateRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "..."
}

// writeBytesAtomic writes data to path using a unique temp file in the same directory
// and renames it into place. A fixed ".tmp" suffix races when multiple goroutines patch
// the same session (e.g. markActivityRead from parallel UI tabs).
func writeBytesAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, base+".tmp.")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeJSONAtomic(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeBytesAtomic(path, data)
}

// SyncActiveTodoFile writes todos/active.md from the current plan (empty file if no items).
func SyncActiveTodoFile(sessionDir string, plan []acp.PlanEntry) error {
	tdir := filepath.Join(sessionDir, todosDirName)
	if err := os.MkdirAll(filepath.Join(tdir, todosArchiveName), 0o755); err != nil {
		return err
	}
	path := filepath.Join(tdir, activeTodoFile)
	text := todo.FormatPlanMarkdown(plan)
	var data []byte
	if text != "" {
		data = []byte(text + "\n")
	}
	return writeBytesAtomic(path, data)
}

// WritePlanArchivedMarkdown saves markdown to todos/archive/plan_<unix_seconds>.md.
// If another file already uses the same basename (same-second collision), tries plan_<sec>_1.md, etc.
func WritePlanArchivedMarkdown(sessionDir, markdown string) (writtenPath string, err error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", fmt.Errorf("session directory is empty")
	}
	raw := strings.TrimSpace(markdown)
	archDir := filepath.Join(sessionDir, todosDirName, todosArchiveName)
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		return "", err
	}
	sec := time.Now().Unix()
	for i := range 4096 {
		var name string
		if i == 0 {
			name = fmt.Sprintf("plan_%d.md", sec)
		} else {
			name = fmt.Sprintf("plan_%d_%d.md", sec, i)
		}
		dest := filepath.Join(archDir, name)
		if _, statErr := os.Stat(dest); statErr == nil {
			continue
		}
		var data []byte
		if raw != "" {
			data = []byte(raw)
			if data[len(data)-1] != '\n' {
				data = append(data, '\n')
			}
		}
		if err := writeBytesAtomic(dest, data); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("could not allocate unique archive file under sec=%d", sec)
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
