//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
	s.mux.HandleFunc("GET /coddy/slash-commands", s.coddySlashCommandsGet)
	s.mux.HandleFunc("GET /coddy/sessions", s.coddySessionsList)
	s.mux.HandleFunc("POST /coddy/describe", s.coddyDescribePost)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/activity", s.coddySessionActivityGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/messages", s.coddySessionMessagesGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/composer-stream", s.coddySessionComposerStream)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/tool-calls", s.coddyToolCallsList)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/tool-calls/{toolCallId}", s.coddyToolCallGet)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/stats", s.coddySessionStatsGet)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}", s.coddySessionPatch)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/cancel", s.coddySessionCancelGeneration)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/question", s.coddySessionQuestionPost)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/permission", s.coddySessionPermissionPost)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}", s.coddySessionDelete)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/plan", s.coddyPlanGet)
	s.mux.HandleFunc("PUT /coddy/sessions/{id}/plan", s.coddyPlanPut)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/plan/archive", s.coddyPlanArchivePost)
	s.registerDesignPlanRoutes()
	s.registerMemoryRoutes()
	s.registerSchedulerRoutes()
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
			CWD:       s.defaultCWD,
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

	words := strings.Fields(raw)
	if len(words) <= 3 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "coddy.describe",
			"short":  strings.Join(words, " "),
		})
		return
	}

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
			Content: "You generate short descriptions for chat titles and command labels. " +
				"Return exactly one short phrase (3 to 8 words) describing what the user's text is about. " +
				"Match the user's language when possible. " +
				"No quotes, no preamble, no headings, no line breaks, no numbering. Output only the phrase.",
		},
		{Role: llm.RoleUser, Content: raw},
	}, nil)
	if err != nil {
		s.log.Error("describe llm", "error", err)
		http.Error(w, `{"error":{"message":"LLM error"}}`, http.StatusBadGateway)
		return
	}

	short := describePickPhraseFromLLM(resp.Content, words)
	if short == "" {
		short = strings.Join(words[:min(3, len(words))], " ")
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.describe",
		"short":  short,
	})
}

type coddyToolCallRow struct {
	ToolCallID             string `json:"toolCallId"`
	Name                   string `json:"name,omitempty"`
	Kind                   string `json:"kind,omitempty"`
	Status                 string `json:"status,omitempty"`
	StartedAt              string `json:"startedAt,omitempty"`
	FinishedAt             string `json:"finishedAt,omitempty"`
	ArgsPreview            string `json:"argsPreview,omitempty"`
	ResultPreview          string `json:"resultPreview,omitempty"`
	ResultPreviewTruncated bool   `json:"resultPreviewTruncated,omitempty"`
	ResultTotalLines       int    `json:"resultTotalLines,omitempty"`
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
				"object":    "coddy.session_stats",
				"sessionId": id,
				"stats":     nil,
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
	})
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
		_, err := s.mgr.HandleSessionLoad(r.Context(), acp.SessionLoadParams{
			SessionID: id,
			CWD:       s.defaultCWD,
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
	rows, err := fs.ListSnapshots("", includeScheduler)
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
		if includeActivity {
			dir := fs.SessionPath(row.SessionID)
			turnActive := session.TurnLockHeld(dir)
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
	turnActive := session.TurnLockHeld(dir)
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
		"messages":  llmMsgsToCoddyOpenAI(st.GetMessages()),
	}
	if s.activeCfg() != nil {
		out["selectedModelId"] = strings.TrimSpace(st.GetSelectedModelID())
		out["model"] = effectiveYAMLModel(s.activeCfg(), st)
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
		Title             string  `json:"title"`
		MarkActivityRead  bool    `json:"markActivityRead"`
		SelectedModelID   *string `json:"selectedModelId"`
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
		st.SetTitlePinned(t)
		did = true
		resp["title"] = t
	}
	if !did {
		http.Error(w, `{"error":{"message":"title, markActivityRead, or selectedModelId required"}}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
	s.mgr.ForgetLiveSession(id)
	if err := os.RemoveAll(fs.SessionPath(id)); err != nil {
		if !os.IsNotExist(err) {
			s.log.Error("coddy session delete", "error", err)
			http.Error(w, `{"error":{"message":"delete failed"}}`, http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.session_deleted", "id": id})
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
