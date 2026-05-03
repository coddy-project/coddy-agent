// Package react implements the ReAct (Reasoning + Acting) agent loop.
// System prompts are rendered via internal/prompts (embedded templates or prompts.dir).
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// SessionState is the interface react.Agent needs from a session.
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
	GetPlan() []acp.PlanEntry
	SetPlan([]acp.PlanEntry)
}

// Agent runs the ReAct loop for a single session turn.
type Agent struct {
	cfg      *config.Config
	state    SessionState
	server   acp.UpdateSender
	log      *slog.Logger
	registry *tools.Registry
}

// NewAgent creates an Agent for a prompt turn.
func NewAgent(cfg *config.Config, state SessionState, server acp.UpdateSender, log *slog.Logger) *Agent {
	return &Agent{
		cfg:      cfg,
		state:    state,
		server:   server,
		log:      log,
		registry: tools.NewRegistry(),
	}
}

// Run executes the ReAct loop and returns the stop reason.
func (a *Agent) Run(ctx context.Context, prompt []acp.ContentBlock) (string, error) {
	mode := a.state.GetMode()

	// Build the user message from prompt content blocks.
	userText := contentBlocksToText(prompt)
	a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: userText})

	// Collect context files from the prompt for skill filtering.
	contextFiles := extractContextFiles(prompt)

	// Load skills applicable to this context.
	activeSkills := skills.FilterForContext(a.state.GetSkills(), contextFiles)

	toolDefs := a.registry.ToolsForMode(mode)
	for _, mcpClient := range a.state.GetMCPClients() {
		for _, t := range mcpClient.Tools() {
			toolDefs = append(toolDefs, t.ToLLMToolDefinition(mcpClient.Name()))
		}
	}

	systemPrompt := a.buildSystemPrompt(mode, activeSkills, toolDefs)

	// Get or create LLM provider.
	provider, err := a.getProvider(mode)
	if err != nil {
		return string(acp.StopReasonRefused), fmt.Errorf("no LLM configured: %w", err)
	}

	// Restore existing plan via session/update if one was set by create_todo_list in a previous turn.
	if existing := a.state.GetPlan(); len(existing) > 0 {
		if err := a.sendPlan(a.state.GetID(), existing); err != nil {
			a.log.Warn("failed to restore plan", "error", err)
		}
	}

	// Build the full message list starting with system prompt.
	messages := a.buildMessages(systemPrompt)

	maxTurns := a.cfg.React.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}

	toolEnv := &tools.Env{
		CWD:                          a.state.GetCWD(),
		RestrictToCWD:                a.cfg.Tools.RestrictToCWD,
		RequirePermissionForCommands: a.cfg.Tools.RequirePermissionForCommands,
		RequirePermissionForWrites:   a.cfg.Tools.RequirePermissionForWrites,
		CommandAllowlist:             a.cfg.Tools.CommandAllowlist,
		SessionID:                    a.state.GetID(),
		Sender:                       a.server,
		GetPlan:                      a.state.GetPlan,
		SetPlan:                      a.state.SetPlan,
	}

	var totalInputTokens, totalOutputTokens int

	// ReAct loop.
	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return string(acp.StopReasonCancelled), nil
		}

		// Call LLM and stream response.
		var response *llm.Response
		var streamErr error

		sessionID := a.state.GetID()
		response, streamErr = provider.Stream(ctx, messages, toolDefs, func(chunk llm.StreamChunk) {
			if ctx.Err() != nil {
				return
			}
			if chunk.ReasoningDelta != "" {
				_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeAgentMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: chunk.ReasoningDelta},
				})
			}
			if chunk.TextDelta != "" {
				_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeAgentMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: chunk.TextDelta},
				})
			}
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
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
			if ctx.Err() != nil {
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

		// Append assistant message to history.
		assistantMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
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

			result, execErr := a.executeToolCall(ctx, tc, toolEnv, mode, a.state.GetID())

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
func (a *Agent) executeToolCall(ctx context.Context, tc llm.ToolCall, env *tools.Env, mode, sessionID string) (string, error) {
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

	if tc.Name == "run_command" {
		if env.RequirePermissionForCommands {
			if !env.CommandAllowed(extractCommand(tc.InputJSON)) {
				requiresPerm = true
			} else {
				requiresPerm = false
			}
		} else {
			requiresPerm = false
		}
	} else if (tc.Name == "write_file" || tc.Name == "apply_diff") && env.RequirePermissionForWrites {
		requiresPerm = true
	}

	// Outside CWD when restrict_to_cwd is false - still require explicit approval.
	if !env.RestrictToCWD && tools.ToolPathsEscapeCWD(tc.Name, tc.InputJSON, env.CWD) {
		requiresPerm = true
	}

	if requiresPerm {
		permResult, err := a.server.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: sessionID,
			ToolCall: acp.PermissionToolCall{
				ToolCallID: tc.ID,
				Title:      fmt.Sprintf("Run: %s", tc.Name),
				Kind:       toolKind(tc.Name),
				Status:     "pending",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: "text", Text: fmt.Sprintf("Arguments: %s", tc.InputJSON)}},
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

		// Handle switch_to_agent_mode.
		if tc.Name == "switch_to_agent_mode" && permResult.OptionID != "reject" {
			a.state.SetMode("agent")
			_ = a.server.SendSessionUpdate(sessionID, acp.ModeUpdate{
				SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
				ModeID:        "agent",
			})
			if st, ok := a.state.(*session.State); ok {
				_ = a.server.SendSessionUpdate(sessionID, acp.ConfigOptionUpdate{
					SessionUpdate: acp.UpdateTypeConfigOptionUpdate,
					ConfigOptions: session.BuildACPConfigOptions(a.cfg, st),
				})
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    tc.ID,
				Status:        "completed",
			})
			return "switched to agent mode", nil
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

	var content []acp.ToolCallResultItem
	if result != "" {
		// Truncate very long results for the notification (full result still sent to LLM).
		display := result
		if len(display) > 2000 {
			display = display[:2000] + "\n... (truncated)"
		}
		content = []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: display}},
		}
	}

	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        status,
		Content:       content,
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

// buildSystemPrompt constructs the system prompt for the current mode and skills.
func (a *Agent) buildSystemPrompt(mode string, activeSkills []*skills.Skill, toolDefs []llm.ToolDefinition) string {
	promptsDir := a.cfg.Prompts.ResolvedPromptsDir(a.state.GetCWD())
	return prompts.RenderWithFallback(mode, promptsDir, prompts.TemplateData{
		CWD:    a.state.GetCWD(),
		Skills: skills.BuildSystemPromptSection(activeSkills),
		Tools:  tools.FormatDefinitionsForPrompt(toolDefs),
		Memory: strings.TrimSpace(a.state.GetAgentMemory()),
	})
}

// buildMessages constructs the message slice to send to the LLM.
func (a *Agent) buildMessages(systemPrompt string) []llm.Message {
	history := a.state.GetMessages()
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	msgs = append(msgs, history...)
	return msgs
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

	def, err := a.cfg.FindModelDef(modelID)
	if err != nil {
		return nil, err
	}

	return llm.NewProvider(def.Provider, def.Model, def.APIKey, def.BaseURL, def.MaxTokens, def.Temperature)
}

// contentBlocksToText converts ACP content blocks to a plain text string.
func contentBlocksToText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource":
			if b.Resource != nil {
				parts = append(parts, fmt.Sprintf("[File: %s]\n%s", b.Resource.URI, b.Resource.Text))
			}
		}
	}
	return strings.Join(parts, "\n\n")
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
	switch {
	case name == "read_file" || name == "list_dir":
		return "read"
	case name == "write_file" || name == "write_text_file" || name == "apply_diff":
		return "write"
	case name == "run_command":
		return "run_command"
	case name == "switch_to_agent_mode":
		return "switch_mode"
	default:
		return "other"
	}
}

// extractCommand parses the "command" field from run_command JSON args.
func extractCommand(argsJSON string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return args.Command
}
