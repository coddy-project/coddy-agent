package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

// MessagesFileName is the transcript file inside a session bundle; hooks
// receive its path as transcript_path.
const MessagesFileName = "messages.json"

const (
	sessionMetaFile      = "session.json"
	messagesFile         = MessagesFileName
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

// FileStore persists session state under Root/<sessionId>/. A session spawned
// by another one lives inside its parent's bundle instead, under
// ChildSessionsDirName; SessionPath is what resolves either shape.
//
// Always use *FileStore, never a copy: the embedded mutex must not be copied.
type FileStore struct {
	Root string

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	msgCacheMu sync.Mutex
	msgCache   map[string]*persistedMessages

	// childMu guards the index of nested bundles: the id of every session
	// found inside another one, mapped to the directory it lives in.
	// childScanned is when the sessions root was last walked to fill it, and
	// childScanMu admits one walker at a time.
	childMu      sync.RWMutex
	childDirs    map[string]string
	childScanned time.Time
	childScanMu  sync.Mutex
}

// persistedMessages remembers what was last written to one messages.json. A
// save compares the state's message revisions against it to tell "nothing
// moved" from "the tail grew", which is what keeps the cost of persisting a
// session proportional to the change rather than to the whole conversation.
type persistedMessages struct {
	// owner is the State this was written from: revisions start at zero in
	// every State, so without it a reopened session could reach a revision its
	// predecessor already wrote and be mistaken for unchanged.
	owner   uint64
	rev     uint64
	editRev uint64
	count   int
	bytes   []byte

	// size and modTime are what the file looked like once written. They are
	// checked before a save decides it need not write: another process over
	// the same bundle replaces the file without this store hearing about it,
	// and existence alone would not notice.
	size    int64
	modTime time.Time
}

// matchesFile reports whether path still holds what this entry recorded.
func (p *persistedMessages) matchesFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.Size() == p.size && st.ModTime().Equal(p.modTime)
}

func (f *FileStore) cachedMessages(path string) *persistedMessages {
	f.msgCacheMu.Lock()
	defer f.msgCacheMu.Unlock()
	return f.msgCache[path]
}

func (f *FileStore) rememberMessages(path string, p *persistedMessages) {
	f.msgCacheMu.Lock()
	defer f.msgCacheMu.Unlock()
	if f.msgCache == nil {
		f.msgCache = make(map[string]*persistedMessages)
	}
	f.msgCache[path] = p
}

// messagesFileTail is how an encoded history with at least one message ends.
// Splicing replaces it with the new entries and puts it back.
const messagesFileTail = "\n  ]\n}\n"

// encodeMessagesFile renders messages.json. When the previous content is known
// and the history only grew at the end, the new entries are appended to those
// bytes instead of encoding every earlier message again. The result is
// byte-for-byte what encoding the whole history would have produced, which
// TestPersistedHistoryMatchesAFullEncoding holds it to.
//
// The second return value is how many messages had to be encoded: the whole
// history when it could not build on the previous content, and only the new
// tail when it could. Nothing in the store reads it, but it is what makes the
// difference between the two paths observable to a test - encoding a long
// history is slow rather than allocation-heavy, so the work cannot be measured
// any other way without timing.
func encodeMessagesFile(msgs []llm.Message, base *persistedMessages) ([]byte, int, error) {
	if base != nil && base.count > 0 && len(msgs) > base.count {
		if out, ok := spliceMessages(base.bytes, msgs[base.count:]); ok {
			return out, len(msgs) - base.count, nil
		}
	}
	data, err := json.MarshalIndent(messagesFileData{Version: messagesLayout, Messages: msgs}, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	return append(data, '\n'), len(msgs), nil
}

// spliceMessages appends added to an already encoded history. It reports false
// when prev is not shaped as expected, so the caller falls back to encoding the
// whole history rather than writing something malformed.
func spliceMessages(prev []byte, added []llm.Message) ([]byte, bool) {
	if !bytes.HasSuffix(prev, []byte(messagesFileTail)) {
		return nil, false
	}
	out := make([]byte, 0, len(prev)+len(added)*1024)
	out = append(out, prev[:len(prev)-len(messagesFileTail)]...)
	for i := range added {
		frag, err := json.MarshalIndent(added[i], "    ", "  ")
		if err != nil {
			return nil, false
		}
		out = append(out, ",\n    "...)
		out = append(out, frag...)
	}
	return append(out, messagesFileTail...), true
}

// pathMutex returns a per-path mutex, creating it on first use. Holding it
// across a read-modify-write cycle serializes concurrent writers to the same
// file, preventing lost updates and avoiding the atomic rename racing another
// open handle on the destination (which Windows rejects with "Access is
// denied" / "sharing violation").
func (f *FileStore) pathMutex(path string) *sync.Mutex {
	f.locksMu.Lock()
	defer f.locksMu.Unlock()
	if f.locks == nil {
		f.locks = make(map[string]*sync.Mutex)
	}
	m, ok := f.locks[path]
	if !ok {
		m = &sync.Mutex{}
		f.locks[path] = m
	}
	return m
}

// SessionPath returns the directory for a session id: Root/<id> for a session
// somebody started themselves, and the nested bundle under its parent for one
// a spawn_agent call started.
//
// The id must already have passed ValidateFolderSessionID - every entry point
// that takes one from a client runs it (the HTTP routes, EnsureHTTPSession,
// session/load, session/new with a preferred id) - because the path is built
// by joining, and joining only cleans a traversal, it does not refuse one. A bundle in the sessions root wins over a nested
// one of the same name, which only a collision of two random ids could produce.
// An id nothing has stored yet resolves to the sessions root, which is where a
// fresh session belongs.
func (f *FileStore) SessionPath(sessionID string) string {
	return f.sessionPath(sessionID, false)
}

// sessionPath resolves an id; forceScan skips the rate limit on the walk that
// looks for a nested bundle, which the write path uses so a bundle another
// process wrote is never answered with a second, top-level path for one id.
func (f *FileStore) sessionPath(sessionID string, forceScan bool) string {
	if f == nil {
		return ""
	}
	if dir, ok := f.lookupChildDir(sessionID); ok {
		return dir
	}
	top := filepath.Join(f.Root, sessionID)
	if isBundleDir(top) {
		return top
	}
	if dir, ok := f.resolveChildDir(sessionID, forceScan); ok {
		return dir
	}
	return top
}

// isBundleDir reports whether dir holds a session.json, which is what makes a
// directory a session bundle rather than an ordinary folder.
func isBundleDir(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, sessionMetaFile))
	return err == nil && !fi.IsDir()
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

// ResolveSessionID returns the stored session whose folder id is idOrPrefix,
// or the single stored session whose id starts with it. Folders without a
// session.json are not sessions.
func (f *FileStore) ResolveSessionID(idOrPrefix string) (string, error) {
	q := strings.TrimSpace(idOrPrefix)
	if q == "" {
		return "", fmt.Errorf("session id is empty")
	}
	if f == nil || f.Root == "" {
		return "", fmt.Errorf("session store not available")
	}
	if err := ValidateFolderSessionID(q); err != nil {
		return "", err
	}
	if f.HasPersistedSnapshot(q) {
		return q, nil
	}
	de, err := os.ReadDir(f.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("session not found: %s", q)
		}
		return "", err
	}
	var matches []string
	for _, ent := range de {
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), q) || !f.HasPersistedSnapshot(ent.Name()) {
			continue
		}
		matches = append(matches, ent.Name())
	}
	// A session spawned by another one is stored inside its parent's bundle,
	// so the top-level scan never sees it. It is still a session an operator
	// can name, so the nested bundles join the match set.
	for id, dir := range f.scanChildDirs() {
		if !strings.HasPrefix(id, q) || !isBundleDir(dir) {
			continue
		}
		f.rememberChildDir(id, dir)
		matches = append(matches, id)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session not found: %s", q)
	case 1:
		return matches[0], nil
	}
	shown := matches
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return "", fmt.Errorf("session id prefix %q is ambiguous (%d matches: %s)", q, len(matches), strings.Join(shown, ", "))
}

// ActiveTodoPath is the markdown file for the current todo list.
func ActiveTodoPath(sessionDir string) string {
	return filepath.Join(sessionDir, todosDirName, activeTodoFile)
}

// AssetsPath is the per-session assets directory.
func AssetsPath(sessionDir string) string {
	return filepath.Join(sessionDir, "assets")
}

// AssetThumbnailsPath is the directory containing bounded PNG previews for
// image assets. Thumbnail files are UI-only and are not exposed to the agent.
func AssetThumbnailsPath(sessionDir string) string {
	return filepath.Join(AssetsPath(sessionDir), "thumbnails")
}

// AssetThumbnailPath returns the preview path corresponding to one saved asset.
func AssetThumbnailPath(sessionDir, assetName string) string {
	return filepath.Join(AssetThumbnailsPath(sessionDir), assetName+".png")
}

// EnsureLayout creates session.json (if missing), messages.json, assets/, todos/, todos/archive/.
func (f *FileStore) EnsureLayout(sessionID string) (dir string, err error) {
	// A fresh walk, not the rate-limited one: this call is about to write a
	// bundle, and answering from a stale index would put a second bundle in
	// the sessions root for an id another process has already stored inside a
	// parent. It runs once per session, not once per request.
	dir = f.sessionPath(sessionID, true)
	return dir, f.ensureLayoutAt(sessionID, dir)
}

// ensureLayoutAt builds a bundle at an explicit directory, which is what lets
// a session spawned by another one be created inside its parent's bundle.
func (f *FileStore) ensureLayoutAt(sessionID, dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, todosDirName, todosArchiveName), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(AssetsPath(dir), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(AssetThumbnailsPath(dir), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, toolCallsDirName), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "plans"), 0o755); err != nil {
		return err
	}
	metaPath := filepath.Join(dir, sessionMetaFile)
	if _, statErr := os.Stat(metaPath); os.IsNotExist(statErr) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		m := SessionMeta{
			Version:   sessionFileLayout,
			ID:        sessionID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if wErr := writeJSONAtomic(metaPath, m); wErr != nil {
			return wErr
		}
	}
	msgPath := filepath.Join(dir, messagesFile)
	if _, statErr := os.Stat(msgPath); os.IsNotExist(statErr) {
		wrap := messagesFileData{Version: messagesLayout, Messages: []llm.Message{}}
		if wErr := writeJSONAtomic(msgPath, wrap); wErr != nil {
			return wErr
		}
	}
	return nil
}

// SessionMeta is persisted in session.json.
type SessionMeta struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	CWD               string `json:"cwd"`
	Mode              string `json:"mode"`
	SelectedModelID   string `json:"selectedModelId,omitempty"`
	SelectedReasoning string `json:"selectedReasoning,omitempty"`
	AgentMemory       string `json:"agentMemory,omitempty"`
	// HookContext is what SessionStart hooks handed to the session.
	HookContext string `json:"hookContext,omitempty"`
	Title       string `json:"title,omitempty"`
	TitlePinned string `json:"titlePinned,omitempty"`
	// Tags are the flat labels the listing filters and groups by, normalized
	// by NormalizeTags before they reach this struct.
	Tags []string `json:"tags,omitempty"`
	// Archived takes a session out of the working list without taking it off
	// disk; ArchivedAt records when that happened.
	Archived   bool   `json:"archived,omitempty"`
	ArchivedAt string `json:"archivedAt,omitempty"`
	// Origin names the surface that started the session: empty for one a
	// person opened on this host, "gateway:<messenger>" for a conversation a
	// messenger gateway is holding.
	Origin    string `json:"origin,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	// CreatedAt is stamped when the bundle directory is first written and never
	// moves again. A bundle stored before this field existed carries none: the
	// moment it was started is not recoverable, so it stays empty rather than
	// being invented from a later write.
	CreatedAt string `json:"createdAt,omitempty"`
	// Scheduler-run bundle (cron / manual scheduler); omitted for normal chats.
	SchedulerRun        bool   `json:"schedulerRun,omitempty"`
	SchedulerJobID      string `json:"schedulerJobId,omitempty"`
	SchedulerStartedAt  string `json:"schedulerStartedAt,omitempty"`
	SchedulerEndedAt    string `json:"schedulerEndedAt,omitempty"`
	SchedulerStopStatus string `json:"schedulerStopStatus,omitempty"`
	// Subagent-run bundle: a child session spawned by another session's
	// spawn_agent call; omitted for normal chats. The pool task that represents
	// the run lives under ParentSessionID.
	SubagentRun     bool   `json:"subagentRun,omitempty"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	SubagentName    string `json:"subagentName,omitempty"`
	SubagentTaskID  string `json:"subagentTaskId,omitempty"`
	SubagentDepth   int    `json:"subagentDepth,omitempty"`
	// ActivitySeq increments when an agent turn completes (multi-surface unread indicator).
	ActivitySeq uint64 `json:"activitySeq,omitempty"`
	// ReadActivitySeq tracks the last activity generation the user marked as read.
	ReadActivitySeq uint64 `json:"readActivitySeq,omitempty"`
	// PermissionMode is the session-level override for tools.permission_mode.
	PermissionMode string `json:"permissionMode,omitempty"`
}

// ExcludedFromComposerSessionList reports whether this session should not appear on default composer UI lists (GET /coddy/sessions).
func (m SessionMeta) ExcludedFromComposerSessionList(sessionFolderName string) bool {
	if m.SchedulerRun {
		return true
	}
	s := strings.TrimSpace(sessionFolderName)
	return strings.HasPrefix(s, "sched_")
}

// IsSubagentRun reports whether this bundle is a session spawned by another
// session. Nothing about the id says so - every session id is minted the same
// way - so the bundle's own metadata is the answer, written before the child
// ever runs.
func (m SessionMeta) IsSubagentRun() bool {
	return m.SubagentRun
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
	return f.readSnapshotAt(f.SessionPath(sessionID), sessionID)
}

// readSnapshotAt reads a bundle from an explicit directory, which is how a
// listing walk reads nested bundles it already has the path of.
//
// Where the bundle sits is the second line of defence next to its metadata: a
// bundle inside another session's ChildSessionsDirName is a spawned run even
// when its session.json does not say so yet, because only a spawn writes one
// there. Without that, a bundle whose first save never landed - the process
// died between the layout and the save - would read back as an ordinary,
// writable session hidden inside its parent.
func (f *FileStore) readSnapshotAt(dir, sessionID string) (*LoadedSnapshot, error) {
	metaPath := filepath.Join(dir, sessionMetaFile)
	metaBytes, err := readFileWithRetry(metaPath)
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
	if b, readErr := readFileWithRetry(msgPath); readErr == nil {
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

	if parent, ok := f.childBundleParent(dir); ok {
		meta.SubagentRun = true
		if strings.TrimSpace(meta.ParentSessionID) == "" {
			meta.ParentSessionID = parent
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
	// CreatedAt is empty for a bundle stored before the field existed.
	CreatedAt string
	// Model is the session's own backend override (session.json
	// selectedModelId); empty when the session ran on the configured default.
	Model string
	// Tags are the session's labels, already normalized on the way to disk.
	Tags []string
	// Archived marks a session the operator put aside; ArchivedAt says when.
	Archived   bool
	ArchivedAt string
	// Origin is the surface that started the session (see SessionMeta.Origin).
	Origin string
	// MessageCount counts the persisted transcript rows of every role. The
	// snapshot behind this listing is already parsed, so it costs no extra read.
	MessageCount int
	// SubagentRun marks a session another session spawned, and the three
	// fields below name the run. They come off the same snapshot the row was
	// built from, so a client that shows children pays no second read.
	SubagentRun     bool
	ParentSessionID string
	SubagentName    string
	SubagentTaskID  string
}

// ListOptions selects which persisted sessions ListSnapshotsWith returns.
type ListOptions struct {
	// CWD keeps only sessions saved with this working directory when non-empty.
	CWD string
	// IncludeSchedulerRuns adds bundles created by scheduler runs (sched_ ids).
	IncludeSchedulerRuns bool
	// IncludeSubagents descends into the sessions spawned by spawn_agent,
	// which are stored inside the bundle of the session that spawned them.
	IncludeSubagents bool
	// Archived selects which side of the archive is listed. The zero value is
	// ArchiveExclude, so every caller written before the archive existed keeps
	// getting the working list.
	Archived ArchiveFilter
	// Tags keeps the sessions carrying any of these labels when non-empty.
	Tags []string
	// Origin selects sessions by the surface that started them; the zero value
	// is every surface.
	Origin OriginFilter
}

// ListSnapshots scans Root for persisted sessions (requires session.json).
// When includeSchedulerRuns is false, sessions marked schedulerRun in session.json (or folder id prefix sched_) are omitted (default composer list).
// Subagent child sessions are always omitted here; use ListSnapshotsWith to include them.
// A non-empty cwdFilter keeps the sessions of that folder, matched with
// SameWorkspacePath rather than by the stored string.
func (f *FileStore) ListSnapshots(cwdFilter string, includeSchedulerRuns bool) ([]SessionListEntry, error) {
	return f.ListSnapshotsWith(ListOptions{CWD: cwdFilter, IncludeSchedulerRuns: includeSchedulerRuns})
}

// ListSnapshotsWith scans Root for persisted sessions matching opts.
func (f *FileStore) ListSnapshotsWith(opts ListOptions) ([]SessionListEntry, error) {
	// The filter names a folder, not a string: a session stored under one
	// spelling of a workspace (a symlink, a trailing separator, the case of a
	// Windows drive letter) is listed for any other spelling of it.
	cwdFilter := CanonicalWorkspacePath(opts.CWD)
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
		dir := filepath.Join(f.Root, ent.Name())
		out = f.appendBundleRow(out, dir, ent.Name(), cwdFilter, opts)
		if opts.IncludeSubagents {
			out = f.appendChildRows(out, dir, cwdFilter, opts, 0)
		}
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

// appendBundleRow reads one bundle and adds its row when opts admit it.
func (f *FileStore) appendBundleRow(out []SessionListEntry, dir, id, cwdFilter string, opts ListOptions) []SessionListEntry {
	snap, err := f.readSnapshotAt(dir, id)
	if err != nil {
		return out
	}
	if !opts.IncludeSchedulerRuns && snap.Meta.ExcludedFromComposerSessionList(id) {
		return out
	}
	if !opts.IncludeSubagents && snap.Meta.IsSubagentRun() {
		return out
	}
	if cwdFilter != "" && !matchesWorkspace(cwdFilter, snap.Meta.CWD) {
		return out
	}
	if !opts.Archived.Keeps(snap.Meta.Archived) {
		return out
	}
	if !opts.Origin.Keeps(snap.Meta.Origin) {
		return out
	}
	row := SessionListEntry{
		SessionID:       snap.Meta.ID,
		CWD:             snap.Meta.CWD,
		Title:           snap.Meta.Title,
		UpdatedAt:       snap.Meta.UpdatedAt,
		CreatedAt:       snap.Meta.CreatedAt,
		Model:           snap.Meta.SelectedModelID,
		Tags:            NormalizeTags(snap.Meta.Tags),
		Archived:        snap.Meta.Archived,
		ArchivedAt:      snap.Meta.ArchivedAt,
		Origin:          snap.Meta.Origin,
		MessageCount:    len(snap.Messages),
		SubagentRun:     snap.Meta.SubagentRun,
		ParentSessionID: snap.Meta.ParentSessionID,
		SubagentName:    snap.Meta.SubagentName,
		SubagentTaskID:  snap.Meta.SubagentTaskID,
	}
	if !SessionMatchesAnyTag(row, opts.Tags) {
		return out
	}
	return append(out, row)
}

// appendChildRows adds the sessions nested inside dir, and their own children,
// depth first.
func (f *FileStore) appendChildRows(out []SessionListEntry, dir, cwdFilter string, opts ListOptions, depth int) []SessionListEntry {
	if depth >= maxChildNesting {
		return out
	}
	for _, child := range f.childBundleDirs(dir) {
		out = f.appendBundleRow(out, child, filepath.Base(child), cwdFilter, opts)
		out = f.appendChildRows(out, child, cwdFilter, opts, depth+1)
	}
	return out
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

// matchesAnyTagSubstring reports whether one of the session's tags contains the
// (already lower-cased) needle. Tags are stored normalized, so no folding here.
func matchesAnyTagSubstring(tags []string, needle string) bool {
	for _, tag := range tags {
		if strings.Contains(tag, needle) {
			return true
		}
	}
	return false
}

// FilterSnapshotListForSearch keeps sessions where the title, the working directory, a tag,
// or the first role-user message content matches needle (case-insensitive substring), checked
// in that order because the first three are already in hand and the last needs a file read.
//
// The working directory is part of the search because it is often how someone remembers a
// session: not by what they called it, but by which checkout it was about.
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
		if cwd := strings.ToLower(strings.TrimSpace(row.CWD)); cwd != "" && strings.Contains(cwd, needle) {
			out = append(out, row)
			continue
		}
		if matchesAnyTagSubstring(row.Tags, needle) {
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
	metaPath := filepath.Join(dir, sessionMetaFile)
	msgPath := filepath.Join(dir, messagesFile)

	// The lock comes before the snapshot, not after. Two saves of one session
	// overlap, and a snapshot taken outside it can be written after a newer one
	// has already landed - putting an older history on disk and leaving the
	// cache describing it, which the next append would then splice onto.
	msgMu := f.pathMutex(msgPath)
	msgMu.Lock()
	defer msgMu.Unlock()

	msgs, msgRev, msgEditRev, stateID := state.MessagesForPersist()
	title := conversationTitle(state, msgs)

	var prevMeta SessionMeta
	metaExisted := false
	if prevMetaBytes, err := os.ReadFile(metaPath); err == nil {
		metaExisted = true
		_ = json.Unmarshal(prevMetaBytes, &prevMeta)
	}

	newActivitySeq := state.GetActivitySeq()
	newReadSeq := state.GetReadActivitySeq()

	// What was last written is remembered rather than read back and compared:
	// the old code encoded the history a second time only to diff it against
	// the file, and compared a compact encoding with an indented one, so the
	// answer was always "changed" and updatedAt moved on every save.
	//
	// An entry is only worth anything while it belongs to this State and the
	// file still holds what this store put there; another writer over the same
	// bundle replaces it without this store hearing about it.
	cached := f.cachedMessages(msgPath)
	usable := cached != nil && cached.owner == stateID && cached.matchesFile(msgPath)

	var pending []byte
	messagesUnchanged := usable && cached.rev == msgRev
	if !messagesUnchanged {
		// Only a history that grew purely at the end can be spliced onto what
		// was written last; an edit or a replacement re-encodes everything.
		var base *persistedMessages
		if usable && cached.editRev == msgEditRev {
			base = cached
		}
		data, _, err := encodeMessagesFile(msgs, base)
		if err != nil {
			return err
		}
		// A revision moves on any edit, including one that changed nothing in
		// the end. Comparing what was encoded against what is already there
		// costs a memcmp on a path that has just paid for the encoding, and it
		// is what makes updatedAt follow the content rather than the bookkeeping.
		if usable && bytes.Equal(data, cached.bytes) {
			messagesUnchanged = true
		} else {
			pending = data
		}
	}
	archived, archivedAt := state.ArchiveState()
	meta := SessionMeta{
		Version:           sessionFileLayout,
		ID:                state.ID,
		CWD:               state.CWD,
		Mode:              state.GetMode(),
		SelectedModelID:   state.GetSelectedModelID(),
		SelectedReasoning: state.GetSelectedReasoning(),
		HookContext:       state.GetHookContext(),
		AgentMemory:       state.GetAgentMemory(),
		Title:             title,
		TitlePinned:       strings.TrimSpace(state.GetTitlePinned()),
		Tags:              state.GetTags(),
		Archived:          archived,
		ArchivedAt:        strings.TrimSpace(archivedAt),
		Origin:            strings.TrimSpace(state.GetOrigin()),
	}
	if state.GetSchedulerRun() {
		meta.SchedulerRun = true
		meta.SchedulerJobID = strings.TrimSpace(state.GetSchedulerJobID())
		meta.SchedulerStartedAt = strings.TrimSpace(state.GetSchedulerStartedAt())
		meta.SchedulerEndedAt = strings.TrimSpace(state.GetSchedulerEndedAt())
		meta.SchedulerStopStatus = strings.TrimSpace(state.GetSchedulerStopStatus())
	}
	if sub := state.Subagent(); sub != nil {
		meta.SubagentRun = true
		meta.ParentSessionID = strings.TrimSpace(sub.ParentSessionID)
		meta.SubagentName = strings.TrimSpace(sub.Name)
		meta.SubagentTaskID = strings.TrimSpace(sub.TaskID)
		meta.SubagentDepth = sub.Depth
	}
	meta.ActivitySeq = newActivitySeq
	meta.ReadActivitySeq = newReadSeq
	meta.PermissionMode = state.GetPermissionMode()

	// The stamp stands only when this save puts nothing new anywhere - not the
	// history, and not a field of the meta either. Pinning a title, tagging or
	// archiving is something persisted, and docs/features/sessions.md promises
	// the listing follows it. The tag slice makes SessionMeta uncomparable with
	// ==, so the two are compared field by field: a reflective compare is
	// nothing next to the encoding this save already paid for, and it cannot be
	// left stale by a field somebody adds later.
	sameMeta := meta
	sameMeta.UpdatedAt, sameMeta.CreatedAt = prevMeta.UpdatedAt, prevMeta.CreatedAt
	preserveUpdatedAt := messagesUnchanged && metaExisted && reflect.DeepEqual(sameMeta, prevMeta)

	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if preserveUpdatedAt && strings.TrimSpace(prevMeta.UpdatedAt) != "" {
		updatedAt = prevMeta.UpdatedAt
	}
	// The creation stamp is written once and then carried forward. A bundle that
	// has a session.json but no createdAt was stored by an older build: leave it
	// empty rather than backdating it to this save.
	createdAt := strings.TrimSpace(prevMeta.CreatedAt)
	if createdAt == "" && !metaExisted {
		createdAt = updatedAt
	}
	meta.UpdatedAt, meta.CreatedAt = updatedAt, createdAt

	if err := writeJSONAtomic(metaPath, meta); err != nil {
		return err
	}
	switch {
	case pending != nil:
		if err := writeBytesAtomic(msgPath, pending); err != nil {
			return err
		}
		entry := &persistedMessages{owner: stateID, rev: msgRev, editRev: msgEditRev, count: len(msgs), bytes: pending}
		if st, err := os.Stat(msgPath); err == nil {
			entry.size, entry.modTime = st.Size(), st.ModTime()
		}
		f.rememberMessages(msgPath, entry)
	case usable && (cached.rev != msgRev || cached.editRev != msgEditRev):
		// The history was touched but came out identical. Carry the revisions
		// forward so the next append can still build on these bytes instead of
		// encoding the conversation again.
		moved := *cached
		moved.rev, moved.editRev, moved.count = msgRev, msgEditRev, len(msgs)
		f.rememberMessages(msgPath, &moved)
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

	// Serialize the whole read-modify-write against concurrent patchers of the
	// same file (e.g. markActivityRead from parallel UI tabs).
	mu := f.pathMutex(path)
	mu.Lock()
	defer mu.Unlock()

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
	return titleFromMessages(s.GetMessages())
}

// titleFromMessages derives the title from a history the caller already holds,
// so a save does not copy the whole conversation a second time just to read
// its first line.
func titleFromMessages(msgs []llm.Message) string {
	for _, msg := range msgs {
		if msg.Role == llm.RoleUser && strings.TrimSpace(msg.Content) != "" {
			text := stripCoddySessionAssetsXML(strings.TrimSpace(msg.Content))
			// A hydrated @mention turn also carries <coddy_attachment> file bodies;
			// a title is the user's own text, never the attachment XML.
			text = stripCoddyAttachmentXML(text)
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			return truncateRunes(text, 120)
		}
	}
	return ""
}

// stripCoddySessionAssetsXML removes <coddy_session_assets>...</coddy_session_assets> blocks from s.
func stripCoddySessionAssetsXML(s string) string {
	const open = "<coddy_session_assets>"
	const close = "</coddy_session_assets>"
	for {
		start := strings.Index(strings.ToLower(s), strings.ToLower(open))
		if start < 0 {
			break
		}
		end := strings.Index(strings.ToLower(s[start:]), strings.ToLower(close))
		if end < 0 {
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len(close):]
	}
	return s
}

// stripCoddyAttachmentXML removes hydrated @mention attachment blocks from s.
// Transcripts keep them - only derived titles drop them. The file body sits in
// CDATA sections (internal/agent wrapXMLCDATA splits an embedded "]]>" across
// two of them), so the closing tag is looked for outside those sections only:
// a file that itself contains "</coddy_attachment>" must not leak past it.
func stripCoddyAttachmentXML(s string) string {
	const openTag, closeTag, cdataOpen, cdataClose = "<coddy_attachment", "</coddy_attachment>", "<![CDATA[", "]]>"
	var b strings.Builder
	for {
		start := strings.Index(s, openTag)
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		rest := s[start:]
		end := -1
		for pos := len(openTag); pos < len(rest); {
			if strings.HasPrefix(rest[pos:], cdataOpen) {
				n := strings.Index(rest[pos+len(cdataOpen):], cdataClose)
				if n < 0 {
					break
				}
				pos += len(cdataOpen) + n + len(cdataClose)
				continue
			}
			if strings.HasPrefix(rest[pos:], closeTag) {
				end = pos + len(closeTag)
				break
			}
			pos++
		}
		if end < 0 {
			// An unterminated block: keep the text as it is rather than eat it.
			b.WriteString(rest)
			return b.String()
		}
		s = rest[end:]
	}
}

// persistedConversationTitle selects the snapshot title saved to session.json.
func persistedConversationTitle(s *State) string {
	return conversationTitle(s, s.GetMessages())
}

// conversationTitle is persistedConversationTitle over a history the caller
// already has.
func conversationTitle(s *State, msgs []llm.Message) string {
	if p := strings.TrimSpace(s.GetTitlePinned()); p != "" {
		return p
	}
	return titleFromMessages(msgs)
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
	if err := renameWithRetry(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// renameWithRetry renames tmpPath over path, retrying briefly on transient
// Windows sharing errors that occur when another handle holds the destination
// open. On POSIX isRetryableFileLockError is always false, so this is a single
// os.Rename with no behavior change.
func renameWithRetry(tmpPath, path string) error {
	const maxAttempts = 20
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = os.Rename(tmpPath, path); err == nil || !isRetryableFileLockError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}

// readFileWithRetry reads path, retrying briefly on the transient Windows sharing
// errors that an in-flight atomic write of the same file produces. Readers are not
// only tests: the session list and activity endpoints read a bundle while its turn
// keeps persisting. On POSIX isRetryableFileLockError is always false, so this is a
// single os.ReadFile with no behavior change.
func readFileWithRetry(path string) ([]byte, error) {
	const maxAttempts = 20
	var (
		b   []byte
		err error
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if b, err = os.ReadFile(path); err == nil || !isRetryableFileLockError(err) {
			return b, err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return b, err
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
