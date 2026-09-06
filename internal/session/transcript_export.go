package session

// Transcript export: the document behind the built-in /export command. The
// persisted message rows are folded into one format-agnostic document (tool
// results paired with their calls, attachment XML reduced to a file list) and
// rendered as markdown, a self-contained HTML page, JSON, or JSON Lines. The
// file always lands inside the session workspace.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ExportFormat names one rendering of an exported transcript.
type ExportFormat string

const (
	ExportFormatMarkdown ExportFormat = "md"
	ExportFormatHTML     ExportFormat = "html"
	ExportFormatJSON     ExportFormat = "json"
	ExportFormatJSONL    ExportFormat = "jsonl"
)

// exportDocumentVersion is the schema version stamped on JSON and JSON Lines exports.
const exportDocumentVersion = 1

// Entry kinds of an exported transcript.
const (
	ExportEntryUser              = "user"
	ExportEntryAssistant         = "assistant"
	ExportEntryToolResult        = "tool_result"
	ExportEntryCompactionSummary = "compaction_summary"
	ExportEntryPlanDocument      = "plan_document"
	ExportEntrySystem            = "system"
)

// ErrExportOutsideWorkspace rejects an export target that leaves the session workspace.
var ErrExportOutsideWorkspace = errors.New("export target must stay inside the session workspace")

// ExportFormats lists the supported formats in catalog order.
func ExportFormats() []ExportFormat {
	return []ExportFormat{ExportFormatMarkdown, ExportFormatHTML, ExportFormatJSON, ExportFormatJSONL}
}

// ParseExportFormat maps a command token onto a format. Matching is
// case-insensitive and "markdown" is an alias of "md".
func ParseExportFormat(token string) (ExportFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "md", "markdown":
		return ExportFormatMarkdown, true
	case "html":
		return ExportFormatHTML, true
	case "json":
		return ExportFormatJSON, true
	case "jsonl":
		return ExportFormatJSONL, true
	}
	return "", false
}

// ExportFormatForPath infers the format from a file extension (.htm counts as HTML).
func ExportFormatForPath(path string) (ExportFormat, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(path)), "."))
	if ext == "htm" {
		ext = "html"
	}
	if ext == "" {
		return "", false
	}
	return ParseExportFormat(ext)
}

// ExportOptions trims the exported document.
type ExportOptions struct {
	// NoTools leaves out tool calls and their results.
	NoTools bool
	// NoThinking leaves out the model's reasoning.
	NoThinking bool
}

// ExportRequest is one parsed /export invocation: the format token the user
// typed (empty when omitted), the optional destination, the trimming options,
// and any --option the parser did not recognize (reported as an error).
type ExportRequest struct {
	Format         string
	Target         string
	Options        ExportOptions
	UnknownOptions []string
}

// ResolveExportRequest picks the format and the destination of one command.
// An explicit format token wins. Without one, a target that is empty, ends
// with a separator, or names an existing directory exports markdown into that
// directory, and any other target takes its format from the file extension; a
// bare word that is neither a format nor an existing directory is refused so
// a mistyped format never becomes a file of that name.
func ResolveExportRequest(cwd string, req ExportRequest, at time.Time) (ExportFormat, ExportTarget, error) {
	if len(req.UnknownOptions) > 0 {
		return "", ExportTarget{}, fmt.Errorf("unknown option %s (supported: --no-tools, --no-thinking)", strings.Join(req.UnknownOptions, ", "))
	}
	token := strings.TrimSpace(req.Format)
	target := strings.TrimSpace(req.Target)
	var format ExportFormat
	switch {
	case token != "":
		f, ok := ParseExportFormat(token)
		if !ok {
			return "", ExportTarget{}, fmt.Errorf("unknown export format %q (supported: md or markdown, html, json, jsonl)", token)
		}
		format = f
	case target == "" || targetWantsDirectory(target):
		format = ExportFormatMarkdown
	default:
		_, abs, err := resolveExportPath(cwd, target)
		if err != nil {
			return "", ExportTarget{}, err
		}
		switch {
		case isExistingDir(abs):
			format = ExportFormatMarkdown
		case filepath.Ext(filepath.Base(target)) == "":
			return "", ExportTarget{}, fmt.Errorf("%q is neither an export format nor an existing directory (supported formats: md or markdown, html, json, jsonl)", target)
		default:
			f, ok := ExportFormatForPath(target)
			if !ok {
				return "", ExportTarget{}, fmt.Errorf("unknown export format for %q (supported extensions: .md, .markdown, .html, .htm, .json, .jsonl)", target)
			}
			format = f
		}
	}
	t, err := ResolveExportTarget(cwd, target, format, at)
	if err != nil {
		return "", ExportTarget{}, err
	}
	return format, t, nil
}

// Extension returns the file extension without the dot.
func (f ExportFormat) Extension() string { return string(f) }

// DisplayName returns the human label used in command replies.
func (f ExportFormat) DisplayName() string {
	switch f {
	case ExportFormatMarkdown:
		return "markdown"
	case ExportFormatHTML:
		return "HTML"
	case ExportFormatJSON:
		return "JSON"
	case ExportFormatJSONL:
		return "JSON Lines"
	}
	return string(f)
}

// ExportFileName returns the default file name, coddy-export-<UTC stamp>.<ext>.
func ExportFileName(f ExportFormat, at time.Time) string {
	return "coddy-export-" + at.UTC().Format("2006-01-02T15-04-05Z") + "." + f.Extension()
}

// ExportTarget is a resolved export destination.
type ExportTarget struct {
	// Path is the absolute file path.
	Path string
	// Display is the path relative to the workspace, for command replies.
	Display string
	// Generated marks a name coddy made up. The write reserves it with an
	// exclusive create and moves on to the next free -N suffix when another
	// process took the name first; an explicit name is replaced instead.
	Generated bool

	// dir, stem and ext rebuild the next candidate of a generated name; seq is
	// the suffix in use (1 means none).
	dir, stem, ext string
	seq            int
}

// next returns the following candidate of a generated name.
func (t ExportTarget) next() ExportTarget {
	t.seq++
	t.Path = filepath.Join(t.dir, fmt.Sprintf("%s-%d.%s", t.stem, t.seq, t.ext))
	return t
}

// targetWantsDirectory reports whether the user wrote the target as a folder.
func targetWantsDirectory(target string) bool {
	return strings.HasSuffix(target, "/") || strings.HasSuffix(target, string(filepath.Separator))
}

// resolveExportPath returns the workspace root and the clean absolute path
// the target names inside it, before any file name is appended.
func resolveExportPath(cwd, target string) (root, abs string, err error) {
	root, err = filepath.Abs(filepath.Clean(strings.TrimSpace(cwd)))
	if err != nil {
		return "", "", err
	}
	raw := strings.TrimSpace(target)
	if raw == "" {
		return root, root, nil
	}
	p := filepath.FromSlash(raw)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if !pathWithin(root, p) {
		return "", "", fmt.Errorf("%w: %s", ErrExportOutsideWorkspace, raw)
	}
	return root, p, nil
}

// ResolveExportTarget maps the optional user-supplied path onto a file inside
// cwd. An empty target, ".", a trailing separator, or an existing directory
// receive the generated file name; any other path names the file itself.
// Paths that leave cwd, relative or absolute, fail with ErrExportOutsideWorkspace.
func ResolveExportTarget(cwd, target string, f ExportFormat, at time.Time) (ExportTarget, error) {
	root, p, err := resolveExportPath(cwd, target)
	if err != nil {
		return ExportTarget{}, err
	}
	generated := targetWantsDirectory(strings.TrimSpace(target)) || p == root || isExistingDir(p)
	if !generated {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return ExportTarget{}, err
		}
		return ExportTarget{Path: p, Display: rel}, nil
	}
	name := ExportFileName(f, at)
	t := ExportTarget{
		Path:      filepath.Join(p, name),
		Generated: true,
		dir:       p,
		stem:      strings.TrimSuffix(name, "."+f.Extension()),
		ext:       f.Extension(),
		seq:       1,
	}
	// Skip the names already on disk so the reply names the likely file; the
	// write still reserves the final name exclusively.
	for {
		if _, err := os.Lstat(t.Path); err != nil {
			break
		}
		t = t.next()
	}
	rel, err := filepath.Rel(root, t.Path)
	if err != nil {
		return ExportTarget{}, err
	}
	t.Display = rel
	return t, nil
}

// pathWithin reports whether p equals root or sits below it. Both paths must
// be absolute and clean.
func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func isExistingDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// nearestExistingAncestor walks up from p to the first path that exists.
func nearestExistingAncestor(p string) (string, error) {
	for {
		if _, err := os.Lstat(p); err == nil {
			return p, nil
		}
		next := filepath.Dir(p)
		if next == p {
			return "", fmt.Errorf("no existing ancestor for %s", p)
		}
		p = next
	}
}

// realPathWithin resolves symlinks on both sides before the containment check.
func realPathWithin(root, p string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	realP, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false, err
	}
	return pathWithin(realRoot, realP), nil
}

// exportWriteMaxAttempts bounds the search for a free generated name.
const exportWriteMaxAttempts = 1000

// WriteExportFile writes data to the target and returns the target actually
// written. On POSIX systems the file is created with owner-only permissions
// (0600). The lexical and symlink pre-checks name the common escapes clearly;
// every filesystem step then runs through an os.Root opened on the workspace,
// which refuses a symbolic link leading outside the root at the time of each
// operation, so a link that appears between the pre-check and the write is
// refused too (filesystem boundaries and bind mounts are not checked, per the
// os.Root contract). A generated name is reserved with an exclusive
// create, moving to the next free -N suffix when another process took it
// first. An explicit name is written to a fresh 0600 temporary file that is
// renamed over the target, so an existing file is never opened and never
// keeps wider permissions; a target that is a directory, a symlink, or any
// other non-regular file is refused.
func WriteExportFile(cwd string, t ExportTarget, data []byte) (ExportTarget, error) {
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(cwd)))
	if err != nil {
		return t, err
	}
	if !pathWithin(root, t.Path) {
		return t, fmt.Errorf("%w: %s", ErrExportOutsideWorkspace, t.Path)
	}
	parent := filepath.Dir(t.Path)
	existing, err := nearestExistingAncestor(parent)
	if err != nil {
		return t, err
	}
	if ok, err := realPathWithin(root, existing); err != nil {
		return t, err
	} else if !ok {
		return t, fmt.Errorf("%w: %s resolves outside %s", ErrExportOutsideWorkspace, existing, root)
	}
	if !t.Generated {
		if fi, err := os.Lstat(t.Path); err == nil {
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				return t, fmt.Errorf("%w: %s is a symbolic link", ErrExportOutsideWorkspace, t.Path)
			case fi.IsDir():
				return t, fmt.Errorf("export target %s is a directory", t.Path)
			case !fi.Mode().IsRegular():
				return t, fmt.Errorf("export target %s is not a regular file", t.Path)
			}
		}
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = r.Close() }()
	relParent, err := filepath.Rel(root, parent)
	if err != nil {
		return t, err
	}
	if relParent != "." {
		if err := r.MkdirAll(relParent, 0o755); err != nil {
			return t, err
		}
	}
	if t.Generated {
		return writeReservedExport(r, root, t, data)
	}
	rel, err := filepath.Rel(root, t.Path)
	if err != nil {
		return t, err
	}
	if err := replaceExportFile(r, rel, data); err != nil {
		return t, err
	}
	return t, nil
}

// writeReservedExport creates a generated name exclusively, shifting to the
// next -N suffix while the name is taken, so two exports racing for the same
// name land in two files.
func writeReservedExport(r *os.Root, root string, t ExportTarget, data []byte) (ExportTarget, error) {
	for attempt := 0; attempt < exportWriteMaxAttempts; attempt++ {
		rel, err := filepath.Rel(root, t.Path)
		if err != nil {
			return t, err
		}
		f, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			t = t.next()
			continue
		}
		if err != nil {
			return t, err
		}
		if err := writeAndClose(f, data); err != nil {
			_ = r.Remove(rel)
			return t, err
		}
		t.Display = rel
		return t, nil
	}
	return t, fmt.Errorf("no free export name after %d attempts under %s", exportWriteMaxAttempts, t.dir)
}

// replaceExportFile writes data to a fresh 0600 temporary file next to rel
// and renames it over rel.
func replaceExportFile(r *os.Root, rel string, data []byte) error {
	tmpRel := fmt.Sprintf("%s.tmp.%d.%d", rel, os.Getpid(), time.Now().UnixNano())
	f, err := r.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := writeAndClose(f, data); err != nil {
		_ = r.Remove(tmpRel)
		return err
	}
	if err := r.Rename(tmpRel, rel); err != nil {
		_ = r.Remove(tmpRel)
		return err
	}
	return nil
}

// writeAndClose writes data, syncs and closes f, reporting the first error.
func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ExportInput gathers what an export needs from a session.
type ExportInput struct {
	SessionID string
	Title     string
	CWD       string
	GitBranch string
	Model     string
	Messages  []llm.Message
	// Stats is the session stats.json content when available.
	Stats *SessionStats
	// ExportedAt stamps the document and the default file name; zero means now.
	ExportedAt time.Time
	// Options trims the document (tool calls, reasoning).
	Options ExportOptions
}

// ExportTokenUsage mirrors the session token counters.
type ExportTokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ExportSessionInfo is the metadata block of an exported transcript.
type ExportSessionInfo struct {
	ID           string            `json:"id"`
	Title        string            `json:"title,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	GitBranch    string            `json:"git_branch,omitempty"`
	Model        string            `json:"model,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	ExportedAt   string            `json:"exported_at"`
	MessageCount int               `json:"message_count"`
	UserTurns    int               `json:"user_turns"`
	TokenUsage   *ExportTokenUsage `json:"token_usage,omitempty"`
}

// ExportAttachment is a file the user attached to a prompt.
type ExportAttachment struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// ExportToolCall is a tool call together with the result it produced.
type ExportToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Input holds the call arguments as JSON, or a JSON string when the model
	// produced arguments that are not valid JSON.
	Input json.RawMessage `json:"input,omitempty"`
	// Result is the tool output; nil when the transcript holds no result for
	// the call (the turn was cancelled or is still running), an empty string
	// when the tool returned nothing.
	Result *string `json:"result,omitempty"`
}

// ExportPlanDocument describes a plan document row.
type ExportPlanDocument struct {
	Slug      string `json:"slug"`
	Name      string `json:"name,omitempty"`
	Overview  string `json:"overview,omitempty"`
	Path      string `json:"path,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ExportEntry is one row of an exported transcript.
type ExportEntry struct {
	Type        string              `json:"type"`
	CreatedAt   string              `json:"created_at,omitempty"`
	Model       string              `json:"model,omitempty"`
	Text        string              `json:"text,omitempty"`
	Reasoning   string              `json:"reasoning,omitempty"`
	ToolCalls   []ExportToolCall    `json:"tool_calls,omitempty"`
	Attachments []ExportAttachment  `json:"attachments,omitempty"`
	ToolCallID  string              `json:"tool_call_id,omitempty"`
	Plan        *ExportPlanDocument `json:"plan,omitempty"`
}

// ExportDocument is the format-agnostic export of one session.
type ExportDocument struct {
	Version int               `json:"version"`
	Session ExportSessionInfo `json:"session"`
	Entries []ExportEntry     `json:"entries"`
}

// ExportResult reports a completed export.
type ExportResult struct {
	Format  ExportFormat
	Target  ExportTarget
	Entries int
	Bytes   int
}

// attachmentPathRE and attachmentNameRE pick the attributes out of the opening
// tag of a hydrated @-mention attachment in any order.
var (
	attachmentPathRE = regexp.MustCompile(`\bpath="([^"]*)"`)
	attachmentNameRE = regexp.MustCompile(`\bname="([^"]*)"`)
)

// sessionAssetsBlockRE matches the note listing uploaded files saved to session assets.
var sessionAssetsBlockRE = regexp.MustCompile(`(?s)<coddy_session_assets>.*?</coddy_session_assets>`)

// blankLineRunRE matches three or more consecutive line breaks (blank lines
// left behind by a removed block).
var blankLineRunRE = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)

const (
	attachmentOpenTag  = "<coddy_attachment"
	attachmentCloseTag = "</coddy_attachment>"
	cdataOpen          = "<![CDATA["
	cdataClose         = "]]>"
)

func isXMLSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// splitUserAttachments strips every hydrated attachment block (see
// internal/agent resourceBlockToXMLAttachment) from a user message and lists
// the attached files. A block body is one or more CDATA sections (the producer
// splits "]]>" into adjacent sections), so the scanner walks the sections
// before it looks for the closing tag: a file that itself contains
// "</coddy_attachment>" cannot end the block early and leak its body. An
// opening tag without a matching block is ordinary text and stays. A message
// without any block is returned verbatim; once a block was removed the gap it
// left is closed and the text trimmed.
func splitUserAttachments(content string) (string, []ExportAttachment) {
	var atts []ExportAttachment
	var out strings.Builder
	rest := content
	removed := false
	for {
		start := strings.Index(rest, attachmentOpenTag)
		if start < 0 {
			out.WriteString(rest)
			break
		}
		after := rest[start+len(attachmentOpenTag):]
		tagEnd := strings.IndexByte(after, '>')
		if tagEnd < 0 || (after != "" && after[0] != '>' && !isXMLSpace(after[0])) {
			// Not an attachment tag (unterminated, or a longer word).
			out.WriteString(rest[:start+len(attachmentOpenTag)])
			rest = after
			continue
		}
		attrs := after[:tagEnd]
		body := after[tagEnd+1:]
		end := attachmentBodyEnd(body)
		if end < 0 {
			out.WriteString(rest[:start+len(attachmentOpenTag)])
			rest = after
			continue
		}
		att := ExportAttachment{}
		if pm := attachmentPathRE.FindStringSubmatch(attrs); len(pm) == 2 {
			att.Path = html.UnescapeString(pm[1])
		}
		if nm := attachmentNameRE.FindStringSubmatch(attrs); len(nm) == 2 {
			att.Name = html.UnescapeString(nm[1])
		}
		if att.Path != "" || att.Name != "" {
			atts = append(atts, att)
		}
		out.WriteString(rest[:start])
		rest = body[end:]
		removed = true
	}
	text := out.String()
	if stripped := sessionAssetsBlockRE.ReplaceAllString(text, ""); stripped != text {
		text = stripped
		removed = true
	}
	if !removed {
		return content, nil
	}
	text = blankLineRunRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text), atts
}

// attachmentBodyEnd returns the offset just past the closing tag of one
// attachment body, skipping CDATA sections so their content cannot end the
// block, or -1 when the block is unterminated. A body that is not CDATA ends
// at its first closing tag.
func attachmentBodyEnd(body string) int {
	i := 0
	for {
		j := i
		for j < len(body) && isXMLSpace(body[j]) {
			j++
		}
		if strings.HasPrefix(body[j:], cdataOpen) {
			k := strings.Index(body[j+len(cdataOpen):], cdataClose)
			if k < 0 {
				return -1
			}
			i = j + len(cdataOpen) + k + len(cdataClose)
			continue
		}
		if strings.HasPrefix(body[j:], attachmentCloseTag) {
			return j + len(attachmentCloseTag)
		}
		k := strings.Index(body[i:], attachmentCloseTag)
		if k < 0 {
			return -1
		}
		return i + k + len(attachmentCloseTag)
	}
}

// toolInputJSON keeps valid JSON arguments as they are and wraps anything
// else in a JSON string so the document stays valid JSON.
func toolInputJSON(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return b
}

// BuildExportDocument folds persisted message rows into an ExportDocument.
// Tool results are attached to the call that produced them; a result whose
// call is not in the transcript becomes a tool_result entry of its own.
func BuildExportDocument(in ExportInput) ExportDocument {
	exportedAt := in.ExportedAt
	if exportedAt.IsZero() {
		exportedAt = time.Now()
	}
	info := ExportSessionInfo{
		ID:           in.SessionID,
		Title:        strings.Join(strings.Fields(in.Title), " "),
		CWD:          in.CWD,
		GitBranch:    in.GitBranch,
		Model:        in.Model,
		ExportedAt:   exportedAt.UTC().Format(time.RFC3339),
		MessageCount: len(in.Messages),
	}
	if in.Stats != nil {
		u := in.Stats.TokenUsageTotal
		if u.InputTokens > 0 || u.OutputTokens > 0 || u.TotalTokens > 0 {
			info.TokenUsage = &ExportTokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TotalTokens: u.TotalTokens}
		}
	}

	type callRef struct{ entry, call int }
	pending := map[string]callRef{}
	entries := make([]ExportEntry, 0, len(in.Messages))
	for _, m := range in.Messages {
		if info.StartedAt == "" && strings.TrimSpace(m.CreatedAt) != "" {
			info.StartedAt = m.CreatedAt
		}
		switch {
		case m.CompactionSummary:
			entries = append(entries, ExportEntry{Type: ExportEntryCompactionSummary, CreatedAt: m.CreatedAt, Model: m.Model, Text: m.Content})
		case m.PlanDocument != nil:
			pd := m.PlanDocument
			text := pd.Content
			if text == "" {
				text = pd.Body
			}
			entries = append(entries, ExportEntry{
				Type:      ExportEntryPlanDocument,
				CreatedAt: m.CreatedAt,
				Text:      text,
				Plan:      &ExportPlanDocument{Slug: pd.Slug, Name: pd.Name, Overview: pd.Overview, Path: pd.Path, UpdatedAt: pd.UpdatedAt},
			})
		case m.Role == llm.RoleUser:
			info.UserTurns++
			text, atts := splitUserAttachments(m.Content)
			for _, p := range m.ImageParts {
				if p.FilePath == "" && p.Name == "" {
					continue
				}
				atts = append(atts, ExportAttachment{Path: p.FilePath, Name: p.Name})
			}
			entries = append(entries, ExportEntry{Type: ExportEntryUser, CreatedAt: m.CreatedAt, Text: text, Attachments: atts})
		case m.Role == llm.RoleAssistant:
			e := ExportEntry{Type: ExportEntryAssistant, CreatedAt: m.CreatedAt, Model: m.Model, Text: m.Content, Reasoning: m.Reasoning}
			if in.Options.NoThinking {
				e.Reasoning = ""
			}
			calls := m.ToolCalls
			if in.Options.NoTools {
				calls = nil
			}
			if e.Text == "" && e.Reasoning == "" && len(calls) == 0 {
				continue
			}
			for i, tc := range calls {
				e.ToolCalls = append(e.ToolCalls, ExportToolCall{ID: tc.ID, Name: tc.Name, Input: toolInputJSON(tc.InputJSON)})
				pending[tc.ID] = callRef{entry: len(entries), call: i}
			}
			entries = append(entries, e)
		case m.Role == llm.RoleTool:
			if in.Options.NoTools {
				continue
			}
			if ref, ok := pending[m.ToolCallID]; ok {
				result := m.Content
				entries[ref.entry].ToolCalls[ref.call].Result = &result
				delete(pending, m.ToolCallID)
				continue
			}
			entries = append(entries, ExportEntry{Type: ExportEntryToolResult, CreatedAt: m.CreatedAt, ToolCallID: m.ToolCallID, Text: m.Content})
		default:
			typ := ExportEntrySystem
			if m.Role != llm.RoleSystem && m.Role != "" {
				typ = string(m.Role)
			}
			entries = append(entries, ExportEntry{Type: typ, CreatedAt: m.CreatedAt, Text: m.Content})
		}
	}
	return ExportDocument{Version: exportDocumentVersion, Session: info, Entries: entries}
}

// RenderExport serializes doc in the given format.
func RenderExport(doc ExportDocument, f ExportFormat) ([]byte, error) {
	switch f {
	case ExportFormatMarkdown:
		return renderExportMarkdown(doc), nil
	case ExportFormatHTML:
		return renderExportHTML(doc), nil
	case ExportFormatJSON:
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case ExportFormatJSONL:
		return renderExportJSONL(doc)
	}
	return nil, fmt.Errorf("unsupported export format %q", f)
}

// ExportSession resolves the request, builds and renders the document, and
// writes it under in.CWD.
func ExportSession(in ExportInput, req ExportRequest) (*ExportResult, error) {
	if in.ExportedAt.IsZero() {
		in.ExportedAt = time.Now().UTC()
	}
	f, t, err := ResolveExportRequest(in.CWD, req, in.ExportedAt)
	if err != nil {
		return nil, err
	}
	in.Options = req.Options
	doc := BuildExportDocument(in)
	data, err := RenderExport(doc, f)
	if err != nil {
		return nil, err
	}
	written, err := WriteExportFile(in.CWD, t, data)
	if err != nil {
		return nil, err
	}
	return &ExportResult{Format: f, Target: written, Entries: len(doc.Entries), Bytes: len(data)}, nil
}

// --- markdown ---------------------------------------------------------------

// markdownFence returns a backtick fence longer than any backtick run inside
// content (at least three), found in one pass.
func markdownFence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	n := 3
	if longest >= 3 {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}

func writeMarkdownCode(b *strings.Builder, lang, content string) {
	fence := markdownFence(content)
	b.WriteString(fence)
	b.WriteString(lang)
	b.WriteByte('\n')
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(fence)
	b.WriteByte('\n')
}

func markdownCodeSpan(s string) string {
	if s == "" {
		return ""
	}
	return "`" + s + "`"
}

func writeMarkdownHeading(b *strings.Builder, title string, parts ...string) {
	b.WriteString("## ")
	b.WriteString(title)
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		b.WriteString(" · ")
		b.WriteString(p)
	}
	b.WriteString("\n\n")
}

func writeMarkdownText(b *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.WriteString(text)
	b.WriteString("\n\n")
}

// writeMarkdownToolInput prints the call arguments pretty-printed, or verbatim
// when the model produced arguments that are not JSON.
func writeMarkdownToolInput(b *strings.Builder, input json.RawMessage) {
	if len(input) == 0 {
		return
	}
	b.WriteString("Input:\n\n")
	var literal string
	if err := json.Unmarshal(input, &literal); err == nil {
		writeMarkdownCode(b, "", literal)
		b.WriteByte('\n')
		return
	}
	writeMarkdownCode(b, "json", prettyJSON(input))
	b.WriteByte('\n')
}

// prettyJSON indents raw JSON for display, falling back to the raw bytes.
func prettyJSON(raw json.RawMessage) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}

func renderExportMarkdown(doc ExportDocument) []byte {
	var b strings.Builder
	s := doc.Session
	b.WriteString("# Coddy session export\n\n")
	item := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fmt.Fprintf(&b, "- **%s**: %s\n", label, value)
	}
	item("Session", markdownCodeSpan(s.ID))
	item("Title", s.Title)
	item("Workspace", markdownCodeSpan(s.CWD))
	item("Git branch", markdownCodeSpan(s.GitBranch))
	item("Model", markdownCodeSpan(s.Model))
	item("Started", s.StartedAt)
	item("Exported", s.ExportedAt)
	item("Transcript", fmt.Sprintf("%d message(s), %d user turn(s)", s.MessageCount, s.UserTurns))
	if u := s.TokenUsage; u != nil {
		item("Tokens", fmt.Sprintf("%d in, %d out, %d total", u.InputTokens, u.OutputTokens, u.TotalTokens))
	}

	for _, e := range doc.Entries {
		b.WriteByte('\n')
		switch e.Type {
		case ExportEntryUser:
			writeMarkdownHeading(&b, "User", e.CreatedAt)
			writeMarkdownText(&b, e.Text)
			if len(e.Attachments) > 0 {
				names := make([]string, 0, len(e.Attachments))
				for _, a := range e.Attachments {
					label := a.Path
					if label == "" {
						label = a.Name
					}
					names = append(names, markdownCodeSpan(label))
				}
				b.WriteString("Attachments: ")
				b.WriteString(strings.Join(names, ", "))
				b.WriteString("\n\n")
			}
		case ExportEntryAssistant:
			writeMarkdownHeading(&b, "Assistant", e.Model, e.CreatedAt)
			if strings.TrimSpace(e.Reasoning) != "" {
				b.WriteString("<details>\n<summary>Reasoning</summary>\n\n")
				b.WriteString(strings.TrimSpace(e.Reasoning))
				b.WriteString("\n\n</details>\n\n")
			}
			writeMarkdownText(&b, e.Text)
			for _, tc := range e.ToolCalls {
				b.WriteString("### Tool call: ")
				b.WriteString(tc.Name)
				b.WriteString("\n\n")
				writeMarkdownToolInput(&b, tc.Input)
				if tc.Result != nil {
					b.WriteString("Result:\n\n")
					if *tc.Result == "" {
						b.WriteString("(empty)\n\n")
					} else {
						writeMarkdownCode(&b, "", *tc.Result)
						b.WriteByte('\n')
					}
				}
			}
		case ExportEntryToolResult:
			writeMarkdownHeading(&b, "Tool result", markdownCodeSpan(e.ToolCallID), e.CreatedAt)
			writeMarkdownCode(&b, "", e.Text)
			b.WriteByte('\n')
		case ExportEntryCompactionSummary:
			writeMarkdownHeading(&b, "Compaction summary", e.Model, e.CreatedAt)
			writeMarkdownText(&b, e.Text)
		case ExportEntryPlanDocument:
			name := ""
			if e.Plan != nil {
				name = strings.TrimSpace(e.Plan.Name)
				if name == "" {
					name = e.Plan.Slug
				}
			}
			title := "Plan document"
			if name != "" {
				title += ": " + name
			}
			writeMarkdownHeading(&b, title, e.CreatedAt)
			writeMarkdownText(&b, e.Text)
		default:
			writeMarkdownHeading(&b, entryTitle(e.Type), e.CreatedAt)
			writeMarkdownText(&b, e.Text)
		}
	}
	return []byte(b.String())
}

// entryTitle capitalizes an entry type for a heading; unknown or empty types
// still get a readable label.
func entryTitle(typ string) string {
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return "Entry"
	}
	return strings.ToUpper(typ[:1]) + typ[1:]
}

// --- HTML -------------------------------------------------------------------

const exportHTMLStyle = `:root{color-scheme:light dark;--bg:#fff;--fg:#1f2328;--muted:#6a737d;--line:#d0d7de;--user:#0969da;--assistant:#1a7f37;--tool:#9a6700;--summary:#8250df;--code:#f6f8fa}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8b949e;--line:#30363d;--user:#58a6ff;--assistant:#3fb950;--tool:#d29922;--summary:#a371f7;--code:#161b22}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:60rem;margin:0 auto;padding:1.5rem 1rem 4rem}
h1{font-size:1.5rem;margin:0 0 .75rem}h2{font-size:1rem;margin:0 0 .5rem}h3{font-size:.9rem;margin:.75rem 0 .25rem}
dl.meta{display:grid;grid-template-columns:max-content 1fr;gap:.25rem 1rem;margin:0 0 1.5rem;font-size:.9rem}
dl.meta dt{color:var(--muted)}dl.meta dd{margin:0;overflow-wrap:anywhere}
.entry{border-left:3px solid var(--line);padding:.5rem 0 .5rem 1rem;margin:0 0 1.25rem}
.entry-user{border-color:var(--user)}.entry-assistant{border-color:var(--assistant)}.entry-tool_result{border-color:var(--tool)}
.entry-compaction_summary{border-color:var(--summary)}.entry-plan_document{border-color:var(--summary)}
.stamp{color:var(--muted);font-weight:normal;font-size:.8rem;margin-left:.5rem}
pre{white-space:pre-wrap;overflow-wrap:anywhere;background:var(--code);border:1px solid var(--line);border-radius:6px;padding:.75rem;margin:.25rem 0 .5rem;font-size:.85rem}
pre.text{background:transparent;border:0;padding:0;font:inherit}
details{margin:.25rem 0 .5rem}summary{cursor:pointer;color:var(--muted)}
.attachments{color:var(--muted);font-size:.85rem}code{font-size:.9em}`

func writeHTMLStamp(b *strings.Builder, parts ...string) {
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		b.WriteString(`<span class="stamp">`)
		b.WriteString(html.EscapeString(p))
		b.WriteString(`</span>`)
	}
}

func writeHTMLPre(b *strings.Builder, class, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.WriteString("<pre")
	if class != "" {
		b.WriteString(` class="` + class + `"`)
	}
	b.WriteString(">")
	b.WriteString(html.EscapeString(text))
	b.WriteString("</pre>\n")
}

func renderExportHTML(doc ExportDocument) []byte {
	var b strings.Builder
	s := doc.Session
	title := s.Title
	if title == "" {
		title = "Coddy session export"
	}
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>" + exportHTMLStyle + "</style>\n</head>\n<body>\n<main>\n<header>\n")
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>\n<dl class=\"meta\">\n")
	item := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		b.WriteString("<dt>" + html.EscapeString(label) + "</dt><dd>" + html.EscapeString(value) + "</dd>\n")
	}
	item("Session", s.ID)
	item("Workspace", s.CWD)
	item("Git branch", s.GitBranch)
	item("Model", s.Model)
	item("Started", s.StartedAt)
	item("Exported", s.ExportedAt)
	item("Transcript", fmt.Sprintf("%d message(s), %d user turn(s)", s.MessageCount, s.UserTurns))
	if u := s.TokenUsage; u != nil {
		item("Tokens", fmt.Sprintf("%d in, %d out, %d total", u.InputTokens, u.OutputTokens, u.TotalTokens))
	}
	b.WriteString("</dl>\n</header>\n")

	for _, e := range doc.Entries {
		b.WriteString(`<section class="entry entry-` + html.EscapeString(e.Type) + "\">\n<h2>")
		switch e.Type {
		case ExportEntryUser:
			b.WriteString("User")
			writeHTMLStamp(&b, e.CreatedAt)
			b.WriteString("</h2>\n")
			writeHTMLPre(&b, "text", e.Text)
			if len(e.Attachments) > 0 {
				b.WriteString(`<p class="attachments">Attachments: `)
				for i, a := range e.Attachments {
					if i > 0 {
						b.WriteString(", ")
					}
					label := a.Path
					if label == "" {
						label = a.Name
					}
					b.WriteString("<code>" + html.EscapeString(label) + "</code>")
				}
				b.WriteString("</p>\n")
			}
		case ExportEntryAssistant:
			b.WriteString("Assistant")
			writeHTMLStamp(&b, e.Model, e.CreatedAt)
			b.WriteString("</h2>\n")
			if strings.TrimSpace(e.Reasoning) != "" {
				b.WriteString("<details>\n<summary>Reasoning</summary>\n")
				writeHTMLPre(&b, "text", e.Reasoning)
				b.WriteString("</details>\n")
			}
			writeHTMLPre(&b, "text", e.Text)
			for _, tc := range e.ToolCalls {
				b.WriteString("<details class=\"tool\">\n<summary>Tool call: " + html.EscapeString(tc.Name) + "</summary>\n")
				if len(tc.Input) > 0 {
					b.WriteString("<h3>Input</h3>\n")
					var literal string
					if err := json.Unmarshal(tc.Input, &literal); err == nil {
						writeHTMLPre(&b, "", literal)
					} else {
						writeHTMLPre(&b, "", prettyJSON(tc.Input))
					}
				}
				if tc.Result != nil {
					b.WriteString("<h3>Result</h3>\n")
					if *tc.Result == "" {
						b.WriteString("<p class=\"attachments\">(empty)</p>\n")
					} else {
						writeHTMLPre(&b, "", *tc.Result)
					}
				}
				b.WriteString("</details>\n")
			}
		case ExportEntryToolResult:
			b.WriteString("Tool result")
			writeHTMLStamp(&b, e.ToolCallID, e.CreatedAt)
			b.WriteString("</h2>\n")
			writeHTMLPre(&b, "", e.Text)
		case ExportEntryCompactionSummary:
			b.WriteString("Compaction summary")
			writeHTMLStamp(&b, e.Model, e.CreatedAt)
			b.WriteString("</h2>\n")
			writeHTMLPre(&b, "text", e.Text)
		case ExportEntryPlanDocument:
			b.WriteString("Plan document")
			if e.Plan != nil {
				name := strings.TrimSpace(e.Plan.Name)
				if name == "" {
					name = e.Plan.Slug
				}
				if name != "" {
					b.WriteString(": " + html.EscapeString(name))
				}
			}
			writeHTMLStamp(&b, e.CreatedAt)
			b.WriteString("</h2>\n")
			writeHTMLPre(&b, "text", e.Text)
		default:
			b.WriteString(html.EscapeString(entryTitle(e.Type)))
			writeHTMLStamp(&b, e.CreatedAt)
			b.WriteString("</h2>\n")
			writeHTMLPre(&b, "text", e.Text)
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</main>\n</body>\n</html>\n")
	return []byte(b.String())
}

// --- JSON Lines -------------------------------------------------------------

// exportJSONLHead is the first line of a JSON Lines export.
type exportJSONLHead struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	ExportSessionInfo
}

func renderExportJSONL(doc ExportDocument) ([]byte, error) {
	var b strings.Builder
	head, err := json.Marshal(exportJSONLHead{Type: "session", Version: doc.Version, ExportSessionInfo: doc.Session})
	if err != nil {
		return nil, err
	}
	b.Write(head)
	b.WriteByte('\n')
	for _, e := range doc.Entries {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}
