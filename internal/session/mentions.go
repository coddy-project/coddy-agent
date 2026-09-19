package session

// Prompt mentions: every "@" reference a user writes is resolved here, once,
// when the message enters the conversation, into an attachment that rides in
// that same user message - a file's text, a folder's listing, another
// session's digest, a rule's body, a subagent the user wants involved. The
// message is persisted with them and never resolved again, so nothing a
// mention produces moves the system prompt or rewrites a message the
// provider has already cached.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// Limits of what one mention may put into a message.
const (
	// mentionRangeScanBytes bounds how much of a file a ranged mention reads
	// to find its lines: a range narrows a log far larger than the inline cap.
	mentionRangeScanBytes = 64 << 20
	// mentionListingEntries caps a folder listing.
	mentionListingEntries = 400
	// mentionListingWalkDepth bounds the walk of a folder git does not list:
	// one outside any checkout, or one git ignores as a whole.
	mentionListingWalkDepth = 4
	// sessionDigestBytes caps the digest of another session.
	sessionDigestBytes = 24 << 10
	// sessionDigestMessageBytes caps one message inside a digest.
	sessionDigestMessageBytes = 3 << 10
	// mentionDocBytes caps a page of the built-in documentation: every
	// guide fits, and a long reference page arrives as its beginning with
	// the way to read the rest.
	mentionDocBytes = 64 << 10
)

// MentionAgent is a subagent a prompt may mention as "@agent:<name>".
type MentionAgent struct {
	Name        string
	Description string
	// Blocked says why this one cannot be spawned (a project definition
	// awaiting approval); its description is then withheld.
	Blocked string
}

// MentionScope is what one prompt may resolve beyond files and folders.
// Files and folders are read for every prompt; the meta references need a
// person on the other end or a surface that can keep them private.
type MentionScope struct {
	// Sessions resolves "@session:<id>" into a digest of that session. Off for
	// a child's task, written by a model, and for a messenger chat, where the
	// people talking are not the owner of every other session on the host.
	Sessions bool
	// Plans inlines "@plans/<slug>.plan.md" from the session bundle.
	Plans bool
	// URLs reads the page an "@https://..." mention names, through the web
	// tools' fetcher and its address guard (mention.FetchURL).
	URLs bool
	// Agents lists the subagents "@agent:<name>" may name, read only when a
	// prompt holds such a mention. The note, when set, is why none can be
	// spawned in this turn; the mention still says which one the user meant.
	Agents func() (agents []MentionAgent, note string)
}

// mentionScope is what a prompt of this session may resolve. A child's task
// was written by the parent model and reads files and folders only; a
// messenger chat does not reach into other sessions.
func (m *Manager) mentionScope(st *State, subagentTurn, askMode bool) MentionScope {
	if subagentTurn || st == nil {
		return MentionScope{}
	}
	return MentionScope{
		Sessions: m.store != nil && !IsGatewayOrigin(st.GetOrigin()),
		Plans:    true,
		URLs:     true,
		Agents: func() ([]MentionAgent, string) {
			return m.mentionAgents(st.GetCWD(), askMode)
		},
	}
}

// mentionAgents lists the subagents the parent model is offered, with the
// same trust decisions: a project definition awaiting approval is named with
// its description withheld.
func (m *Manager) mentionAgents(cwd string, askMode bool) ([]MentionAgent, string) {
	cfg := m.Cfg()
	if cfg == nil || !cfg.Subagents.ResolvedEnabled() {
		return nil, ""
	}
	loader := subagents.NewLoader(cfg.Subagents.Dirs, cfg.Subagents.ResolvedProjectTrust())
	loader.Log = m.log
	entries := subagents.BuildCatalog(loader.Load(cwd, cfg.Paths.Home), cfg.Subagents.ResolvedProjectTrust(),
		subagents.CanonicalWorkspace(cwd), subagents.NewTrustStore(cfg.Paths.Home))
	var out []MentionAgent
	for _, e := range entries {
		if e.Hidden {
			continue
		}
		a := MentionAgent{Name: e.Name, Description: e.Description}
		if e.NeedsApproval {
			a.Description = ""
			a.Blocked = fmt.Sprintf("its project definition awaits approval (coddy agents trust %s)", e.Name)
		}
		out = append(out, a)
	}
	note := ""
	if askMode {
		note = "ask mode is read-only and delegates nothing"
	}
	return out, note
}

// mentionResolver resolves the mentions of one prompt.
type mentionResolver struct {
	ctx        context.Context
	cwd        string
	home       string
	sessionID  string
	sessionDir string
	store      *FileStore
	live       func(id string) *State
	rules      []*rules.Rule
	scope      MentionScope
	// agents are read from scope.Agents the first time a prompt names one.
	agents       []MentionAgent
	agentsNote   string
	agentsLoaded bool
	// seen holds the key of every attachment already in the prompt, so a
	// reference repeated in one message is read once.
	seen map[string]bool
	// dry resolves without reading: a file is looked at, never opened; a
	// folder is not listed; a session is found, not summarised; a page is
	// not fetched. CheckMentions runs it, so what the composer marks is what
	// sending would attach.
	dry bool
}

// ResolvePromptMentions returns blocks with an attachment resource after each
// text block for every reference that text makes. A reference that names
// nothing - "@username", a path that does not exist - stays prose; one that
// names something that cannot be inlined (a binary file, a file too large
// without a range) is still attached, with a note saying why its body is
// missing, so the model does not guess. It never fails the prompt.
func (m *Manager) ResolvePromptMentions(ctx context.Context, st *State, blocks []acp.ContentBlock, scope MentionScope) []acp.ContentBlock {
	if st == nil {
		return blocks
	}
	r := &mentionResolver{
		ctx:        ctx,
		cwd:        st.GetCWD(),
		home:       mention.HomeDir(),
		sessionID:  st.GetID(),
		sessionDir: strings.TrimSpace(st.GetPersistedSessionDir()),
		rules:      st.GetRulesCatalog(),
		scope:      scope,
	}
	if m != nil {
		r.store = m.store
		r.live = m.getSession
	}
	return r.resolve(blocks)
}

// ResolveMentionsInDir is ResolvePromptMentions for a caller without a
// session: files and folders only.
func ResolveMentionsInDir(cwd string, blocks []acp.ContentBlock) []acp.ContentBlock {
	r := &mentionResolver{ctx: context.Background(), cwd: cwd, home: mention.HomeDir()}
	return r.resolve(blocks)
}

func (r *mentionResolver) resolve(blocks []acp.ContentBlock) []acp.ContentBlock {
	r.seen = map[string]bool{}
	for _, b := range blocks {
		if b.Type == acp.ContentTypeResource && b.Resource != nil && strings.TrimSpace(b.Resource.Text) != "" {
			r.seen[r.resourceKey(b.Resource)] = true
		}
	}
	out := make([]acp.ContentBlock, 0, len(blocks)+2)
	for _, b := range blocks {
		out = append(out, b)
		if b.Type != acp.ContentTypeText {
			continue
		}
		for _, tok := range mention.Parse(b.Text) {
			if res, _, ok := r.resolveToken(b.Text, tok); ok {
				out = append(out, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: res})
			}
		}
	}
	return out
}

// resourceKey is the identity of an attachment: its kind and what it names.
func (r *mentionResolver) resourceKey(res *acp.Resource) string {
	kind := mention.KindFile
	if res.Mention != nil && res.Mention.Kind != "" {
		kind = res.Mention.Kind
	}
	base, start, end := SplitLineRangeURI(res.URI)
	if kind == mention.KindFile || kind == mention.KindDirectory {
		if loc, ok := mention.Resolve(r.cwd, r.home, base); ok {
			base = loc.Display
		}
	}
	return kind + "|" + lineRangeURI(base, start, end)
}

func (r *mentionResolver) claim(key string) bool {
	if r.dry {
		// A check marks every mention, the repeated one too.
		return true
	}
	if r.seen[key] {
		return false
	}
	r.seen[key] = true
	return true
}

// resolveToken reads one token. reading is the index of the path reading
// that won, -1 for a meta reference or a web page.
func (r *mentionResolver) resolveToken(text string, tok mention.Token) (res *acp.Resource, reading int, ok bool) {
	typed := text[tok.Start+1 : tok.End]
	if tok.URL != "" {
		res, ok = r.resolveURL(tok.URL)
		return res, -1, ok
	}
	switch tok.Scheme {
	case mention.SchemeSession:
		res, ok = r.resolveSession(tok.Ref, typed)
		return res, -1, ok
	case mention.SchemeRule:
		res, ok = r.resolveRule(tok.Ref, typed)
		return res, -1, ok
	case mention.SchemeAgent:
		res, ok = r.resolveAgent(tok.Ref, typed)
		return res, -1, ok
	case mention.SchemeCoddy:
		res, ok = r.resolveDoc(tok.Ref, typed)
		return res, -1, ok
	}
	for i, rd := range tok.Readings {
		// The label is the path as typed, without its quotes or the range
		// the lines attribute carries: what ForDisplay matches the text by.
		if res, ok, stop := r.resolvePath(rd, rd.Path); stop {
			return res, i, ok
		}
	}
	// A bare word that names no path may still name a mention-only rule:
	// "@deploy" the way Cursor spells it.
	if last := len(tok.Readings) - 1; !tok.Quoted && last >= 0 {
		if name := tok.Readings[last].Path; isBareName(name) {
			if rule := findMentionRule(r.rules, name); rule != nil {
				res, ok = r.ruleResource(rule, name)
				return res, last, ok
			}
		}
	}
	return nil, -1, false
}

// isBareName reports whether a token is a plain name rather than a path.
func isBareName(s string) bool {
	if s == "" || strings.ContainsAny(s, `/\.~:`) {
		return false
	}
	return true
}

// resolvePath reads one path reading. stop is true once the reading named
// something - resolved or not - so no shorter reading of the token is tried.
func (r *mentionResolver) resolvePath(reading mention.PathReading, typed string) (res *acp.Resource, ok bool, stop bool) {
	if r.scope.Plans && r.sessionDir != "" && plans.IsPlanMention(reading.Path) {
		if doc, err := plans.ReadByMention(r.sessionDir, reading.Path); err == nil {
			key := mention.KindPlan + "|" + reading.Path
			if !r.claim(key) {
				return nil, false, true
			}
			return &acp.Resource{
				URI:      filepath.ToSlash(strings.TrimPrefix(reading.Path, "./")),
				MimeType: "text/markdown; charset=utf-8",
				Text:     doc.Content,
				Mention:  &acp.ResourceMention{Kind: mention.KindPlan},
			}, true, true
		}
	}
	loc, okLoc := mention.Resolve(r.cwd, r.home, reading.Path)
	if !okLoc {
		return nil, false, false
	}
	info, err := os.Stat(loc.Abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) || errors.Is(err, syscall.ENOTDIR) {
			return nil, false, false
		}
		// It is there, but it cannot be looked at: say so.
		res := r.fileResource(loc, reading, typed)
		if res == nil {
			return nil, false, true
		}
		res.Text = fmt.Sprintf("Not inlined: %s could not be read (%v).", loc.Display, unwrapPathError(err))
		return res, true, true
	}
	if info.IsDir() {
		display := strings.TrimSuffix(loc.Display, "/") + "/"
		if loc.Display == "./" {
			display = "./"
		}
		key := mention.KindDirectory + "|" + display
		if !r.claim(key) {
			return nil, false, true
		}
		listing := ""
		if !r.dry {
			listing = r.listFolder(loc, display)
		}
		return &acp.Resource{
			URI:      display,
			MimeType: "text/plain; charset=utf-8",
			Text:     listing,
			Mention:  &acp.ResourceMention{Kind: mention.KindDirectory, Typed: typed, Path: loc.Abs},
		}, true, true
	}
	if strings.HasSuffix(reading.Path, "/") || strings.HasSuffix(reading.Path, `\`) {
		// "@name/" asked for a folder and found a file: not what was meant.
		return nil, false, false
	}
	res = r.fileResource(loc, reading, typed)
	if res == nil {
		return nil, false, true
	}
	if !r.dry {
		res.Text = readMentionFile(loc, info, reading.Range)
	}
	return res, true, true
}

// fileResource is the attachment shell of a file mention, or nil when the
// same file (and range) is already attached.
func (r *mentionResolver) fileResource(loc mention.Location, reading mention.PathReading, typed string) *acp.Resource {
	uri := lineRangeURI(loc.Display, reading.Range.Start, reading.Range.End)
	if !r.claim(mention.KindFile + "|" + uri) {
		return nil
	}
	return &acp.Resource{
		URI:      uri,
		MimeType: "text/plain; charset=utf-8",
		Mention:  &acp.ResourceMention{Kind: mention.KindFile, Typed: typed, Path: loc.Abs},
	}
}

func unwrapPathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// readMentionFile returns the text a file mention inlines: the file, or the
// lines of its range, or a note saying why there is nothing to inline.
func readMentionFile(loc mention.Location, info os.FileInfo, rng mention.Range) string {
	size := info.Size()
	if rng.IsZero() && size > MaxPromptAttachmentBytes {
		return fmt.Sprintf("Not inlined: %s is %s, over the %s a mention inlines. Read the part you need with the read tool (offset and limit), or mention a line range such as @%s:1-200.",
			loc.Display, humanBytes(size), humanBytes(MaxPromptAttachmentBytes), loc.Display)
	}
	limit := int64(MaxPromptAttachmentBytes)
	if !rng.IsZero() {
		limit = mentionRangeScanBytes
	}
	f, err := os.Open(loc.Abs)
	if err != nil {
		return fmt.Sprintf("Not inlined: %s could not be read (%v).", loc.Display, unwrapPathError(err))
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return fmt.Sprintf("Not inlined: %s could not be read (%v).", loc.Display, unwrapPathError(err))
	}
	text, _, err := textenc.DecodeToUTF8(data)
	if err != nil {
		return fmt.Sprintf("Not inlined: %s is binary, or text in an encoding that could not be detected (%s).", loc.Display, humanBytes(size))
	}
	if rng.IsZero() {
		if text == "" {
			return "(empty file)"
		}
		return text
	}
	sliced, err := sliceLines(text, rng.Start, rng.End)
	if err != nil {
		lines := strings.Count(text, "\n")
		if text != "" && !strings.HasSuffix(text, "\n") {
			lines++
		}
		return fmt.Sprintf("Not inlined: lines %d-%d are past the end of %s, which has %d lines.", rng.Start, rng.End, loc.Display, lines)
	}
	if len(sliced) > MaxPromptAttachmentBytes {
		cut := sliced[:MaxPromptAttachmentBytes]
		for !utf8.ValidString(cut) && len(cut) > 0 {
			cut = cut[:len(cut)-1]
		}
		return cut + fmt.Sprintf("\n\n[cut at %s: the range is longer than a mention inlines]", humanBytes(MaxPromptAttachmentBytes))
	}
	return sliced
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// listFolder renders a folder mention: its entries, breadth first so a large
// tree shows its top levels whole rather than one branch deep, with the
// folders whose contents were cut marked. Inside the workspace the index
// answers, so what .gitignore leaves out stays out; elsewhere the folder is
// walked with the index's skip list.
func (r *mentionResolver) listFolder(loc mention.Location, display string) string {
	// The folder as it is on disk now, not as the completion index last saw
	// it: a folder written a moment before the prompt is listed whole. Inside
	// a git checkout git decides what is left out; a folder git offers nothing
	// of (ignored as a whole, or not a checkout) is walked.
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	rel, _, gitOK := mention.ListGitFolder(ctx, loc.Abs, mention.MaxIndexEntries)
	source := mention.SourceGit
	if !gitOK || len(rel) == 0 {
		rel = walkFolder(loc.Abs, mentionListingWalkDepth, mentionListingEntries*4)
		source = ""
	}
	sort.SliceStable(rel, func(i, j int) bool {
		di, dj := entryDepth(rel[i].Path), entryDepth(rel[j].Path)
		if di != dj {
			return di < dj
		}
		return rel[i].Path < rel[j].Path
	})
	total := len(rel)
	shown := rel
	if len(shown) > mentionListingEntries {
		shown = shown[:mentionListingEntries]
	}
	kept := make(map[string]bool, len(shown))
	for _, e := range shown {
		kept[e.Path] = true
	}
	cutDirs := map[string]bool{}
	for _, e := range rel[len(shown):] {
		parent := parentDir(e.Path)
		for parent != "" && !kept[parent] {
			parent = parentDir(strings.TrimSuffix(parent, "/"))
		}
		if parent != "" {
			cutDirs[parent] = true
		}
	}
	sort.Slice(shown, func(i, j int) bool { return shown[i].Path < shown[j].Path })
	var b strings.Builder
	switch {
	case total == 0:
		fmt.Fprintf(&b, "Folder %s is empty", display)
	case total == len(shown):
		fmt.Fprintf(&b, "Folder %s holds %d entries", display, total)
	default:
		fmt.Fprintf(&b, "Folder %s holds %d entries; the first %d by depth are listed", display, total, len(shown))
	}
	if source == mention.SourceGit {
		b.WriteString(" (files ignored by git left out)")
	}
	b.WriteString(":\n")
	for _, e := range shown {
		b.WriteString(e.Path)
		if cutDirs[e.Path] {
			b.WriteString("  (more inside, not listed)")
		}
		b.WriteByte('\n')
	}
	if total > len(shown) {
		fmt.Fprintf(&b, "%d more entries are not listed; read a subfolder with the read tool.\n", total-len(shown))
	}
	return strings.TrimRight(b.String(), "\n")
}

func entryDepth(p string) int { return strings.Count(strings.TrimSuffix(p, "/"), "/") }

func parentDir(p string) string {
	p = strings.TrimSuffix(p, "/")
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i+1]
}

// walkFolder lists dir relative to itself, depth-bounded and capped, with the
// folders the workspace index never enters skipped.
func walkFolder(dir string, maxDepth, limit int) []mention.Entry {
	var out []mention.Entry
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/")
		if d.IsDir() {
			if mention.SkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			out = append(out, mention.Entry{Path: rel + "/", Dir: true})
			if depth+1 >= maxDepth {
				return filepath.SkipDir
			}
		} else {
			out = append(out, mention.Entry{Path: rel})
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// mentionURLTimeout bounds how long a prompt waits for a mentioned page.
const mentionURLTimeout = 25 * time.Second

// resolveURL attaches the page an "@https://..." mention names, read now. A
// page that cannot be read is still attached, with the reason, so the model
// knows the user pointed at it.
func (r *mentionResolver) resolveURL(u string) (*acp.Resource, bool) {
	if !r.scope.URLs {
		return nil, false
	}
	if !r.claim(mention.KindURL + "|" + u) {
		return nil, false
	}
	if r.dry {
		if !mention.CanFetchURL() {
			return nil, false
		}
		return &acp.Resource{URI: u, Mention: &acp.ResourceMention{Kind: mention.KindURL, Name: u}}, true
	}
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, mentionURLTimeout)
	defer cancel()
	text, ok, err := mention.FetchURL(ctx, u)
	if !ok {
		return nil, false
	}
	if err != nil {
		text = fmt.Sprintf("Not inlined: %s could not be read (%v). The webfetch tool can try again.", u, err)
	}
	return &acp.Resource{
		URI:      u,
		MimeType: "text/markdown; charset=utf-8",
		Text:     text,
		Mention:  &acp.ResourceMention{Kind: mention.KindURL, Name: u},
	}, true
}

// resolveSession attaches a digest of another session.
func (r *mentionResolver) resolveSession(ref, typed string) (*acp.Resource, bool) {
	if !r.scope.Sessions || r.store == nil {
		return nil, false
	}
	id, err := r.store.ResolveSessionID(ref)
	if err != nil || id == r.sessionID {
		return nil, false
	}
	key := mention.KindSession + "|" + id
	if !r.claim(key) {
		return nil, false
	}
	if r.dry {
		return &acp.Resource{URI: "session:" + id, Mention: &acp.ResourceMention{Kind: mention.KindSession, Typed: typed}}, true
	}
	digest, title, err := r.sessionDigest(id)
	if err != nil {
		return nil, false
	}
	return &acp.Resource{
		URI:      "session:" + id,
		MimeType: "text/plain; charset=utf-8",
		Text:     digest,
		Mention:  &acp.ResourceMention{Kind: mention.KindSession, Name: title, Typed: typed},
	}, true
}

// sessionDigest renders what the model gets for "@session:<id>": where the
// session ran, its summary when it was compacted, and its latest messages
// within a budget, with the path of the full transcript for anything cut.
func (r *mentionResolver) sessionDigest(id string) (string, string, error) {
	var msgs []llm.Message
	var meta SessionMeta
	dir := r.store.SessionPath(id)
	if st := r.liveSession(id); st != nil {
		msgs = st.GetMessages()
		meta = SessionMeta{ID: id, CWD: st.GetCWD(), Title: persistedConversationTitle(st)}
	}
	if snap, err := r.store.ReadSnapshot(id); err == nil {
		if msgs == nil {
			msgs = snap.Messages
		}
		title := meta.Title
		meta = snap.Meta
		if title != "" {
			meta.Title = title
		}
		if snap.Dir != "" {
			dir = snap.Dir
		}
	} else if msgs == nil {
		return "", "", err
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = titleFromMessages(msgs)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s", id)
	if title != "" {
		fmt.Fprintf(&b, " - %q", title)
	}
	b.WriteString("\n")
	if meta.CWD != "" {
		fmt.Fprintf(&b, "Workspace: %s\n", meta.CWD)
	}
	if meta.CreatedAt != "" || meta.UpdatedAt != "" {
		fmt.Fprintf(&b, "Started: %s, last active: %s\n", orDash(meta.CreatedAt), orDash(meta.UpdatedAt))
	}
	fmt.Fprintf(&b, "Full transcript (JSON): %s\n", filepath.Join(dir, MessagesFileName))

	window := MessagesForLLM(msgs)
	if len(window) > 0 && window[0].CompactionSummary {
		b.WriteString("\nSummary of its earlier conversation:\n")
		b.WriteString(clipText(strings.TrimSpace(window[0].Content), sessionDigestBytes/3))
		b.WriteString("\n")
		window = window[1:]
	}
	lines := digestLines(window)
	budget := sessionDigestBytes - b.Len()
	start := len(lines)
	used := 0
	for start > 0 && used+len(lines[start-1])+1 <= budget {
		start--
		used += len(lines[start]) + 1
	}
	if start > 0 {
		fmt.Fprintf(&b, "\nLatest messages (%d earlier ones left out):\n", countMessages(lines[:start]))
	} else {
		b.WriteString("\nConversation:\n")
	}
	for _, l := range lines[start:] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), title, nil
}

func (r *mentionResolver) liveSession(id string) *State {
	if r.live == nil {
		return nil
	}
	return r.live(id)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// digestLines renders messages as "User: ..." / "Assistant: ..." entries.
// Tool results are left out: the assistant's own words say what they found,
// and a line names the tools it called.
func digestLines(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			if m.BackgroundWake != nil || m.CompactionSummary {
				continue
			}
			text := strings.TrimSpace(mention.ForDisplay(stripCoddySessionAssetsXML(m.Content)))
			if text == "" {
				continue
			}
			out = append(out, "User: "+clipText(text, sessionDigestMessageBytes))
		case llm.RoleAssistant:
			if text := strings.TrimSpace(m.Content); text != "" {
				out = append(out, "Assistant: "+clipText(text, sessionDigestMessageBytes))
			}
			if len(m.ToolCalls) > 0 {
				var names []string
				seen := map[string]bool{}
				for _, tc := range m.ToolCalls {
					if !seen[tc.Name] {
						seen[tc.Name] = true
						names = append(names, tc.Name)
					}
				}
				out = append(out, "(assistant called: "+strings.Join(names, ", ")+")")
			}
		}
	}
	return out
}

func countMessages(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "User: ") || strings.HasPrefix(l, "Assistant: ") {
			n++
		}
	}
	return n
}

func clipText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut + " […]"
}

// resolveRule attaches a rule's body: "@rule:<name>" names any rule of the
// catalog, the way a bare "@name" names a mention-only one.
func (r *mentionResolver) resolveRule(name, typed string) (*acp.Resource, bool) {
	var rule *rules.Rule
	for _, rl := range r.rules {
		if rl != nil && strings.EqualFold(rl.CanonicalName(), name) {
			rule = rl
			break
		}
	}
	if rule == nil {
		return nil, false
	}
	return r.ruleResource(rule, typed)
}

func findMentionRule(catalog []*rules.Rule, name string) *rules.Rule {
	for _, rl := range catalog {
		if rl != nil && rl.ApplyMode == rules.ApplyMention && strings.EqualFold(rl.CanonicalName(), name) {
			return rl
		}
	}
	return nil
}

func (r *mentionResolver) ruleResource(rule *rules.Rule, typed string) (*acp.Resource, bool) {
	res := RuleAttachment(r.cwd, r.home, rule)
	if !r.claim(mention.KindRule + "|" + res.URI) {
		return nil, false
	}
	res.Mention.Typed = typed
	return res, true
}

// RuleAttachment is the attachment that carries a rule's body in a user
// message: an explicitly mentioned rule, or one a mentioned path activated.
func RuleAttachment(cwd, home string, rule *rules.Rule) *acp.Resource {
	path := RuleAttachmentPath(cwd, home, rule)
	return &acp.Resource{
		URI:      path,
		MimeType: "text/markdown; charset=utf-8",
		Text:     strings.TrimSpace(rule.Content),
		Mention:  &acp.ResourceMention{Kind: mention.KindRule, Name: rule.CanonicalName()},
	}
}

// RuleAttachmentPath is how a rule is named in its attachment: its file,
// relative to the workspace when it lives there.
func RuleAttachmentPath(cwd, home string, rule *rules.Rule) string {
	if rule == nil {
		return ""
	}
	if loc, ok := mention.Resolve(cwd, home, rule.FilePath); ok && strings.TrimSpace(rule.FilePath) != "" {
		return loc.Display
	}
	return "rule:" + rule.CanonicalName()
}

// resolveDoc attaches a page of Coddy's own documentation, or one section of
// it: "@coddy:features/mentions#completion". The documentation is the one
// built into this binary, so it is mentionable from every surface and every
// session, a subagent's task included.
func (r *mentionResolver) resolveDoc(ref, typed string) (*acp.Resource, bool) {
	lib, err := docs.Default()
	if err != nil {
		return nil, false
	}
	page, anchor, err := lib.Resolve(ref)
	if err != nil {
		return nil, false
	}
	uri := docs.LinkScheme + docs.Ref(page.Slug, anchor)
	if !r.claim(mention.KindDoc + "|" + uri) {
		return nil, false
	}
	if r.dry {
		return &acp.Resource{URI: uri, Mention: &acp.ResourceMention{Kind: mention.KindDoc, Typed: typed}}, true
	}
	rd, err := page.Read(docs.ReadOptions{Anchor: anchor, MaxBytes: mentionDocBytes})
	if err != nil {
		return nil, false
	}
	text, name := rd.Text, page.Title
	if rd.Heading != nil {
		name = page.Title + " > " + rd.Heading.Text
	}
	if rd.Next != 0 {
		// A reference page is longer than a question needs: the attachment
		// is its beginning, and says how the model reads the rest.
		text += fmt.Sprintf("\n\n[The page continues at line %d of %d: read the rest with the coddy_docs_read tool (page %q, offset %d) or one section of it (page \"%s#<anchor>\").", rd.Next, rd.Total, docs.Ref(page.Slug, anchor), rd.Next, page.Slug)
		if anchor == "" {
			text += " Its sections:\n" + page.Outline()
		}
		text += "]"
	}
	return &acp.Resource{
		URI:      uri,
		MimeType: "text/markdown; charset=utf-8",
		Text:     text,
		Mention:  &acp.ResourceMention{Kind: mention.KindDoc, Name: name, Typed: typed},
	}, true
}

// resolveAgent attaches the user's request to involve a subagent.
func (r *mentionResolver) resolveAgent(name, typed string) (*acp.Resource, bool) {
	if r.scope.Agents == nil {
		return nil, false
	}
	if !r.agentsLoaded {
		r.agents, r.agentsNote = r.scope.Agents()
		r.agentsLoaded = true
	}
	var agent *MentionAgent
	for i := range r.agents {
		if strings.EqualFold(r.agents[i].Name, name) {
			agent = &r.agents[i]
			break
		}
	}
	if agent == nil {
		return nil, false
	}
	if !r.claim(mention.KindAgent + "|" + agent.Name) {
		return nil, false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The user points at the subagent %q", agent.Name)
	if d := strings.TrimSpace(agent.Description); d != "" {
		fmt.Fprintf(&b, " (%s)", d)
	}
	b.WriteString(".\n")
	note := strings.TrimSpace(r.agentsNote)
	if agent.Blocked != "" {
		note = agent.Blocked
	}
	if note != "" {
		fmt.Fprintf(&b, "It cannot be spawned in this turn: %s. Do the work yourself.", note)
	} else {
		fmt.Fprintf(&b, "Hand the part of the request it is meant for to it with the spawn_agent tool (agent %q) instead of doing that part yourself.", agent.Name)
	}
	return &acp.Resource{
		URI:      "agent:" + agent.Name,
		MimeType: "text/plain; charset=utf-8",
		Text:     b.String(),
		Mention:  &acp.ResourceMention{Kind: mention.KindAgent, Name: agent.Name, Typed: typed},
	}, true
}
