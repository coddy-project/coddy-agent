// Package agent implements the ReAct (Reasoning + Acting) loop for a session turn.
// System prompts are rendered via internal/prompts (embedded templates or prompts.dir).
package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// SessionState is the interface Agent needs from a session.
// It is implemented by session.State without requiring a direct import.
type SessionState interface {
	GetID() string
	GetCWD() string
	GetMode() string
	SetMode(mode string)
	EffectiveModelID(cfg *config.Config) string
	AddMessage(msg llm.Message)
	GetMessages() []llm.Message
	GetMCPClients() []*mcp.Client
	GetSkills() []*skills.Skill
	GetAgentMemory() string
	GetMemoryCopilotBlock() string
	SetMemoryCopilotBlock(text string)
	ClearMemoryCopilotBlock()
	GetPlan() []acp.PlanEntry
	SetPlan([]acp.PlanEntry)
	GetPersistedSessionDir() string
	AppendPlanDocument(plans.Document)
	DiscardedPlanSlugs() []string
	TakePendingPlanContext() string
	GetPermissionMode() string
}

// Agent runs the ReAct loop for a single session turn.
type Agent struct {
	cfg             *config.Config
	state           SessionState
	server          acp.UpdateSender
	log             *slog.Logger
	registry        *tools.Registry
	providerFactory func(llm.ProviderInput) (llm.Provider, error)
}

// NewAgent creates an Agent for a prompt turn.
func NewAgent(cfg *config.Config, state SessionState, server acp.UpdateSender, log *slog.Logger) *Agent {
	return &Agent{
		cfg:             cfg,
		state:           state,
		server:          server,
		log:             log,
		registry:        tools.NewRegistryFor(cfg),
		providerFactory: llm.NewProvider,
	}
}

// SetProviderFactory replaces the LLM provider factory used by subsequent turns.
func (a *Agent) SetProviderFactory(mk func(llm.ProviderInput) (llm.Provider, error)) {
	if a == nil || mk == nil {
		return
	}
	a.providerFactory = mk
}

// Run executes the ReAct loop and returns the stop reason.
func (a *Agent) Run(ctx context.Context, prompt []acp.ContentBlock) (string, error) {
	mode := a.state.GetMode()

	// Build the user message from prompt content blocks.
	a.state.ClearMemoryCopilotBlock()
	userText := contentBlocksToText(prompt)
	a.state.AddMessage(llm.Message{
		Role:      llm.RoleUser,
		Content:   userText,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	a.runMemoryBeforeTurn(ctx, userText)

	// Collect context files from the prompt for skill filtering.
	contextFiles := extractContextFiles(prompt)

	// Load skills applicable to this context.
	activeSkills := FilterSkillsForContext(a.state.GetSkills(), contextFiles)

	toolSet := ToolSetForMode(mode)
	toolDefs := FilterToolDefinitions(a.registry.AllToolDefinitions(), toolSet)
	if toolSet.Unrestricted() || mode == "plan" {
		for _, mcpClient := range a.state.GetMCPClients() {
			for _, t := range mcpClient.Tools() {
				toolDefs = append(toolDefs, t.ToLLMToolDefinition(mcpClient.Name()))
			}
		}
	}

	// Get or create LLM provider.
	provider, err := a.getProvider(mode)
	if err != nil {
		return string(acp.StopReasonRefused), fmt.Errorf("no LLM configured: %w", err)
	}

	// Restore existing plan via session/update if one was set by coddy todo tools in a previous turn.
	if existing := a.state.GetPlan(); len(existing) > 0 {
		if err := a.sendPlan(a.state.GetID(), existing); err != nil {
			a.log.Warn("failed to restore plan", "error", err)
		}
	}

	// Build the full message list starting with system prompt (refreshed each ReAct turn).
	messages := a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))

	maxTurns := a.cfg.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}

	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())

	toolEnv := &tools.Env{
		CWD:              a.state.GetCWD(),
		PermissionMode:   effectivePermMode(a.state, a.cfg),
		CommandAllowlist: a.cfg.Tools.CommandAllowlist,
		SessionID:                    a.state.GetID(),
		SessionDir:                   sd,
		ArchiveActiveMarkdown: func() error {
			if sd == "" {
				return nil
			}
			return session.ArchiveActiveTodo(sd)
		},
		WriteArchivedPlanMarkdown: func(md string) (string, error) {
			if sd == "" {
				return "", nil
			}
			return session.WritePlanArchivedMarkdown(sd, md)
		},
		Sender:  a.server,
		GetPlan: a.state.GetPlan,
		SetPlan: a.state.SetPlan,
		SetSessionMode: func(mode string) error {
			a.state.SetMode(strings.TrimSpace(mode))
			return nil
		},
		PersistPlanDocument: func(doc plans.Document) {
			a.state.AppendPlanDocument(doc)
		},
	}
	toolEnv.SendDesignPlanUpdate = func(doc plans.Document) {
		tools.SendDesignPlanUpdate(toolEnv, doc)
	}

	return a.runReActLoop(ctx, mode, messages, toolDefs, provider, toolEnv, sd, userText, contextFiles, activeSkills, maxTurns)
}

func (a *Agent) runReActLoop(
	ctx context.Context,
	mode string,
	messages []llm.Message,
	toolDefs []llm.ToolDefinition,
	provider llm.Provider,
	toolEnv *tools.Env,
	sd, userText string,
	contextFiles []string,
	activeSkills []*skills.Skill,
	maxTurns int,
) (string, error) {
	var totalInputTokens, totalOutputTokens int
	var turnIndex int
	var lastStatsWrite time.Time

	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return string(acp.StopReasonCancelled), nil
		}

		// System prompt is rebuilt every turn so conditional sections (e.g. todo checklist) match
		// state after coddy_todo_* tools in the same user turn.
		if len(messages) > 0 && messages[0].Role == llm.RoleSystem {
			messages[0].Content = a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles)
		}

		// Call LLM and stream response.
		var response *llm.Response
		var streamErr error
		var reasoningBuf strings.Builder

		reasonClockStart := time.Time{}
		reasonClockEnd := time.Time{}
		maybeMarkReasonEnd := func(now time.Time) {
			if reasonClockStart.IsZero() || !reasonClockEnd.IsZero() {
				return
			}
			if strings.TrimSpace(reasoningBuf.String()) == "" {
				return
			}
			reasonClockEnd = now
		}

		sessionID := a.state.GetID()
		emitReason := func(d string, now time.Time) {
			reasoningBuf.WriteString(d)
			if reasonClockStart.IsZero() {
				reasonClockStart = now
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: d},
			})
		}
		emitText := func(delta string, now time.Time, markReasonEnd bool) {
			if markReasonEnd && strings.TrimSpace(delta) != "" {
				maybeMarkReasonEnd(now)
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: delta},
			})
		}

		response, streamErr = provider.Stream(ctx, messages, toolDefs, func(chunk llm.StreamChunk) {
			if ctx.Err() != nil {
				return
			}
			now := time.Now()
			if chunk.ReasoningDelta != "" {
				emitReason(chunk.ReasoningDelta, now)
			}
			if chunk.TextDelta != "" && strings.TrimSpace(chunk.TextDelta) != "" {
				emitText(chunk.TextDelta, now, true)
			} else if chunk.TextDelta != "" {
				emitText(chunk.TextDelta, now, false)
			}
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
				maybeMarkReasonEnd(now)
				if st := sessionStatePtr(a.state); st != nil {
					if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" && strings.TrimSpace(chunk.ToolCall.ID) != "" {
						_ = session.WriteToolCallMeta(sd, chunk.ToolCall.ID, session.ToolCallMeta{
							ToolCallID: strings.TrimSpace(chunk.ToolCall.ID),
							Name:       chunk.ToolCall.Name,
							Kind:       toolKind(chunk.ToolCall.Name),
							Status:     "pending",
						})
					}
				}
				_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: acp.UpdateTypeToolCall,
					ToolCallID:    chunk.ToolCall.ID,
					Title:         chunk.ToolCall.Name, // plain name, no "Calling: " prefix
					Kind:          toolKind(chunk.ToolCall.Name),
					Status:        "pending",
				})
			}
		})

		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) && response != nil {
				reasonTrim := strings.TrimSpace(reasoningBuf.String())
				hasText := strings.TrimSpace(response.Content) != ""
				hasTools := len(response.ToolCalls) > 0
				if hasText || hasTools || reasonTrim != "" {
					var reasoningMs int64
					if reasonTrim != "" && !reasonClockStart.IsZero() {
						end := reasonClockEnd
						if end.IsZero() {
							end = time.Now()
						}
						d := end.Sub(reasonClockStart)
						if d < 0 {
							d = 0
						}
						reasoningMs = d.Milliseconds()
					}
					assistantMsg := llm.Message{
						Role:                llm.RoleAssistant,
						Content:             response.Content,
						Reasoning:           reasonTrim,
						ToolCalls:           response.ToolCalls,
						ReasoningDurationMs: reasoningMs,
						Model:               a.state.EffectiveModelID(a.cfg),
						CreatedAt:           time.Now().UTC().Format(time.RFC3339),
					}
					a.state.AddMessage(assistantMsg)
				}
			}
			if ctx.Err() != nil {
				return string(acp.StopReasonCancelled), nil
			}
			if errors.Is(streamErr, context.Canceled) {
				return string(acp.StopReasonCancelled), nil
			}
			return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w", streamErr)
		}

		// Accumulate and broadcast token usage after each LLM call.
		totalInputTokens += response.InputTokens
		totalOutputTokens += response.OutputTokens
		_ = a.server.SendSessionUpdate(sessionID, acp.TokenUsageUpdate{
			SessionUpdate: acp.UpdateTypeTokenUsage,
			InputTokens:   response.InputTokens,
			OutputTokens:  response.OutputTokens,
			TotalTokens:   totalInputTokens + totalOutputTokens,
		})

		if sd != "" {
			now := time.Now().UTC()
			if lastStatsWrite.IsZero() || now.Sub(lastStatsWrite) > 750*time.Millisecond {
				lastStatsWrite = now
				stats := session.SessionStats{
					Version:   1,
					UpdatedAt: now.Format(time.RFC3339),
					TokenUsageTotal: session.TokenUsageTotals{
						InputTokens:  totalInputTokens,
						OutputTokens: totalOutputTokens,
						TotalTokens:  totalInputTokens + totalOutputTokens,
					},
					TokenUsageByTurn: []session.TokenUsageTurn{{
						TurnIndex:    turnIndex,
						InputTokens:  response.InputTokens,
						OutputTokens: response.OutputTokens,
						TotalTokens:  totalInputTokens + totalOutputTokens,
						Timestamp:    now.Format(time.RFC3339),
					}},
				}
				if rs, ok := a.state.(rulesState); ok {
					if b := rs.GetLastContextBreakdown(); b != nil {
						cp := *b
						stats.ContextBreakdown = &cp
					}
				}
				_ = session.WriteSessionStats(sd, stats)
			}
		}
		turnIndex++

		reasonTrim := strings.TrimSpace(reasoningBuf.String())
		var reasoningMs int64
		if reasonTrim != "" && !reasonClockStart.IsZero() {
			end := reasonClockEnd
			if end.IsZero() {
				end = time.Now()
			}
			d := end.Sub(reasonClockStart)
			if d < 0 {
				d = 0
			}
			reasoningMs = d.Milliseconds()
		}

		// Append assistant message to history.
		assistantMsg := llm.Message{
			Role:                llm.RoleAssistant,
			Content:             response.Content,
			Reasoning:           reasonTrim,
			ToolCalls:           response.ToolCalls,
			ReasoningDurationMs: reasoningMs,
			Model:               a.state.EffectiveModelID(a.cfg),
			CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		}
		messages = append(messages, assistantMsg)
		a.state.AddMessage(assistantMsg)

		// If no tool calls, we're done.
		if len(response.ToolCalls) == 0 {
			stopReason := response.StopReason
			if stopReason == "" || stopReason == "end_turn" {
				return string(acp.StopReasonEndTurn), nil
			}
			if stopReason == "max_tokens" {
				return string(acp.StopReasonMaxTokens), nil
			}
			return string(acp.StopReasonEndTurn), nil
		}

		// Execute all tool calls.
		for _, tc := range response.ToolCalls {
			if ctx.Err() != nil {
				return string(acp.StopReasonCancelled), nil
			}

			result, execErr := a.executeToolCall(ctx, tc, toolEnv, mode, a.state.GetID(), false)

			var toolResultMsg llm.Message
			if execErr != nil {
				toolResultMsg = llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf("error: %v", execErr),
					ToolCallID: tc.ID,
				}
			} else {
				toolResultMsg = llm.Message{
					Role:       llm.RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				}
			}

			messages = append(messages, toolResultMsg)
			a.state.AddMessage(toolResultMsg)
		}
	}

	return string(acp.StopReasonMaxTurns), nil
}

// executeToolCall runs a single tool call and reports updates to the client.
func (a *Agent) executeToolCall(ctx context.Context, tc llm.ToolCall, env *tools.Env, mode, sessionID string, skipPermission bool) (string, error) {
	env.ToolCallID = strings.TrimSpace(tc.ID)
	defer func() { env.ToolCallID = "" }()

	sessionDir := ""
	if st := sessionStatePtr(a.state); st != nil {
		sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	}

	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		_ = session.MarkToolCallStarted(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), "in_progress")
		_ = session.WriteToolCallArgs(sessionDir, tc.ID, tc.InputJSON)
	}

	// Mark as in_progress, include raw InputJSON so connected clients can show args.
	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        "in_progress",
		Content: []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: tc.InputJSON}},
		},
	})

	// Check if tool requires permission.
	tool, ok := a.registry.Get(tc.Name)
	requiresPerm := ok && tool.RequiresPermission

	var sessCmdGrants, sessWriteGrants []string
	if st := sessionStatePtr(a.state); st != nil {
		sessCmdGrants = st.GetPermissionCommandGrants()
		sessWriteGrants = st.GetPermissionWriteGrants()
	}

	if tc.Name == "run_command" {
		switch env.PermissionMode {
		case config.PermModeBypass:
			requiresPerm = false
		case config.PermModeAcceptEdits:
			cmd := permission.ExtractRunCommand(tc.InputJSON)
			if permission.CommandAllowedWithSession(env, sessCmdGrants, cmd) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		default: // ask
			cmd := permission.ExtractRunCommand(tc.InputJSON)
			if permission.CommandAllowedWithSession(env, sessCmdGrants, cmd) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		}
	} else if filesystemWriteTool(tc.Name) {
		switch env.PermissionMode {
		case config.PermModeBypass, config.PermModeAcceptEdits:
			keys := permission.WriteGrantKeys(tc.Name, tc.InputJSON, env.CWD)
			if permission.AllWriteKeysGranted(sessWriteGrants, keys) {
				requiresPerm = false
			} else {
				requiresPerm = false // auto-approve
			}
		default: // ask
			keys := permission.WriteGrantKeys(tc.Name, tc.InputJSON, env.CWD)
			if permission.AllWriteKeysGranted(sessWriteGrants, keys) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		}
	}

	if requiresPerm && !skipPermission {
		permResult, err := a.server.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: sessionID,
			ToolCall: acp.PermissionToolCall{
				ToolCallID: tc.ID,
				Title:      fmt.Sprintf("Run: %s", tc.Name),
				Kind:       toolKind(tc.Name),
				Status:     "pending",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: "text", Text: permission.PromptBody(tc.Name, tc.InputJSON)}},
				},
			},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
				{OptionID: "allow_always", Name: "Allow always", Kind: "allow_always"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
		})

		if err != nil || permResult == nil || permResult.Outcome == "cancelled" || permResult.OptionID == "reject" {
			_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    tc.ID,
				Status:        "cancelled",
			})
			return "permission denied by user", nil
		}
		if st := sessionStatePtr(a.state); st != nil {
			permission.RecordAllowAlways(st, tc.Name, tc.InputJSON, env.CWD, permResult)
		}
	}

	// Execute the tool.
	var result string
	var execErr error

	// Check if it's an MCP tool (name contains __).
	if idx := strings.Index(tc.Name, "__"); idx >= 0 {
		serverName := tc.Name[:idx]
		toolName := tc.Name[idx+2:]
		result, execErr = a.callMCPTool(ctx, serverName, toolName, tc.InputJSON)
	} else {
		result, execErr = a.registry.Execute(ctx, tc.Name, tc.InputJSON, env)
	}

	status := "completed"
	if execErr != nil {
		status = "failed"
	}

	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		finalText := result
		if execErr != nil {
			finalText = fmt.Sprintf("error: %v", execErr)
		}
		_ = session.WriteToolCallResult(sessionDir, tc.ID, finalText)
		_ = session.MarkToolCallFinished(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), status)
	}

	payload := result
	if execErr != nil {
		payload = fmt.Sprintf("error: %v", execErr)
	}
	var content []acp.ToolCallResultItem
	var previewMeta map[string]interface{}
	if strings.TrimSpace(payload) != "" {
		display, meta := session.PreviewToolResultForSessionUpdate(tc.Name, payload)
		previewMeta = meta
		content = []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: display}},
		}
	}

	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        status,
		Content:       content,
		Meta:          previewMeta,
	})

	return result, execErr
}

// callMCPTool routes a tool call to the appropriate MCP client.
func (a *Agent) callMCPTool(ctx context.Context, serverName, toolName, argsJSON string) (string, error) {
	for _, client := range a.state.GetMCPClients() {
		if client.Name() == serverName {
			return client.CallTool(ctx, toolName, argsJSON)
		}
	}
	return "", fmt.Errorf("MCP server not found: %s", serverName)
}

// buildMessages constructs the message slice to send to the LLM.
func (a *Agent) buildMessages(systemPrompt string) []llm.Message {
	history := a.state.GetMessages()
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	for _, m := range history {
		if isLLMHistoryMessage(m) {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

func isLLMHistoryMessage(m llm.Message) bool {
	if m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Reasoning) == "" {
		return false
	}
	return true
}

// sendPlan sends the plan update to the client.
func (a *Agent) sendPlan(sessionID string, entries []acp.PlanEntry) error {
	return a.server.SendSessionUpdate(sessionID, acp.PlanUpdate{
		SessionUpdate: acp.UpdateTypePlan,
		Entries:       entries,
	})
}

// getProvider creates the LLM provider for the given mode.
func (a *Agent) getProvider(mode string) (llm.Provider, error) {
	modelID := a.state.EffectiveModelID(a.cfg)
	if modelID == "" {
		return nil, fmt.Errorf("no model configured")
	}

	rm, err := a.cfg.ResolveLLM(modelID)
	if err != nil {
		return nil, err
	}

	mk := a.providerFactory
	if mk == nil {
		mk = llm.NewProvider
	}
	return mk(a.llmProviderInput(rm))
}

func (a *Agent) llmProviderInput(rm *config.ResolvedLLM) llm.ProviderInput {
	return llm.WithAgentResilience(llm.ProviderInput{
		Type:        rm.ProviderType,
		Model:       rm.Model,
		APIKey:      rm.APIKey,
		BaseURL:     rm.BaseURL,
		ProxyURL:    rm.ProxyURL,
		MaxTokens:   rm.MaxTokens,
		Temperature: rm.Temperature,
	}, a.cfg.Agent.LLMRetryMax, a.cfg.Agent.LLMRetryBaseMS, a.cfg.Agent.LLMMinIntervalMS)
}

// contentBlocksToText converts ACP content blocks to a plain text string.
// Hydrated attachments become **<coddy_attachment path="..." name="...">…</coddy_attachment>**
// with file body inside CDATA so the SPA can strip tags for display while the model retains full context.
func contentBlocksToText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource":
			if b.Resource != nil {
				parts = append(parts, resourceBlockToXMLAttachment(b.Resource))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func xmlEscapedAttr(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func wrapXMLCDATA(body string) string {
	// Split CDATA if the payload contains the terminator sequence.
	escaped := strings.ReplaceAll(body, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[" + escaped + "]]>"
}

func resourceBlockToXMLAttachment(res *acp.Resource) string {
	pathRaw := strings.TrimSpace(res.URI)
	pathRaw = strings.TrimPrefix(pathRaw, "file://")
	pathFwd := filepath.ToSlash(pathRaw)
	name := filepath.Base(pathFwd)
	if name == "." || name == "/" {
		name = pathFwd
	}
	var b strings.Builder
	b.WriteString(`<coddy_attachment path="`)
	b.WriteString(xmlEscapedAttr(pathFwd))
	b.WriteString(`" name="`)
	b.WriteString(xmlEscapedAttr(name))
	b.WriteString(`">`)
	b.WriteByte('\n')
	b.WriteString(wrapXMLCDATA(res.Text))
	b.WriteString("\n</coddy_attachment>")
	return b.String()
}

// extractContextFiles returns file paths referenced in content blocks.
func extractContextFiles(blocks []acp.ContentBlock) []string {
	var files []string
	for _, b := range blocks {
		if b.Type == "resource" && b.Resource != nil {
			uri := b.Resource.URI
			if strings.HasPrefix(uri, "file://") {
				files = append(files, strings.TrimPrefix(uri, "file://"))
			}
		}
	}
	return files
}

// toolKind maps a tool name to an ACP tool call kind.
func toolKind(name string) string {
	switch name {
	case "read", "glob", "grep", "websearch", "webfetch":
		return "read"
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv":
		return "write"
	case "run_command":
		return "run_command"
	default:
		return "other"
	}
}

func filesystemWriteTool(name string) bool {
	switch name {
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv":
		return true
	default:
		return false
	}
}

// effectivePermMode returns the session-level permission mode override, falling back to the config default.
func effectivePermMode(state SessionState, cfg *config.Config) string {
	if m := state.GetPermissionMode(); m != "" {
		return m
	}
	return cfg.Tools.ResolvedPermMode()
}

// extractCommand parses the "command" field from run_command JSON args.
func extractCommand(argsJSON string) string {
	return permission.ExtractRunCommand(argsJSON)
}

func sessionStatePtr(s SessionState) *session.State {
	st, ok := s.(*session.State)
	if !ok {
		return nil
	}
	return st
}
