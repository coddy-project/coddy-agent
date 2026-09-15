//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
	s.mux.HandleFunc("GET /coddy/workspace/files", s.coddyWorkspaceFilesGet)
	s.mux.HandleFunc("GET /coddy/workspace/context", s.coddyWorkspaceContextGet)
	s.mux.HandleFunc("GET /coddy/workspace/folders", s.coddyWorkspaceFoldersGet)
	s.mux.HandleFunc("POST /coddy/workspace/folders", s.coddyWorkspaceFoldersPost)
	s.mux.HandleFunc("GET /coddy/workspace/file", s.coddyWorkspaceFileGet)
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
	s.registerSubagentRoutes()
	s.registerHookRoutes()
	s.registerSchedulerRoutes()
	s.registerBranchRoutes()
	s.registerSkillsManagementRoutes()
	s.registerMCPManagementRoutes()
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

	// Every text is asked about, however short. A first message of two words is
	// exactly the one that needs the model: "git status" is a usable title and
	// no filing at all, and the tags ride on this call - echoing the words back
	// would leave the shortest conversations the only unlabelled ones.
	words := strings.Fields(raw)

	provider, err := s.providerFactory(s.activeCfg())
	if err != nil {
		s.log.Error("describe provider", "error", err)
		http.Error(w, `{"error":{"message":"LLM unavailable"}}`, http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	resp, err := provider.Complete(ctx, []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: prompts.WithIdentity(
				"You generate short descriptions for chat titles and command labels. " +
					"Return exactly one short phrase (3 to 8 words) describing what the user's text is about. " +
					"Match the user's language when possible. " +
					"No quotes, no preamble, no headings, no numbering. " +
					"Then, on a second line, write " + describeTagsPrefix + " followed by 1 to 3 comma separated topic labels " +
					"for filing the conversation - one or two words each, lower case, in English. Output nothing else."),
		},
		{Role: llm.RoleUser, Content: raw},
	}, nil)
	if err != nil {
		s.log.Error("describe llm", "error", err)
		http.Error(w, `{"error":{"message":"LLM error"}}`, http.StatusBadGateway)
		return
	}

	phraseLines, tags := describeSplitTagsLine(resp.Content)
	short := describePickPhraseFromLLM(phraseLines, words)
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
	ToolCallID             string          `json:"toolCallId"`
	Name                   string          `json:"name,omitempty"`
	Kind                   string          `json:"kind,omitempty"`
	Status                 string          `json:"status,omitempty"`
	StartedAt              string          `json:"startedAt,omitempty"`
	FinishedAt             string          `json:"finishedAt,omitempty"`
	ArgsPreview            string          `json:"argsPreview,omitempty"`
	ResultPreview          string          `json:"resultPreview,omitempty"`
	ResultPreviewTruncated bool            `json:"resultPreviewTruncated,omitempty"`
	ResultTotalLines       int             `json:"resultTotalLines,omitempty"`
	PlanSnapshot           []acp.PlanEntry `json:"planSnapshot,omitempty"`
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
	rows, err := fs.ListSnapshotsWith(session.ListOptions{
		CWD:                  strings.TrimSpace(r.URL.Query().Get("cwd")),
		IncludeSchedulerRuns: includeScheduler,
		IncludeSubagents:     includeSubagents,
		Archived:             archived,
		Tags:                 session.ParseTagList(r.URL.Query().Get("tags")),
		Origin:               origin,
	})
	if err != nil {
		s.log.Error("coddy sessions list", "error", err)
		http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
		return
	}
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
	session.SortSessionList(rows, sortKey, sortOrder, tokensOf)

	limit, offset := parseLimitCursor(r.URL.Query())
	start := offset
	if start >= len(rows) {
		out := map[string]interface{}{
			"object":     "coddy.session_list",
			"sessions":   []interface{}{},
			"nextCursor": nil,
			"hasMore":    false,
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
	sessions := make([]map[string]interface{}, 0, len(slice))
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
			actSeq, readSeq, _ := fs.ReadDiskActivity(row.SessionID)
			ent["turnActive"] = turnActive
			ent["activitySeq"] = actSeq
			ent["readActivitySeq"] = readSeq
			ent["unreadComplete"] = actSeq > readSeq && !turnActive
			ent["permissionPending"] = session.PendingPermissionHeld(dir)
		}
		sessions = append(sessions, ent)
	}
	var nextCursor interface{}
	if end < len(rows) {
		nextCursor = strconv.Itoa(end)
	}
	out := map[string]interface{}{
		"object":     "coddy.session_list",
		"sessions":   sessions,
		"nextCursor": nextCursor,
		"hasMore":    end < len(rows),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
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
	actSeq, readSeq, err := fs.ReadDiskActivity(id)
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
		"unreadComplete":  actSeq > readSeq && !turnActive,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func llmMsgsToCoddyOpenAI(msgs []llm.Message) []map[string]interface{} {
	return llmMsgsToCoddyOpenAIForSession("", msgs)
}

func llmMsgsToCoddyOpenAIForSession(sessionID string, msgs []llm.Message) []map[string]interface{} {
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
		if m.Role == llm.RoleUser && len(m.ImageParts) > 0 {
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
				if sessionID != "" && part.FilePath != "" && part.ThumbnailPath != "" {
					assetName := filepath.Base(part.FilePath)
					file["preview_url"] = "/coddy/sessions/" + url.PathEscape(sessionID) +
						"/assets/" + url.PathEscape(assetName) + "/thumbnail"
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

func (s *Server) coddySessionAssetThumbnailGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		http.Error(w, `{"error":{"message":"invalid asset name"}}`, http.StatusBadRequest)
		return
	}
	sessionDir := strings.TrimSpace(st.GetPersistedSessionDir())
	if sessionDir == "" {
		http.NotFound(w, r)
		return
	}
	path := session.AssetThumbnailPath(sessionDir, name)
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
	defer f.Close()
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

func (s *Server) coddySessionMessagesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	out := map[string]interface{}{
		"object":    "coddy.messages",
		"sessionId": id,
		"messages":  llmMsgsToCoddyOpenAIForSession(id, st.GetMessages()),
	}
	// A child session is a read-only transcript: the SPA drops the composer
	// and links back to the parent chat and to the task in its drawer.
	if meta := st.Subagent(); meta != nil {
		out["readOnly"] = true
		out["subagent"] = subagentLink(meta.ParentSessionID, meta.Name, meta.TaskID)
	}
	// An archived session is where the composer learns it must not offer a
	// prompt. It cannot be read off the session listing: that skips the archive,
	// so the conversation on screen may be in no page the client holds.
	if archived, _ := st.ArchiveState(); archived {
		out["archived"] = true
	}
	if s.activeCfg() != nil {
		out["selectedModelId"] = strings.TrimSpace(st.GetSelectedModelID())
		out["model"] = effectiveYAMLModel(s.activeCfg(), st)
		out["selectedReasoning"] = st.EffectiveReasoning(s.activeCfg())
		out["mode"] = string(st.GetMode())
	}
	if u := st.GetUILog(); len(u) > 0 {
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
	if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" {
		if env, err := session.ReadMemoryTrace(sd); err == nil && env != nil && len(env.Turns) > 0 {
			out["memoryTurns"] = env.Turns
		}
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
		TitleIfUnpinned   bool      `json:"titleIfUnpinned"`
		MarkActivityRead  bool      `json:"markActivityRead"`
		SelectedModelID   *string   `json:"selectedModelId"`
		SelectedReasoning *string   `json:"selectedReasoning"`
		Tags              *[]string `json:"tags"`
		Archived          *bool     `json:"archived"`
		Pinned            *bool     `json:"pinned"`
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
	if body.SelectedModelID != nil {
		if err := applySessionYAMLModel(s.activeCfg(), st, *body.SelectedModelID); err != nil {
			if errors.Is(err, ErrUnknownMetadataModel) {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
			http.Error(w, `{"error":{"message":"invalid selectedModelId"}}`, http.StatusBadRequest)
			return
		}
		did = true
		resp["selectedModelId"] = strings.TrimSpace(st.GetSelectedModelID())
		if s.activeCfg() != nil {
			resp["model"] = effectiveYAMLModel(s.activeCfg(), st)
		}
	}
	if body.SelectedReasoning != nil {
		if err := applySessionReasoning(s.activeCfg(), st, *body.SelectedReasoning); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
			return
		}
		did = true
		resp["selectedReasoning"] = strings.TrimSpace(st.GetSelectedReasoning())
	}
	if body.MarkActivityRead {
		st.MarkActivityReadSynced()
		did = true
		resp["activitySeq"] = st.GetActivitySeq()
		resp["readActivitySeq"] = st.GetReadActivitySeq()
		if fs != nil {
			if err := fs.PatchSessionMetaActivitySync(st); err != nil {
				s.log.Warn("patch session meta activity", "id", id, "error", err)
			}
		}
	}
	t := strings.TrimSpace(body.Title)
	if t != "" {
		pinned := strings.TrimSpace(st.GetTitlePinned())
		if body.TitleIfUnpinned && pinned != "" {
			// A suggestion that lost the race: the session already carries a
			// name, and the tags of the same request still apply.
			did = true
			resp["title"] = pinned
		} else {
			st.SetTitlePinned(t)
			did = true
			resp["title"] = t
		}
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
// both routes retract branch references and stop background work the same way.
// An id with no bundle on disk removes nothing and reports no error.
func (s *Server) deleteSessionBundle(id string) error {
	// Retract the session from the branch file of whatever it forked from, so the
	// branch navigator stops offering a thread that no longer exists. Read-side
	// filtering covers the failure case, so a prune error must not block delete.
	// It reads the session's own branch file, so it runs before the bundle goes.
	if err := s.mgr.PruneBranchRefs(id); err != nil {
		s.log.Warn("prune branch refs on delete", "session", id, "error", err)
	}
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
	rows, err := fs.ListSnapshotsWith(session.ListOptions{Archived: session.ArchiveAll})
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
		rows, err := fs.ListSnapshotsWith(session.ListOptions{Archived: archived})
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
