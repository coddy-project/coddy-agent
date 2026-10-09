//go:build http

package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

func describeFallbackTitle(words []string) string {
	if len(words) == 0 {
		return ""
	}
	n := min(8, len(words))
	return strings.Join(words[:n], " ")
}

func describeClampWords(s string, maxWords int) string {
	w := strings.Fields(s)
	if len(w) <= maxWords {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:maxWords], " ")
}

// repoRootCache memoizes gitws.MainCheckoutRoot per session cwd for a short
// window: the sessions list is polled while History is open, and a
// `git rev-parse` spawn per distinct cwd per request is measurable for
// folders outside git.
var repoRootCache sync.Map // string cwd -> repoRootCacheEntry

type repoRootCacheEntry struct {
	root    string
	expires time.Time
}

// sessionRepoRoot reports the main checkout a session's cwd belongs to, or ""
// for a folder outside git.
func sessionRepoRoot(cwd string) string {
	if v, ok := repoRootCache.Load(cwd); ok {
		if ent, ok := v.(repoRootCacheEntry); ok && time.Now().Before(ent.expires) {
			return ent.root
		}
	}
	root := gitws.MainCheckoutRoot(cwd)
	repoRootCache.Store(cwd, repoRootCacheEntry{root: root, expires: time.Now().Add(30 * time.Second)})
	return root
}

func describeStripLineNoise(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "- ")
	s = strings.TrimPrefix(s, "* ")
	s = strings.Trim(s, `"'“”„`)
	for strings.HasPrefix(s, "**") {
		s = strings.TrimPrefix(s, "**")
		if i := strings.Index(s, "**"); i >= 0 {
			s = strings.TrimSpace(s[:i] + s[i+2:])
		} else {
			break
		}
	}
	return strings.TrimSpace(s)
}

// jsonTagList renders a tag set for a JSON body: an empty set is an empty array
// rather than null, so a client can assign it without a nil check.
func jsonTagList(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// describeTagsPrefix is how the title prompt asks for the labels. The tags ride
// on the request that already names the conversation, so a session is filed
// without a second call to the model.
const describeTagsPrefix = "tags:"

// describeSplitTagsLine takes the tag line out of the model answer and returns
// what is left for the phrase. A model that ignored the instruction leaves no
// such line and gets exactly the behaviour it had before tags existed: the
// title is what matters, the tags are a bonus.
func describeSplitTagsLine(raw string) (rest string, tags []string) {
	kept := make([]string, 0, 4)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := describeStripLineNoise(line)
		// The prefix is ASCII, so it is matched case-insensitively on the head of
		// the original line and cut at its own fixed length. Measuring the offset
		// on a lower-cased copy would be wrong: case folding changes how many
		// bytes a rune takes (Ⱥ is two, ⱥ is three), so the offset can land
		// inside a rune, or before the start of the string.
		if len(trimmed) >= len(describeTagsPrefix) &&
			strings.EqualFold(trimmed[:len(describeTagsPrefix)], describeTagsPrefix) {
			tags = append(tags, session.ParseTagList(trimmed[len(describeTagsPrefix):])...)
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), session.NormalizeTags(tags)
}

// describePickPhraseFromLLM picks a usable title from model output. Some models emit a junk first line (e.g. "Po") then the real phrase.
func describePickPhraseFromLLM(llmRaw string, userWords []string) string {
	trimmed := strings.TrimSpace(llmRaw)
	if trimmed == "" {
		return describeFallbackTitle(userWords)
	}
	type scored struct {
		text  string
		words int
		chars int
	}
	var cands []scored
	for _, line := range strings.Split(trimmed, "\n") {
		part := describeStripLineNoise(line)
		if part == "" {
			continue
		}
		fw := strings.Fields(part)
		if len(fw) == 0 {
			continue
		}
		joined := strings.Join(fw, " ")
		cands = append(cands, scored{
			text:  joined,
			words: len(fw),
			chars: utf8.RuneCountInString(joined),
		})
	}
	bestText := ""
	bestScore := 0
	substantial := func(c scored) bool {
		if c.words >= 3 {
			return true
		}
		return c.words >= 2 && c.chars >= 12
	}
	for _, c := range cands {
		if !substantial(c) {
			continue
		}
		score := c.words*120 + min(c.chars, 140)
		if score > bestScore {
			bestScore = score
			bestText = c.text
		}
	}
	if bestText == "" && len(cands) > 0 {
		longest := ""
		for _, c := range cands {
			if c.chars > utf8.RuneCountInString(longest) {
				longest = c.text
			}
		}
		if utf8.RuneCountInString(longest) >= 8 {
			bestText = longest
		}
	}
	if bestText == "" || utf8.RuneCountInString(bestText) < 4 {
		return describeFallbackTitle(userWords)
	}
	return describeClampWords(bestText, 12)
}

func (s *Server) registerCoddyRoutes() {
	s.mux.HandleFunc("GET /coddy/info", s.coddyInfoGet)
	s.mux.HandleFunc("GET /coddy/workspace/files", s.coddyWorkspaceFilesGet)
	s.mux.HandleFunc("GET /coddy/workspace/context", s.coddyWorkspaceContextGet)
	s.mux.HandleFunc("POST /coddy/workspace/fetch", s.coddyWorkspaceFetchPost)
	s.mux.HandleFunc("GET /coddy/workspace/folders", s.coddyWorkspaceFoldersGet)
	s.mux.HandleFunc("POST /coddy/workspace/folders", s.coddyWorkspaceFoldersPost)
	s.mux.HandleFunc("GET /coddy/workspace/file", s.coddyWorkspaceFileGet)
	s.mux.HandleFunc("GET /coddy/mentions", s.coddyMentionsGet)
	s.mux.HandleFunc("POST /coddy/mentions/check", s.coddyMentionsCheckPost)
	s.mux.HandleFunc("GET /coddy/slash-commands", s.coddySlashCommandsGet)
	s.mux.HandleFunc("GET /coddy/commands", s.coddyCommandsGet)
	s.mux.HandleFunc("GET /coddy/events", s.coddyEventsStream)
	s.mux.HandleFunc("GET /coddy/sessions", s.coddySessionsList)
	s.mux.HandleFunc("POST /coddy/sessions/bulk-delete", s.coddySessionsBulkDelete)
	s.mux.HandleFunc("POST /coddy/sessions/pins/reorder", s.coddySessionPinsReorder)
	s.mux.HandleFunc("POST /coddy/describe", s.coddyDescribePost)
	s.mux.HandleFunc("POST /coddy/enhance-prompt", s.coddyEnhancePromptPost)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/activity", s.coddySessionActivityGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/messages", s.coddySessionMessagesGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/assets/{name}", s.coddySessionAssetGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/artifacts/{artifactID}", s.coddySessionArtifactGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/artifacts/{artifactID}/preview", s.coddySessionArtifactPreviewGet)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/artifacts/{artifactID}/reveal", s.coddySessionArtifactRevealPost)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/assets/{name}/thumbnail", s.coddySessionAssetThumbnailGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/composer-stream", s.coddySessionComposerStream)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/tool-calls", s.coddyToolCallsList)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/tool-calls/{toolCallId}", s.coddyToolCallGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/stats", s.coddySessionStatsGet)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}", s.coddySessionPatch)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/workspace", s.coddySessionWorkspacePost)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/cancel", s.coddySessionCancelGeneration)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/compact", s.coddySessionCompactPost)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/question", s.coddySessionQuestionPost)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/permission", s.coddySessionPermissionPost)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}", s.coddySessionDelete)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/plan", s.coddyPlanGet)
	s.mux.HandleFunc("PUT /coddy/sessions/{id}/plan", s.coddyPlanPut)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/plan/archive", s.coddyPlanArchivePost)
	s.registerDesignPlanRoutes()
	s.registerMemoryRoutes()
	s.registerBackgroundRoutes()
	s.registerQueueRoutes()
	s.registerGoalRoutes()
	s.registerSubagentRoutes()
	s.registerHookRoutes()
	s.registerDocsRoutes()
	s.registerSchedulerRoutes()
	s.registerRewindRoute()
	s.registerChangesRoutes()
	s.registerWorkspaceViewerRoutes()
	s.registerSkillsManagementRoutes()
	s.registerMCPManagementRoutes()
}

// coddySessionArtifactGet streams only a manifest-registered immutable artifact.
func (s *Server) coddySessionArtifactGet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Range") != "" || r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":{"message":"artifact ranges are not supported"}}`, http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	artifactID := strings.TrimSpace(r.PathValue("artifactID"))
	if artifactID == "" || filepath.Base(artifactID) != artifactID {
		http.NotFound(w, r)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	a, path, err := session.ReadArtifact(st.GetPersistedSessionDir(), artifactID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		http.NotFound(w, r)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.NotFound(w, r)
		return
	}
	name := strings.ReplaceAll(strings.ReplaceAll(a.Name, "\r", "_"), "\n", "_")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, err := io.Copy(w, f); err != nil {
		s.log.Warn("stream session artifact", "error", err)
	}
}

// coddySessionArtifactPreviewGet serves only a verified image artifact inline.
// Non-image files remain download-only through the artifact route.
func (s *Server) coddySessionArtifactPreviewGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	artifactID := strings.TrimSpace(r.PathValue("artifactID"))
	if artifactID == "" || filepath.Base(artifactID) != artifactID {
		http.NotFound(w, r)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	a, path, err := session.ReadArtifact(st.GetPersistedSessionDir(), artifactID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		http.NotFound(w, r)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.NotFound(w, r)
		return
	}
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		http.NotFound(w, r)
		return
	}
	mimeType := http.DetectContentType(buf[:n])
	if !strings.HasPrefix(mimeType, "image/") {
		http.NotFound(w, r)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if _, err := io.Copy(w, f); err != nil {
		s.log.Warn("stream session artifact preview", "error", err)
	}
}

// coddySessionArtifactRevealPost asks the host desktop to reveal only the
// verified source path stored for this session artifact. The client supplies
// neither a path nor a command.
func (s *Server) coddySessionArtifactRevealPost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	artifactID := strings.TrimSpace(r.PathValue("artifactID"))
	if artifactID == "" || filepath.Base(artifactID) != artifactID {
		http.NotFound(w, r)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	path, err := session.ArtifactSourcePath(st.GetPersistedSessionDir(), st.GetCWD(), artifactID)
	if err != nil {
		if errors.Is(err, session.ErrArtifactSourceUnavailable) {
			http.Error(w, `{"error":{"message":"artifact source is unavailable"}}`, http.StatusGone)
			return
		}
		http.NotFound(w, r)
		return
	}
	if err := platform.RevealFile(path); err != nil {
		if errors.Is(err, platform.ErrRevealHeadless) || errors.Is(err, platform.ErrRevealUnsupported) {
			http.Error(w, `{"error":{"message":"artifact reveal is unavailable on this server"}}`, http.StatusServiceUnavailable)
			return
		}
		s.log.Warn("reveal session artifact", "error", err)
		http.Error(w, `{"error":{"message":"artifact reveal could not be started"}}`, http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) coddySessionCancelGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	hdr := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if hdr != "" && hdr != id {
		http.Error(w, `{"error":{"message":"X-Coddy-Session-ID does not match path id"}}`, http.StatusBadRequest)
		return
	}
	_ = s.mgr.WriteCrossProcessCancelRequest(id)
	if s.mgr.SessionByID(id) == nil {
		fs := s.mgr.FileStore()
		if fs == nil || !fs.HasPersistedSnapshot(id) {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		if _, err := s.mgr.HandleSessionLoad(r.Context(), acp.SessionLoadParams{
			SessionID: id,
		}); err != nil {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		if s.mgr.SessionByID(id) == nil {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
	}
	s.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: id})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.session_cancelled", "id": id})
}

func (s *Server) coddySessionPermissionPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	hdr := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if hdr != "" && hdr != id {
		http.Error(w, `{"error":{"message":"X-Coddy-Session-ID does not match path id"}}`, http.StatusBadRequest)
		return
	}
	var body struct {
		ToolCallID string `json:"toolCallId"`
		OptionID   string `json:"optionId"`
		Outcome    string `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	tcid := strings.TrimSpace(body.ToolCallID)
	if tcid == "" {
		http.Error(w, `{"error":{"message":"toolCallId is required"}}`, http.StatusBadRequest)
		return
	}
	opt := strings.TrimSpace(body.OptionID)
	out := strings.TrimSpace(body.Outcome)
	if opt == "" && out == "" {
		http.Error(w, `{"error":{"message":"optionId or outcome is required"}}`, http.StatusBadRequest)
		return
	}
	if out == "" {
		switch opt {
		case "reject":
			out = "cancelled"
		default:
			out = "allow"
		}
	}
	if opt == "" {
		if out == "cancelled" {
			opt = "reject"
		} else {
			opt = "allow"
		}
	}
	res := &acp.PermissionResult{
		Outcome:  out,
		OptionID: opt,
	}
	ok := CompletePermissionAnswer(id, tcid, res)
	if !ok {
		// A child session never owns a prompt of its own (its requests are
		// relayed to the parent chat), and a resume would build an agent on
		// it; a read-only transcript answers 409 instead of 404.
		if rejectSubagentTurn(w, s.persistedSessionState(r.Context(), id)) {
			return
		}
		if s.tryResumePendingPermission(r.Context(), id, tcid, res) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"error":{"message":"no pending permission for this toolCallId"}}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) coddySessionQuestionPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	hdr := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if hdr != "" && hdr != id {
		http.Error(w, `{"error":{"message":"X-Coddy-Session-ID does not match path id"}}`, http.StatusBadRequest)
		return
	}
	var body struct {
		RequestID string     `json:"requestId"`
		Answers   [][]string `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	rid := strings.TrimSpace(body.RequestID)
	if rid == "" {
		http.Error(w, `{"error":{"message":"requestId is required"}}`, http.StatusBadRequest)
		return
	}
	if body.Answers == nil {
		http.Error(w, `{"error":{"message":"answers is required"}}`, http.StatusBadRequest)
		return
	}
	ok := CompleteQuestionAnswer(id, rid, &acp.QuestionResult{Answers: body.Answers})
	if !ok {
		http.Error(w, `{"error":{"message":"no pending question for this requestId"}}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) coddyDescribePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}

	raw := strings.TrimSpace(body.Text)
	if raw == "" {
		http.Error(w, `{"error":{"message":"text is required"}}`, http.StatusBadRequest)
		return
	}

	// A text made only of settings commands configures the session and names
	// nothing: the answer is empty, and the chat keeps whatever name it has.
	text, ok := describePromptText(raw)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "coddy.describe",
			"short":  "",
			"tags":   jsonTagList(nil),
		})
		return
	}

	// Every text is asked about, however short. A first message of two words is
	// exactly the one that needs the model: "git status" is a usable title and
	// no filing at all, and the tags ride on this call - echoing the words back
	// would leave the shortest conversations the only unlabelled ones.
	commands := s.describeInvokedCommands(r, text)
	words := describeFallbackWords(text, commands)

	provider, err := s.providerFactory(s.activeCfg())
	if err != nil {
		s.log.Error("describe provider", "error", err)
		http.Error(w, `{"error":{"message":"LLM unavailable"}}`, http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	resp, err := provider.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: prompts.WithIdentity(describeSystemPrompt(commands))},
		{Role: llm.RoleUser, Content: text},
	}, nil)
	if err != nil {
		s.log.Error("describe llm", "error", err)
		http.Error(w, `{"error":{"message":"LLM error"}}`, http.StatusBadGateway)
		return
	}

	phraseLines, tags := describeSplitTagsLine(resp.Content)
	short := describePickPhraseFromLLM(describeDropCommandEchoes(phraseLines, commands), words)
	if short == "" {
		short = strings.Join(words[:min(3, len(words))], " ")
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.describe",
		"short":  short,
		"tags":   jsonTagList(tags),
	})
}

type coddyToolCallRow struct {
	ToolCallID             string                   `json:"toolCallId"`
	Name                   string                   `json:"name,omitempty"`
	Kind                   string                   `json:"kind,omitempty"`
	Status                 string                   `json:"status,omitempty"`
	StartedAt              string                   `json:"startedAt,omitempty"`
	FinishedAt             string                   `json:"finishedAt,omitempty"`
	ArgsPreview            string                   `json:"argsPreview,omitempty"`
	ResultPreview          string                   `json:"resultPreview,omitempty"`
	ResultPreviewTruncated bool                     `json:"resultPreviewTruncated,omitempty"`
	ResultTotalLines       int                      `json:"resultTotalLines,omitempty"`
	PlanSnapshot           []acp.PlanEntry          `json:"planSnapshot,omitempty"`
	Artifacts              []map[string]interface{} `json:"artifacts,omitempty"`
}

func artifactDTOs(sessionID string, artifacts []llm.Artifact) []map[string]interface{} {
	if sessionID == "" || len(artifacts) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(artifacts))
	for _, a := range artifacts {
		if a.ID == "" {
			continue
		}
		row := map[string]interface{}{"id": a.ID, "name": a.Name, "sha256": a.SHA256, "size": a.Size, "sourcePath": a.SourcePath, "relativePath": a.SourceRelativePath, "url": "/coddy/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(a.ID), "revealUrl": "/coddy/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(a.ID) + "/reveal"}
		if artifactImageName(a.Name) {
			row["previewUrl"] = "/coddy/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(a.ID) + "/preview"
		}
		out = append(out, row)
	}
	return out
}

func artifactImageName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".bmp")
}

func previewText(s string, max int) string {
	txt := strings.TrimSpace(s)
	if txt == "" {
		return ""
	}
	if max <= 0 || len(txt) <= max {
		return txt
	}
	return txt[:max] + "..."
}

func toolKind(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	if n == "" {
		return "tool"
	}
	if strings.HasPrefix(n, "coddy_todo_") {
		return "todo"
	}
	switch n {
	case "run_command":
		return "shell"
	case "write", "edit", "apply_patch", "mkdir", "touch", "mv":
		return "fs"
	}
	return "tool"
}

func coddyApplyResultPreview(row *coddyToolCallRow, full string) {
	snip, trunc, tl := session.PreviewToolResultSnippet(strings.TrimSpace(row.Name), full)
	row.ResultPreview = snip
	row.ResultPreviewTruncated = trunc
	row.ResultTotalLines = tl
}

// coddyLoadToolCallBundle resolves meta, args, full tool output from disk and in-memory transcript.
func (s *Server) coddyLoadToolCallBundle(st *session.State, sd, toolCallID string) (meta *session.ToolCallMeta, primaryName string, args string, fullResult string) {
	toolCallID = strings.TrimSpace(toolCallID)
	if sd != "" {
		if m, err := session.ReadToolCallMeta(sd, toolCallID); err == nil {
			meta = m
			if m != nil && strings.TrimSpace(m.Name) != "" {
				primaryName = strings.TrimSpace(m.Name)
			}
		}
		if a, err := session.ReadToolCallArgs(sd, toolCallID); err == nil {
			args = a
		}
		if res, err := session.ReadToolCallResult(sd, toolCallID); err == nil {
			fullResult = res
		}
	}
	if meta == nil || (args == "" && fullResult == "") {
		for _, m := range st.GetMessages() {
			if m.Role == llm.RoleAssistant {
				for _, tc := range m.ToolCalls {
					if tc.ID != toolCallID {
						continue
					}
					if primaryName == "" && strings.TrimSpace(tc.Name) != "" {
						primaryName = strings.TrimSpace(tc.Name)
					}
					if meta == nil {
						tmp := session.ToolCallMeta{
							ToolCallID: toolCallID,
							Name:       tc.Name,
							Kind:       toolKind(tc.Name),
							Status:     "pending",
						}
						meta = &tmp
					}
					if args == "" {
						args = tc.InputJSON
					}
				}
			}
			if m.Role == llm.RoleTool && m.ToolCallID == toolCallID {
				if fullResult == "" {
					fullResult = m.Content
				}
				if meta != nil && meta.Status == "pending" {
					meta.Status = "completed"
				}
			}
		}
	}
	if meta != nil && primaryName == "" && strings.TrimSpace(meta.Name) != "" {
		primaryName = strings.TrimSpace(meta.Name)
	}
	return meta, primaryName, args, fullResult
}

func (s *Server) coddyToolCallsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	msgs := st.GetMessages()
	// A client holding one page of the history asks for the calls its
	// messages issued, so only their files are read from disk.
	from, _, err := queryIndex(r, "from")
	if err != nil {
		writePageQueryError(w, err)
		return
	}
	to, hasTo, err := queryIndex(r, "to")
	if err != nil {
		writePageQueryError(w, err)
		return
	}
	if hasTo && from > to {
		writePageQueryError(w, errors.New("from must not be after to"))
		return
	}
	if !hasTo || to > len(msgs) {
		to = len(msgs)
	}
	msgs = msgs[min(from, to):to]

	type ent struct {
		row coddyToolCallRow
	}
	ordered := make([]ent, 0)
	idx := map[string]int{}

	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if strings.TrimSpace(tc.ID) == "" {
					continue
				}
				if _, ok := idx[tc.ID]; ok {
					continue
				}
				idx[tc.ID] = len(ordered)
				ordered = append(ordered, ent{
					row: coddyToolCallRow{
						ToolCallID:  tc.ID,
						Name:        tc.Name,
						Kind:        toolKind(tc.Name),
						Status:      "pending",
						ArgsPreview: previewText(tc.InputJSON, 200),
					},
				})
			}
		}
		if m.Role == llm.RoleTool && strings.TrimSpace(m.ToolCallID) != "" {
			i, ok := idx[m.ToolCallID]
			if !ok {
				idx[m.ToolCallID] = len(ordered)
				ordered = append(ordered, ent{row: coddyToolCallRow{ToolCallID: m.ToolCallID}})
				i = idx[m.ToolCallID]
			}
			ordered[i].row.Status = "completed"
			coddyApplyResultPreview(&ordered[i].row, m.Content)
			ordered[i].row.Artifacts = artifactDTOs(id, m.Artifacts)
		}
	}

	if sd != "" {
		for i := range ordered {
			id := ordered[i].row.ToolCallID
			if meta, err := session.ReadToolCallMeta(sd, id); err == nil && meta != nil {
				if strings.TrimSpace(meta.Name) != "" {
					ordered[i].row.Name = meta.Name
				}
				if strings.TrimSpace(meta.Kind) != "" {
					ordered[i].row.Kind = meta.Kind
				}
				if strings.TrimSpace(meta.Status) != "" {
					ordered[i].row.Status = meta.Status
				}
				ordered[i].row.StartedAt = meta.StartedAt
				ordered[i].row.FinishedAt = meta.FinishedAt
				ordered[i].row.PlanSnapshot = append([]acp.PlanEntry(nil), meta.PlanSnapshot...)
			}
			if args, err := session.ReadToolCallArgs(sd, id); err == nil {
				ordered[i].row.ArgsPreview = previewText(args, 200)
			}
			if res, err := session.ReadToolCallResult(sd, id); err == nil {
				coddyApplyResultPreview(&ordered[i].row, res)
			}
		}
	}

	outRows := make([]coddyToolCallRow, 0, len(ordered))
	for _, e := range ordered {
		outRows = append(outRows, e.row)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.tool_calls",
		"sessionId": id,
		"toolCalls": outRows,
	})
}

func (s *Server) coddyToolCallGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	toolCallID := strings.TrimSpace(r.PathValue("toolCallId"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())

	meta, _, args, full := s.coddyLoadToolCallBundle(st, sd, toolCallID)

	payload := map[string]interface{}{
		"object":     "coddy.tool_call",
		"sessionId":  id,
		"toolCallId": toolCallID,
		"meta":       meta,
		"args":       args,
		"result":     full,
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == toolCallID {
			payload["artifacts"] = artifactDTOs(id, m.Artifacts)
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) coddySessionStatsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"stats unavailable"}}`, http.StatusServiceUnavailable)
		return
	}
	stats, err := session.ReadSessionStats(sd)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object":        "coddy.session_stats",
				"sessionId":     id,
				"stats":         nil,
				"contextWindow": s.sessionContextWindow(st),
			})
			return
		}
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}
	if live := st.GetLastContextBreakdown(); live != nil {
		cp := *live
		stats.ContextBreakdown = &cp
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_stats",
		"sessionId": id,
		"stats":     stats,
		// The window the breakdown above is a share of, named with the model
		// it belongs to. A provider listing can answer after GET /v1/models
		// served its fallback, and a client that was looking at another
		// session missed the usage_update that carried the correction; asking
		// the session itself is how it catches up without waiting for the next
		// turn.
		"contextWindow": s.sessionContextWindow(st),
	})
}

// sessionContextWindow is the window a session measures against, resolved the
// way every other reader resolves it, plus the model it belongs to so a client
// can tell it apart from the window of a model the composer has since switched
// to.
func (s *Server) sessionContextWindow(st *session.State) map[string]interface{} {
	cfg := s.activeCfg()
	if cfg == nil || st == nil {
		return nil
	}
	tokens, source := st.ContextWindow(cfg)
	if tokens <= 0 {
		return nil
	}
	return map[string]interface{}{
		"model":  st.EffectiveModelID(cfg),
		"tokens": tokens,
		"source": source,
	}
}

func (s *Server) coddyRequireStore(w http.ResponseWriter) *session.FileStore {
	fs := s.mgr.FileStore()
	if fs == nil || fs.Root == "" {
		http.Error(w, `{"error":{"message":"session store unavailable"}}`, http.StatusServiceUnavailable)
		return nil
	}
	return fs
}

func coddyMustSession(w http.ResponseWriter, s *session.Manager, id string, loadFromDisk func() (*session.State, error)) *session.State {
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return nil
	}
	if st := s.SessionByID(id); st != nil {
		return st
	}
	st, err := loadFromDisk()
	if err != nil {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return nil
	}
	return st
}

func (s *Server) coddyEnsureLoaded(w http.ResponseWriter, r *http.Request, id string) *session.State {
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return nil
	}
	load := func() (*session.State, error) {
		if !fs.HasPersistedSnapshot(id) {
			return nil, errSessionNotFound
		}
		// No cwd: a stored session belongs to the folder it was started in, and
		// a load that carried this server's own cwd would rebind it - quietly
		// moving somebody's conversation to another checkout, and rewriting the
		// bundle, which moves the session in a listing ordered by when it last
		// changed. The load falls back to the default for a bundle with none.
		_, err := s.mgr.HandleSessionLoad(r.Context(), acp.SessionLoadParams{
			SessionID: id,
		})
		if err != nil {
			return nil, err
		}
		return s.mgr.SessionByID(id), nil
	}
	return coddyMustSession(w, s.mgr, id, load)
}

func parseLimitCursor(q url.Values) (limit, offset int) {
	limit = 50
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	if v := strings.TrimSpace(q.Get("cursor")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func (s *Server) coddySessionsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	includeScheduler := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_scheduler")), "true")
	includeSubagents := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_subagents")), "true")
	archived, ok := session.ParseArchiveFilter(r.URL.Query().Get("archived"))
	if !ok {
		http.Error(w, `{"error":{"message":"archived must be \"exclude\", \"only\" or \"all\""}}`, http.StatusBadRequest)
		return
	}
	origin, ok := session.ParseOriginFilter(r.URL.Query().Get("origin"))
	if !ok {
		http.Error(w, `{"error":{"message":"origin must be \"local\" or \"gateway\""}}`, http.StatusBadRequest)
		return
	}
	sortKey, ok := session.ParseSortKey(r.URL.Query().Get("sort"))
	if !ok {
		http.Error(w, `{"error":{"message":"sort must be \"updated\", \"created\", \"title\", \"messages\" or \"tokens\""}}`, http.StatusBadRequest)
		return
	}
	sortOrder, ok := session.ParseSortOrder(r.URL.Query().Get("order"))
	if !ok {
		http.Error(w, `{"error":{"message":"order must be \"asc\" or \"desc\""}}`, http.StatusBadRequest)
		return
	}
	listOpts := session.ListOptions{
		CWD:                  strings.TrimSpace(r.URL.Query().Get("cwd")),
		IncludeSchedulerRuns: includeScheduler,
		IncludeSubagents:     includeSubagents,
		Archived:             archived,
		Tags:                 session.ParseTagList(r.URL.Query().Get("tags")),
		Origin:               origin,
		// A conversation nobody wrote in stays out of History (issue #357),
		// except while its first turn runs: that turn appends the prompt only
		// once its MCP servers and its model's context window are in, and
		// the chat is in History from its first send. Decided below, where
		// the activity of a session is known.
		IncludeEmpty: true,
	}
	rows, err := fs.ListSnapshotsWith(listOpts)
	if err != nil {
		s.log.Error("coddy sessions list", "error", err)
		http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
		return
	}
	// The rail badge is global to History, not to the page or any filter the
	// reader currently has open. Active child sessions contribute even though
	// History itself keeps their rows hidden.
	historyRows := rows
	if !isNormalHistoryList(listOpts) || !listOpts.IncludeSubagents {
		historyRows, err = fs.ListSnapshotsWith(session.ListOptions{IncludeSubagents: true, IncludeEmpty: true})
		if err != nil {
			s.log.Error("coddy sessions active count", "error", err)
			http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
			return
		}
	}
	turnActive := func(id string) bool {
		return s.mgr.SessionTurnActiveInProcess(id) || session.TurnLockHeld(fs.SessionPath(id))
	}
	activeCount := 0
	for _, row := range historyRows {
		if turnActive(row.SessionID) {
			activeCount++
		}
	}
	kept := rows[:0]
	for _, row := range rows {
		if !row.HoldsNoMessage() || turnActive(row.SessionID) {
			kept = append(kept, row)
		}
	}
	rows = kept
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		rows, err = fs.FilterSnapshotListForSearch(rows, q)
		if err != nil {
			s.log.Error("coddy sessions list filter", "error", err)
			http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
			return
		}
	}
	// The order is applied to the whole filtered listing, never to the page:
	// paging is an offset into the sorted result, so a client that asks for
	// page two of a title sort gets the titles that follow page one.
	// The token totals live in a file of their own, so they are read only when
	// that is the column being sorted by, and memoized for the rows that repeat.
	var tokensOf func(string) int
	if sortKey == session.SortTokens {
		cache := make(map[string]int, len(rows))
		tokensOf = func(id string) int {
			if total, seen := cache[id]; seen {
				return total
			}
			total := coddySessionTokenUsage(fs, id)["totalTokens"]
			cache[id] = total
			return total
		}
	}
	if sortKey == session.SortMessages {
		// A message sort compares every candidate, so legacy/stale count
		// metadata is enriched before the whole-list sort. Default History
		// never calls this and therefore never opens transcripts for counts.
		fs.EnrichMessageCounts(rows)
	}
	session.SortSessionList(rows, sortKey, sortOrder, tokensOf)

	limit, offset := parseLimitCursor(r.URL.Query())
	start := offset
	if start >= len(rows) {
		out := map[string]interface{}{
			"object":       "coddy.session_list",
			"sessions":     []interface{}{},
			"nextCursor":   nil,
			"hasMore":      false,
			"active_count": activeCount,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	slice := rows[start:end]
	includeActivity := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_activity")), "true")
	includeStats := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_stats")), "true")
	if includeStats {
		// Statistics are emitted for page rows only, so legacy/stale transcript
		// counts are decoded only after sorting and paging have selected them.
		fs.EnrichMessageCounts(slice)
	}
	// One walk of the task pool for the whole listing, rather than one per row.
	var backgroundRunning map[string]int
	if includeActivity {
		backgroundRunning = bgtask.Default().RunningCountsBySession()
	}
	sessions := make([]map[string]interface{}, 0, len(slice))
	repoRoots := make(map[string]string)
	for _, row := range slice {
		ent := map[string]interface{}{
			"id": row.SessionID,
		}
		if row.Title != "" {
			ent["title"] = row.Title
		}
		if row.UpdatedAt != "" {
			ent["updatedAt"] = row.UpdatedAt
		}
		if row.CWD != "" {
			ent["cwd"] = row.CWD
			root, seen := repoRoots[row.CWD]
			if !seen {
				root = sessionRepoRoot(row.CWD)
				repoRoots[row.CWD] = root
			}
			if root != "" {
				ent["repoRoot"] = root
			}
		}
		if len(row.Tags) > 0 {
			ent["tags"] = row.Tags
		}
		if row.Archived {
			ent["archived"] = true
			if row.ArchivedAt != "" {
				ent["archivedAt"] = row.ArchivedAt
			}
		}
		if row.Origin != "" {
			ent["origin"] = row.Origin
		}
		if row.Pinned {
			ent["pinned"] = true
			if row.PinnedAt != "" {
				ent["pinnedAt"] = row.PinnedAt
			}
		}
		if includeSubagents {
			if link := subagentRowLink(row); link != nil {
				ent["subagent"] = link
			}
		}
		if includeStats {
			// createdAt is absent for a bundle stored before the field existed;
			// model is absent for a session that never overrode the configured
			// default. Both stay out of the row rather than being guessed, so a
			// table can render an explicit "unknown" for them.
			if row.CreatedAt != "" {
				ent["createdAt"] = row.CreatedAt
			}
			if row.Model != "" {
				ent["model"] = row.Model
			}
			ent["messageCount"] = row.MessageCount
			ent["tokenUsage"] = coddySessionTokenUsage(fs, row.SessionID)
		}
		if includeActivity {
			dir := fs.SessionPath(row.SessionID)
			turnActive := s.mgr.SessionTurnActiveInProcess(row.SessionID) || session.TurnLockHeld(dir)
			actSeq, readSeq, lastErrorSeq, _ := fs.ReadDiskActivity(row.SessionID)
			ent["turnActive"] = turnActive
			ent["activitySeq"] = actSeq
			ent["readActivitySeq"] = readSeq
			ent["lastErrorSeq"] = lastErrorSeq
			ent["unreadComplete"] = actSeq > readSeq && !turnActive
			ent["permissionPending"] = session.PendingPermissionHeld(dir)
			ent["questionPending"] = QuestionPending(row.SessionID)
			// Detached work outlives the turn that started it, so a session
			// with no turn in flight is still not idle while a task runs.
			// The count is the pool's own, which already leaves out the
			// runtime's system errands and everything that has finished; the
			// whole listing reads it in one pass, above.
			ent["backgroundRunning"] = backgroundRunning[row.SessionID]
		}
		sessions = append(sessions, ent)
	}
	var nextCursor interface{}
	if end < len(rows) {
		nextCursor = strconv.Itoa(end)
	}
	out := map[string]interface{}{
		"object":       "coddy.session_list",
		"sessions":     sessions,
		"nextCursor":   nextCursor,
		"hasMore":      end < len(rows),
		"active_count": activeCount,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// isNormalHistoryList reports whether opts are the unfiltered working History
// scope used by active_count. Keeping it here makes the single-scan path
// explicit without changing the badge semantics for filtered requests.
func isNormalHistoryList(opts session.ListOptions) bool {
	return strings.TrimSpace(opts.CWD) == "" &&
		!opts.IncludeSchedulerRuns &&
		!opts.IncludeSubagents &&
		opts.Archived == session.ArchiveExclude &&
		len(opts.Tags) == 0 &&
		opts.Origin == session.OriginAny
}

// coddySessionTokenUsage reads the provider token totals a session accumulated.
// A bundle with no stats.json yet (a chat that never completed a model call)
// reports zeroes rather than nothing, so every row of the table has the same
// shape and sorts numerically.
func coddySessionTokenUsage(fs *session.FileStore, id string) map[string]int {
	usage := map[string]int{"inputTokens": 0, "outputTokens": 0, "totalTokens": 0}
	stats, err := session.ReadSessionStats(fs.SessionPath(id))
	if err != nil || stats == nil {
		return usage
	}
	usage["inputTokens"] = stats.TokenUsageTotal.InputTokens
	usage["outputTokens"] = stats.TokenUsageTotal.OutputTokens
	usage["totalTokens"] = stats.TokenUsageTotal.TotalTokens
	return usage
}

func (s *Server) coddySessionActivityGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	if !fs.HasPersistedSnapshot(id) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return
	}
	dir := fs.SessionPath(id)
	turnActive := s.mgr.SessionTurnActiveInProcess(id) || session.TurnLockHeld(dir)
	actSeq, readSeq, lastErrorSeq, err := fs.ReadDiskActivity(id)
	if err != nil {
		s.log.Error("coddy session activity", "error", err)
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}
	out := map[string]interface{}{
		"object":          "coddy.session_activity",
		"sessionId":       id,
		"turnActive":      turnActive,
		"activitySeq":     actSeq,
		"readActivitySeq": readSeq,
		"lastErrorSeq":    lastErrorSeq,
		"unreadComplete":  actSeq > readSeq && !turnActive,
		"questionPending": QuestionPending(id),
	}
	s.addTurnProgress(out, id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// addTurnProgress puts the clock and the token count of the turn id is running in this
// process into an activity answer. A client that joins the turn late reads them here: the
// composer relay does not replay a turn_progress frame the client's transcript snapshot
// already covers. A turn held by another process leaves the fields out.
func (s *Server) addTurnProgress(out map[string]interface{}, id string) {
	startedAt, ok := s.mgr.TurnStartedAt(id)
	if !ok {
		return
	}
	out["turnStartedAt"] = startedAt.UTC().Format(time.RFC3339Nano)
	// The age is taken before the count is read, and the loop dates a frame after it
	// stored the count (agent/turn_progress.go): a client that orders the two by age
	// never takes an answer that saw the older count for the newer one.
	elapsed := time.Since(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	out["turnElapsedMs"] = elapsed.Milliseconds()
	// A turn finished background tasks started carries the tasks, in the shape
	// of the background_wake frame: a client resuming the session mid-turn
	// learns that nobody typed it, and a console over --remote follows it.
	if wake := s.mgr.TurnWake(id); wake != nil {
		out["backgroundWake"] = map[string]interface{}{"tasks": session.BackgroundWakeUpdate(wake).Tasks}
	}
	if st := s.mgr.SessionByID(id); st != nil {
		if progress, running := st.TurnProgress(); running {
			out["turnOutputTokens"] = progress.OutputTokens
			out["turnTokensEstimated"] = progress.Estimated
		}
	}
}

func llmMsgsToCoddyOpenAI(msgs []llm.Message) []map[string]interface{} {
	return llmMsgsToCoddyOpenAIForSession("", "", msgs)
}

// isRegularFile reports whether path is a regular file, a link not followed.
func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// recordedAsset is the name under assetsDir of the copy a part recorded at
// path: its base name, when path was in this assets directory or in the one
// the bundle had before it moved. A path anywhere else names no copy: its base
// name would either answer 404 or, worse, name a different file that happens
// to share it.
func recordedAsset(assetsDir, path string) string {
	if assetsDir == "" || path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if dir != filepath.Clean(assetsDir) && filepath.Base(dir) != filepath.Base(session.AssetsPath("")) {
		return ""
	}
	return filepath.Base(path)
}

func llmMsgsToCoddyOpenAIForSession(sessionID, assetsDir string, msgs []llm.Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		item := map[string]interface{}{
			"role":    string(m.Role),
			"content": m.Content,
		}
		if strings.TrimSpace(m.Reasoning) != "" {
			item["reasoning"] = m.Reasoning
		}
		if m.ReasoningDurationMs > 0 {
			item["reasoning_duration_ms"] = m.ReasoningDurationMs
		}
		if m.Role == llm.RoleTool && m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if len(m.Artifacts) > 0 {
			item["artifacts"] = artifactDTOs(sessionID, m.Artifacts)
		}
		if len(m.ToolCalls) > 0 {
			tc := make([]map[string]interface{}, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				tc = append(tc, map[string]interface{}{
					"id":   c.ID,
					"type": "function",
					"function": map[string]string{
						"name":      c.Name,
						"arguments": c.InputJSON,
					},
				})
			}
			item["tool_calls"] = tc
		}
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Model) != "" {
			item["model"] = strings.TrimSpace(m.Model)
		}
		if cat := strings.TrimSpace(m.CreatedAt); cat != "" {
			item["created_at"] = cat
		}
		if m.CompactionSummary {
			item["compaction_summary"] = true
		}
		if m.Role == llm.RoleUser && m.BackgroundWake != nil {
			// Nobody typed this message: a woken turn opened with it.
			item["background_wake"] = m.BackgroundWake
		}
		if m.Role == llm.RoleUser && m.GoalTurn != nil {
			// Nobody typed this message either: the supervisor opened a
			// goal turn with it.
			item["goal_turn"] = m.GoalTurn
		}
		// A prompt's attachments, and the pictures a tool call showed the
		// model (read on an image file), which stay on that call's result.
		if (m.Role == llm.RoleUser || m.Role == llm.RoleTool) && len(m.ImageParts) > 0 {
			files := make([]map[string]interface{}, 0, len(m.ImageParts))
			for _, part := range m.ImageParts {
				name := strings.TrimSpace(part.Name)
				if name == "" && part.FilePath != "" {
					name = filepath.Base(part.FilePath)
				}
				file := map[string]interface{}{
					"name":      name,
					"mime_type": imagePartMIMEType(part),
				}
				// Both addresses are names under this session's assets directory,
				// given only for a regular file there, so a card never loads a
				// missing one. Symlinks do not count: the address promises bytes
				// of this session's bundle, and a link planted in that directory -
				// the agent can write there, and the prompt tells it where - would
				// make it serve whatever it points at.
				if asset := recordedAsset(assetsDir, part.FilePath); sessionID != "" && asset != "" {
					if part.ThumbnailPath != "" && isRegularFile(session.ThumbnailPathInAssets(assetsDir, asset)) {
						file["preview_url"] = session.AssetThumbnailRoute(sessionID, asset)
					}
					// The full-size original, for a preview card to open enlarged.
					if isRegularFile(filepath.Join(assetsDir, asset)) {
						file["url"] = session.AssetRoute(sessionID, asset)
					}
				}
				files = append(files, file)
			}
			item["files"] = files
		}
		if m.PlanDocument != nil {
			item["plan_document"] = map[string]interface{}{
				"slug":      m.PlanDocument.Slug,
				"name":      m.PlanDocument.Name,
				"overview":  m.PlanDocument.Overview,
				"content":   m.PlanDocument.Content,
				"body":      m.PlanDocument.Body,
				"path":      m.PlanDocument.Path,
				"discarded": m.PlanDocument.Discarded,
				"updatedAt": m.PlanDocument.UpdatedAt,
			}
		}
		out = append(out, item)
	}
	return out
}

func imagePartMIMEType(part llm.ImagePart) string {
	if part.MIMEType != "" {
		return part.MIMEType
	}
	if strings.HasPrefix(part.DataURL, "data:") {
		end := strings.IndexAny(part.DataURL[5:], ";,")
		if end >= 0 {
			raw := part.DataURL[5 : 5+end]
			if mediaType, _, err := mime.ParseMediaType(raw); err == nil && mediaType != "" {
				return mediaType
			}
		}
	}
	for _, name := range []string{part.Name, part.FilePath} {
		if mediaType := mime.TypeByExtension(filepath.Ext(name)); mediaType != "" {
			if base, _, err := mime.ParseMediaType(mediaType); err == nil {
				return base
			}
			return mediaType
		}
	}
	return "application/octet-stream"
}

// coddySessionAsset is the prologue the two asset routes share: the method,
// the session behind {id}, and an asset {name} that must be a bare file name.
// It answers the request itself and reports ok=false when the caller must stop.
func (s *Server) coddySessionAsset(w http.ResponseWriter, r *http.Request) (sessionDir, name string, ok bool) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return "", "", false
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return "", "", false
	}
	name = strings.TrimSpace(r.PathValue("name"))
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		http.Error(w, `{"error":{"message":"invalid asset name"}}`, http.StatusBadRequest)
		return "", "", false
	}
	sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	if sessionDir == "" {
		http.NotFound(w, r)
		return "", "", false
	}
	return sessionDir, name, true
}

// coddySessionAssetGet serves the original bytes of an uploaded asset, which is
// what a preview card opens enlarged - the thumbnail beside it is bounded to a
// 160px edge and has nothing to enlarge. Only images leave the bundle, and the
// file name never decides that: the first bytes are sniffed, so a text file
// called photo.png is a 404 like any other non-image.
func (s *Server) coddySessionAssetGet(w http.ResponseWriter, r *http.Request) {
	sessionDir, name, ok := s.coddySessionAsset(w, r)
	if !ok {
		return
	}
	path := filepath.Join(session.AssetsPath(sessionDir), name)
	// The name is already a bare one, so the only way out of the bundle left is a
	// link inside it, and the agent can write there. Refuse anything that is not a
	// regular file rather than follow it.
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("open session asset", "error", err)
		http.Error(w, `{"error":{"message":"asset unavailable"}}`, http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		s.log.Error("read session asset", "error", err)
		http.Error(w, `{"error":{"message":"asset unavailable"}}`, http.StatusInternalServerError)
		return
	}
	mediaType := http.DetectContentType(head[:n])
	if !strings.HasPrefix(mediaType, "image/") {
		http.NotFound(w, r)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.log.Error("rewind session asset", "error", err)
		http.Error(w, `{"error":{"message":"asset unavailable"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) coddySessionAssetThumbnailGet(w http.ResponseWriter, r *http.Request) {
	sessionDir, name, ok := s.coddySessionAsset(w, r)
	if !ok {
		return
	}
	path := session.AssetThumbnailPath(sessionDir, name)
	// Same reason as the full-size route: a link planted in the bundle must not
	// turn this into a reader of whatever it points at.
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("open session asset thumbnail", "error", err)
		http.Error(w, `{"error":{"message":"thumbnail unavailable"}}`, http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name+".png", info.ModTime(), f)
}

// maxMessagePage bounds the limit of a paged read of a history.
const maxMessagePage = 1000

// messagePageQuery reads the window a GET /coddy/sessions/{id}/messages names:
// limit and before for a page ending at before (the end when absent), or from
// for a window running from a message to before or the end. Without any of
// them it is the whole history. Positions past the history are clamped by
// session.PageMessages; a value that is not a non-negative integer is refused.
func messagePageQuery(r *http.Request) (session.MessagePageQuery, error) {
	q := session.MessagePageQuery{From: -1, Before: -1}
	from, hasFrom, err := queryIndex(r, "from")
	if err != nil {
		return q, err
	}
	before, hasBefore, err := queryIndex(r, "before")
	if err != nil {
		return q, err
	}
	limit, hasLimit, err := queryIndex(r, "limit")
	if err != nil {
		return q, err
	}
	if hasLimit && (limit < 1 || limit > maxMessagePage) {
		return q, fmt.Errorf("limit must be between 1 and %d", maxMessagePage)
	}
	if hasFrom && hasLimit {
		return q, errors.New("from and limit cannot be combined")
	}
	// A page ending somewhere names its size or its start: before alone would
	// read the whole prefix, the very read paging exists to avoid.
	if hasBefore && !hasFrom && !hasLimit {
		return q, errors.New("before needs limit or from")
	}
	if hasFrom {
		q.From = from
	}
	if hasBefore {
		q.Before = before
	}
	q.Limit = limit
	return q, nil
}

// queryIndex reads a non-negative integer query parameter; ok is false when it
// is absent or empty.
func queryIndex(r *http.Request, name string) (n int, ok bool, err error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, false, nil
	}
	n, err = strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return n, true, nil
}

func writePageQueryError(w http.ResponseWriter, err error) {
	http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
}

// messageMCPActivationRequested recognizes the single transcript read the
// SPA uses when it selects a session. Activation is deliberately unavailable
// to older or rebased history reads and requires the session header to bind the request
// to the selected chat rather than merely its URL path.
func messageMCPActivationRequested(r *http.Request, id string) (bool, error) {
	q := r.URL.Query()
	raw, present := q["activate_mcp"]
	if !present {
		return false, nil
	}
	if len(raw) != 1 || raw[0] != "1" {
		return false, errors.New("activate_mcp must be 1")
	}
	for _, name := range []string{"before", "from"} {
		if _, paged := q[name]; paged {
			return false, errors.New("activate_mcp is only valid on an initial transcript read")
		}
	}
	if strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")) != id {
		return false, errors.New("activate_mcp requires X-Coddy-Session-ID matching the path id")
	}
	return true, nil
}

func (s *Server) coddySessionMessagesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	activateMCP, err := messageMCPActivationRequested(r, id)
	if err != nil {
		writePageQueryError(w, err)
		return
	}
	query, err := messagePageQuery(r)
	if err != nil {
		writePageQueryError(w, err)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	if activateMCP {
		if err := s.mgr.ActivateDeferredMCP(r.Context(), id); err != nil {
			s.log.Warn("deferred MCP activation did not start with transcript read",
				"session", id, "error", err)
		}
	}
	msgs, rev := st.MessagesWithRev()
	page := session.PageMessages(msgs, query)
	out := map[string]interface{}{
		"object":    "coddy.messages",
		"sessionId": id,
		"messages":  llmMsgsToCoddyOpenAIForSession(id, session.AssetsPath(st.GetPersistedSessionDir()), msgs[page.Offset:page.End]),
		// The revision this history was read at: a client attaching to the composer
		// relay passes it back as since_rev and is replayed only what it lacks.
		"messagesRev": rev,
		// Where the returned messages sit in the history, and the counts a
		// client numbers their rows from: a prompt's index for a rewind is
		// turnsBefore plus its position among the page's prompts, and uiLog
		// rows are numbered past userRowsBefore.
		"window": map[string]int{
			"offset":         page.Offset,
			"total":          page.Total,
			"turnsBefore":    page.TurnsBefore,
			"userRowsBefore": page.UserRowsBefore,
		},
	}
	// A child session is a read-only transcript: the SPA drops the composer
	// and links back to the parent chat and to the task in its drawer, or to
	// the job's runs for a scheduled run. The session of a scheduler job is
	// read-only too, and names its job.
	if meta := st.Subagent(); meta != nil {
		out["readOnly"] = true
		out["subagent"] = subagentMetaLink(meta)
	} else if st.IsSchedulerJob() {
		out["readOnly"] = true
		job := map[string]interface{}{"jobId": st.GetSchedulerJobID()}
		if ws := st.GetSchedulerJobWorkspace(); ws != "" {
			job["workspace"] = ws
		}
		out["schedulerJob"] = job
	}
	// An archived session is where the composer learns it must not offer a
	// prompt. It cannot be read off the session listing: that skips the archive,
	// so the conversation on screen may be in no page the client holds.
	if archived, _ := st.ArchiveState(); archived {
		out["archived"] = true
	}
	// The last rewind can still be taken back: the client offers Undo on the
	// prompt it edited.
	if idx, ok := s.mgr.RewindUndoAvailable(id); ok {
		out["rewindUndo"] = map[string]int{"userMessageIndex": idx}
	}
	if s.activeCfg() != nil {
		out["selectedModelId"] = strings.TrimSpace(st.GetSelectedModelID())
		out["model"] = effectiveYAMLModel(s.activeCfg(), st)
		out["selectedReasoning"] = st.EffectiveReasoning(s.activeCfg())
		out["mode"] = string(st.GetMode())
		// The whole snapshot, versioned: what the composer mirrors and what
		// it names in metadata.settingsVersion when it sends.
		if snap, err := s.mgr.SessionSettings(id); err == nil {
			out["settings"] = snap
		}
	}
	// The session goal, versioned like the session_goal frames: null when
	// there is none.
	if u, err := s.mgr.SessionGoal(id); err == nil {
		out["goal"] = goalPayload(u)
	}
	// A session saved before only the agent's own settings changes were
	// noted keeps the notices of the operator's: they are not shown.
	if u := page.UILog(msgs, session.VisibleUILog(msgs, st.GetUILog())); len(u) > 0 {
		rows := make([]map[string]interface{}, 0, len(u))
		for _, e := range u {
			rows = append(rows, map[string]interface{}{
				"id":            e.ID,
				"level":         e.Level,
				"message":       e.Message,
				"userTurnIndex": e.UserTurnIndex,
				"createdAt":     e.CreatedAt,
			})
		}
		out["uiLog"] = rows
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) coddySessionPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Title string `json:"title"`
		// TitleIfUnpinned marks a title nobody typed: the phrase the describe
		// call proposes for a new chat. It lands only while the session has no
		// pinned title of its own, so a name the operator or the model wrote
		// during that first turn is not overwritten seconds later by an answer
		// that was already in flight.
		TitleIfUnpinned   bool    `json:"titleIfUnpinned"`
		MarkActivityRead  bool    `json:"markActivityRead"`
		SelectedModelID   *string `json:"selectedModelId"`
		SelectedReasoning *string `json:"selectedReasoning"`
		// Mode and PermissionMode are the operating mode and the permission
		// mode; Turns > 0 changes the settings of this request for that many
		// turns instead of for the session (the --count of a command).
		Mode           *string   `json:"mode"`
		PermissionMode *string   `json:"permissionMode"`
		Turns          int       `json:"turns"`
		Tags           *[]string `json:"tags"`
		Archived       *bool     `json:"archived"`
		Pinned         *bool     `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	fs := s.mgr.FileStore()
	resp := map[string]interface{}{
		"object": "coddy.session_patched",
		"id":     id,
	}
	did := false
	// The settings go through the manager's setter, like every other
	// surface's, so the change is validated once, logged, and mirrored by
	// every client watching the session (event: session_settings).
	change := session.SettingsChange{Source: "web", Turns: body.Turns}
	clearModel := false
	if body.SelectedModelID != nil {
		mid := strings.TrimSpace(*body.SelectedModelID)
		switch {
		case mid == "":
			// Clearing the selection goes back to the configured agent model.
			clearModel = true
		case s.activeCfg() == nil || s.activeCfg().FindModelEntry(mid) == nil:
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, ErrUnknownMetadataModel.Error()), http.StatusBadRequest)
			return
		default:
			change.Model = &mid
		}
	}
	if body.SelectedReasoning != nil {
		level := strings.TrimSpace(*body.SelectedReasoning)
		if change.Model == nil {
			if err := applySessionReasoning(s.activeCfg(), st, level); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
		}
		change.Reasoning = &level
	}
	change.Mode = body.Mode
	change.PermissionMode = body.PermissionMode
	if clearModel {
		if body.Turns > 0 {
			http.Error(w, `{"error":{"message":"selectedModelId cannot be cleared for a number of turns"}}`, http.StatusBadRequest)
			return
		}
		st.SetSelectedModelID("")
		st.ClearTurnOverride(session.SettingModel)
	}
	if !change.Empty() || clearModel {
		var snap acp.SessionSettings
		var err error
		if change.Empty() {
			snap = s.mgr.PublishSessionSettings(id, st, "", "web")
		} else {
			snap, err = s.mgr.ApplySessionSettings(r.Context(), id, change)
		}
		if err != nil {
			code := http.StatusBadRequest
			if isSubagentReadOnly(err) {
				code = http.StatusConflict
			}
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), code)
			return
		}
		did = true
		resp["settings"] = snap
		if body.SelectedModelID != nil {
			resp["selectedModelId"] = strings.TrimSpace(st.GetSelectedModelID())
			if s.activeCfg() != nil {
				resp["model"] = effectiveYAMLModel(s.activeCfg(), st)
			}
		}
		if body.SelectedReasoning != nil {
			resp["selectedReasoning"] = strings.TrimSpace(st.GetSelectedReasoning())
		}
	}
	if body.MarkActivityRead {
		did = true
		if fs != nil {
			activitySeq, readActivitySeq, lastErrorSeq, err := fs.MarkSessionActivityRead(id, st)
			if err != nil {
				s.log.Warn("patch session meta activity", "id", id, "error", err)
				st.MarkActivityReadSynced()
			} else {
				st.RestoreActivityFromSnapshot(activitySeq, readActivitySeq, lastErrorSeq)
			}
		} else {
			st.MarkActivityReadSynced()
		}
		resp["activitySeq"] = st.GetActivitySeq()
		resp["readActivitySeq"] = st.GetReadActivitySeq()
		resp["lastErrorSeq"] = st.GetLastErrorSeq()
	}
	// The same folding and the same limit the agent's session_describe writes
	// through: a title is a row of a list whichever surface typed it, and two
	// vocabularies for one field is how they drift apart.
	t := session.NormalizeTitle(body.Title)
	if t != "" {
		if length, tooLong := session.TitleTooLong(t); tooLong {
			http.Error(w, fmt.Sprintf(
				`{"error":{"message":"title is %d characters long, keep it under %d"}}`,
				length, session.MaxSessionTitleRunes), http.StatusBadRequest)
			return
		}
		if body.TitleIfUnpinned {
			// A suggestion, not a rename: it lands only while the session has
			// no name of its own, and the check and the write are one step so
			// a name written in between is not overwritten by this one.
			stored, _ := st.SetTitlePinnedIfUnset(t)
			resp["title"] = stored
		} else {
			st.SetTitlePinned(t)
			resp["title"] = t
		}
		did = true
	}
	// Tags are replaced wholesale rather than merged: the client holds the set
	// it is editing, and a merge would make removing the last tag impossible.
	// An empty array is therefore "no tags", not "leave them alone" - that is
	// what omitting the field means.
	if body.Tags != nil {
		st.SetTags(*body.Tags)
		did = true
		resp["tags"] = jsonTagList(st.GetTags())
	}
	if body.Archived != nil {
		st.SetArchived(*body.Archived)
		did = true
		resp["archived"] = st.GetArchived()
		if at := strings.TrimSpace(st.GetArchivedAt()); at != "" {
			resp["archivedAt"] = at
		}
	}
	if body.Pinned != nil {
		st.SetPinned(*body.Pinned)
		if *body.Pinned {
			// A new pin goes above the ones already there: a session is pinned
			// because it matters now, and hunting for it at the bottom of the
			// pins would be the opposite of what the pin was for.
			st.SetPinnedRank(s.lowestPinRank() - 1)
		}
		did = true
		pinned, at := st.PinState()
		resp["pinned"] = pinned
		if at = strings.TrimSpace(at); at != "" {
			resp["pinnedAt"] = at
		}
	}
	if !did {
		http.Error(w, `{"error":{"message":"title, tags, archived, pinned, markActivityRead, selectedModelId, or selectedReasoning required"}}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// deleteSessionBundle removes one session tree. It is the shared body of
// DELETE /coddy/sessions/{id} and of every id a bulk delete works through, so
// both routes stop background work the same way.
// An id with no bundle on disk removes nothing and reports no error.
func (s *Server) deleteSessionBundle(id string) error {
	// The manager removes the whole tree: the tasks representing this session's
	// subagent runs (and their descendants) are stopped and awaited first, then
	// every remaining task of every node, then the bundles deepest first, so
	// nothing writes into a directory that is already gone.
	return s.mgr.DeleteSessionTree(id, bgtask.Default())
}

// sessionDeleteStatus maps a delete failure onto the status the single-session
// route answers with: a tree that would not settle is a retryable conflict,
// anything else is a server error.
func sessionDeleteStatus(err error) int {
	if errors.Is(err, session.ErrTurnNotSettled) || errors.Is(err, session.ErrTreeUnstable) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *Server) coddySessionDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	if err := s.deleteSessionBundle(id); err != nil {
		status := sessionDeleteStatus(err)
		if status == http.StatusConflict {
			// A turn of the tree ignored its cancellation, or descendants
			// kept appearing while the tree was being marked; nothing was
			// removed, the client may retry once the tree is quiet.
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), status)
			return
		}
		s.log.Error("coddy session delete", "error", err)
		http.Error(w, `{"error":{"message":"delete failed"}}`, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.session_deleted", "id": id})
}

// lowestPinRank returns the rank of the pin that currently sits highest, or 0
// when nothing is pinned. A caller puts itself above them all with rank-1.
func (s *Server) lowestPinRank() int {
	fs := s.mgr.FileStore()
	if fs == nil {
		return 0
	}
	rows, err := fs.ListSnapshotsWith(session.ListOptions{Archived: session.ArchiveAll, IncludeEmpty: true})
	if err != nil {
		s.log.Warn("read pin ranks", "error", err)
		return 0
	}
	lowest := 0
	for _, row := range rows {
		if row.Pinned && row.PinnedRank < lowest {
			lowest = row.PinnedRank
		}
	}
	return lowest
}

// coddySessionPinsReorder writes the order the operator dragged the pins into.
//
// The whole order arrives at once rather than one moved id: a list rewritten
// from the client's own view cannot end up interleaved with a concurrent change
// in a way nobody asked for, and a refused request leaves every pin where it
// was - the ids are checked before anything is written.
func (s *Server) coddySessionPinsReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	if len(body.IDs) == 0 {
		http.Error(w, `{"error":{"message":"ids must not be empty"}}`, http.StatusBadRequest)
		return
	}
	ids := make([]string, 0, len(body.IDs))
	seen := make(map[string]struct{}, len(body.IDs))
	for _, raw := range body.IDs {
		id := strings.TrimSpace(raw)
		if err := session.ValidateFolderSessionID(id); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
			return
		}
		if _, dup := seen[id]; dup {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"%s is listed twice"}}`, id), http.StatusBadRequest)
			return
		}
		seen[id] = struct{}{}
		snap, err := fs.ReadSnapshot(id)
		if err != nil || !snap.Meta.Pinned {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"%s is not a pinned session"}}`, id), http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}

	for rank, id := range ids {
		st := s.coddyEnsureLoaded(w, r, id)
		if st == nil {
			return
		}
		st.SetPinnedRank(rank)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.session_pins_reordered",
		"ids":    ids,
	})
}

// coddySessionsBulkDeleteRequest is the body of POST /coddy/sessions/bulk-delete.
// Either an explicit list of ids, or scope "all" with an optional keep list -
// the session management table needs both: the rows an operator ticked, and
// "everything except the conversation I am in", which the client cannot spell
// as a list because it only ever holds one page of the history.
type coddySessionsBulkDeleteRequest struct {
	Scope  string   `json:"scope"`
	IDs    []string `json:"ids"`
	Except []string `json:"except"`
}

func (s *Server) coddySessionsBulkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	var req coddySessionsBulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "ids"
	}
	var targets []string
	switch scope {
	case "ids":
		if len(req.IDs) == 0 {
			http.Error(w, `{"error":{"message":"ids must not be empty"}}`, http.StatusBadRequest)
			return
		}
		if len(req.Except) > 0 {
			// Silently ignoring it would let a caller believe a session was
			// spared when the list never consulted the field.
			http.Error(w, `{"error":{"message":"except applies to scope \"all\" only"}}`, http.StatusBadRequest)
			return
		}
		for _, raw := range req.IDs {
			id := strings.TrimSpace(raw)
			if err := session.ValidateFolderSessionID(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
			targets = append(targets, id)
		}
	case "all", "archived":
		if len(req.IDs) > 0 {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"ids and scope %q are mutually exclusive"}}`, scope), http.StatusBadRequest)
			return
		}
		// An exception is a promise that a named session survives, so it is
		// checked before anything is removed: a misspelt id that matched
		// nothing would quietly turn "keep this one" into "delete everything".
		keep := make(map[string]struct{}, len(req.Except))
		for _, raw := range req.Except {
			id := strings.TrimSpace(raw)
			if err := session.ValidateFolderSessionID(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
			if !fs.HasPersistedSnapshot(id) {
				http.Error(w, fmt.Sprintf(`{"error":{"message":"except names a session that is not stored: %s"}}`, id), http.StatusBadRequest)
				return
			}
			// A session survives only if no ancestor of it is removed: the
			// delete takes a whole tree, so keeping a subagent child means
			// keeping the parent it hangs from. The child is not in the
			// listing below, its parent is.
			for _, ancestor := range sessionAncestry(fs, id) {
				keep[ancestor] = struct{}{}
			}
		}
		// "all" is resolved server side against the same listing the table
		// renders, so it means the whole history rather than the page the
		// client happens to have loaded. Scheduler runs stay out of it, and
		// subagent children go with the parent they belong to. "all" reaches
		// into the archive as well: a scope that left sessions behind because
		// they were put aside would not be the whole history.
		archived := session.ArchiveAll
		if scope == "archived" {
			archived = session.ArchiveOnly
		}
		// A session nobody wrote in is out of the listing people read, and in
		// the history this scope deletes all the same.
		rows, err := fs.ListSnapshotsWith(session.ListOptions{Archived: archived, IncludeEmpty: true})
		if err != nil {
			s.log.Error("coddy sessions bulk delete list", "error", err)
			http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			if _, skip := keep[row.SessionID]; skip {
				continue
			}
			targets = append(targets, row.SessionID)
		}
	default:
		http.Error(w, `{"error":{"message":"scope must be \"ids\", \"all\" or \"archived\""}}`, http.StatusBadRequest)
		return
	}

	requested, deleted, failed := bulkDeleteSessions(targets, func(id string) error {
		err := s.deleteSessionBundle(id)
		if err != nil && sessionDeleteStatus(err) != http.StatusConflict {
			s.log.Error("coddy sessions bulk delete", "session", id, "error", err)
		}
		return err
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.sessions_bulk_deleted",
		"requested": requested,
		"deleted":   deleted,
		"failed":    failed,
	})
}

// maxSessionAncestry bounds the parent walk so a corrupted bundle that points
// at itself, or a cycle written by an older build, cannot spin here.
const maxSessionAncestry = 32

// sessionAncestry returns id followed by every ancestor reachable through
// parentSessionId. Deleting any of them takes the whole subtree with it, so a
// caller that wants id to survive has to spare all of them.
func sessionAncestry(fs *session.FileStore, id string) []string {
	out := []string{id}
	seen := map[string]struct{}{id: {}}
	current := id
	for i := 0; i < maxSessionAncestry; i++ {
		snap, err := fs.ReadSnapshot(current)
		if err != nil {
			return out
		}
		parent := strings.TrimSpace(snap.Meta.ParentSessionID)
		if parent == "" {
			return out
		}
		if _, loop := seen[parent]; loop {
			return out
		}
		if err := session.ValidateFolderSessionID(parent); err != nil {
			return out
		}
		seen[parent] = struct{}{}
		out = append(out, parent)
		current = parent
	}
	return out
}

// bulkDeleteSessions removes every target once, in order, and reports what went
// and what stayed. One failing tree must not abandon the rest: a session in the
// middle of a turn answers a conflict and the batch carries on, so the table
// can drop the deleted rows and keep the others with their reason. The error
// text is the sentinel's own only for the two retryable conflicts; anything
// else is generic, because a filesystem error names paths the caller has no
// business reading.
func bulkDeleteSessions(targets []string, del func(string) error) (requested int, deleted []string, failed []map[string]string) {
	deleted = make([]string, 0, len(targets))
	failed = make([]map[string]string, 0)
	seen := make(map[string]struct{}, len(targets))
	for _, id := range targets {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if err := del(id); err != nil {
			message := "delete failed"
			if sessionDeleteStatus(err) == http.StatusConflict {
				message = err.Error()
			}
			failed = append(failed, map[string]string{"id": id, "error": message})
			continue
		}
		deleted = append(deleted, id)
	}
	return len(seen), deleted, failed
}

func (s *Server) coddyPlanGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":  "coddy.plan",
		"entries": st.GetPlan(),
	})
}

func (s *Server) coddyPlanPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Entries []acp.PlanEntry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	st.SetPlan(body.Entries)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.plan_updated",
		"count":  len(body.Entries),
	})
}

func (s *Server) coddyPlanArchivePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	entries := st.GetPlan()
	if len(entries) == 0 {
		st.SetPlan(nil)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.plan_archived", "note": "no active items"})
		return
	}
	for i := range entries {
		if entries[i].Status != "completed" {
			entries[i].Status = "completed"
		}
	}
	md := todo.FormatPlanMarkdown(entries)
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	pathNote := ""
	if sd != "" {
		dest, err := session.WritePlanArchivedMarkdown(sd, md)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
			return
		}
		pathNote = dest
	}
	st.SetPlan(nil)
	resp := map[string]interface{}{"object": "coddy.plan_archived"}
	if pathNote != "" {
		resp["archivePath"] = pathNote
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
