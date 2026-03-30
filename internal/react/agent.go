// Package react implements the ReAct (Reasoning + Acting) agent loop.
// The agent interleaves LLM inference with tool execution until the task
// is complete or a turn/token limit is reached.
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

// SessionState is the minimal interface react.Agent requires from a session.
// Defined here to avoid circular imports with the session package.
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
}

// Agent is the ReAct agent that drives the LLM + tool loop.
type Agent struct {
	cfg    *config.Config
	state  SessionState
	server *acp.Server
	log    *slog.Logger
}

// NewAgent creates a new Agent for the given session.
func NewAgent(cfg *config.Config, state SessionState, server *acp.Server, log *slog.Logger) *Agent {
	return &Agent{
		cfg:    cfg,
		state:  state,
		server: server,
		log:    log,
	}
}

// Run executes the ReAct loop for a single prompt turn.
// It returns the final assistant text response.
func (a *Agent) Run(ctx context.Context, prompt []acp.ContentBlock) (string, error) {
	mode := a.state.GetMode()
	if mode == "" {
		mode = "agent"
	}

	// Build the LLM provider for this mode.
	provider, err := a.buildProvider(mode)
	if err != nil {
		return "", fmt.Errorf("build provider: %w", err)
	}

	// Build the tool registry, adding MCP tools from connected servers.
	registry := tools.NewRegistry()
	for _, client := range a.state.GetMCPClients() {
		for _, t := range client.Tools() {
			registry.RegisterMCPTool(client.Name(), &tools.Tool{
				Definition: llm.ToolDefinition{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
				},
				AllowedInPlanMode: false,
				Execute: func(ctx context.Context, argsJSON string, env *tools.Env) (string, error) {
					result, err := client.CallTool(ctx, t.Name, argsJSON)
					if err != nil {
						return "", err
					}
					return result, nil
				},
			})
		}
	}

	// Prepare the tool environment.
	env := &tools.Env{
		CWD:                          a.state.GetCWD(),
		RestrictToCWD:                a.cfg.Tools.RestrictToCWD,
		RequirePermissionForCommands: a.cfg.Tools.RequirePermissionForCommands,
		RequirePermissionForWrites:   a.cfg.Tools.RequirePermissionForWrites,
		CommandAllowlist:             a.cfg.Tools.CommandAllowlist,
	}

	// Build initial messages from existing history + new user prompt.
	activeSkills := a.state.GetSkills()
	systemPrompt := a.buildSystemPrompt(mode, activeSkills)

	userContent := contentBlocksToText(prompt)
	a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: userContent})

	// Notify client about the user message.
	if a.server != nil {
		_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
			"sessionUpdate": acp.UpdateTypeUserMessageChunk,
			"content":       acp.ContentBlock{Type: "text", Text: userContent},
		})
	}

	toolDefs := registry.ToolsForMode(mode)

	maxTurns := a.cfg.React.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}

	var finalResponse string

	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return finalResponse, ctx.Err()
		}

		messages := a.buildMessages(systemPrompt)

		a.log.Debug("react turn",
			"session", a.state.GetID(),
			"turn", turn,
			"mode", mode,
			"messages", len(messages),
		)

		// Call the LLM with streaming.
		var resp *llm.Response
		resp, err = provider.Stream(ctx, messages, toolDefs, func(chunk llm.StreamChunk) {
			if chunk.TextDelta != "" && a.server != nil {
				_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
					"sessionUpdate": acp.UpdateTypeAgentMessageChunk,
					"content":       acp.ContentBlock{Type: "text", Text: chunk.TextDelta},
				})
			}
		})
		if err != nil {
			return finalResponse, fmt.Errorf("LLM call: %w", err)
		}

		// Store the assistant message in history.
		assistantMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		a.state.AddMessage(assistantMsg)
		finalResponse = resp.Content

		// If no tool calls, the agent is done.
		if len(resp.ToolCalls) == 0 {
			a.log.Debug("react loop complete - no tool calls", "turn", turn)
			break
		}

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			// Check for mode switch.
			if tc.Name == "switch_to_agent_mode" {
				a.state.SetMode("agent")
				mode = "agent"
				toolDefs = registry.ToolsForMode(mode)
				activeSkills = a.state.GetSkills()
				systemPrompt = a.buildSystemPrompt(mode, activeSkills)

				if a.server != nil {
					_ = a.server.SendSessionUpdate(a.state.GetID(), acp.ModeUpdate{
						SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
						ModeID:        "agent",
					})
					if st, ok := a.state.(*session.State); ok {
						_ = a.server.SendSessionUpdate(a.state.GetID(), acp.ConfigOptionUpdate{
							SessionUpdate: acp.UpdateTypeConfigOptionUpdate,
							ConfigOptions: session.BuildACPConfigOptions(a.cfg, st),
						})
					}
				}

				a.state.AddMessage(llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					Content:    "Switched to agent mode.",
				})
				continue
			}

			result, execErr := a.executeToolCall(ctx, tc, env, mode, registry)
			toolResult := result
			if execErr != nil {
				toolResult = fmt.Sprintf("Error: %v", execErr)
			}

			a.state.AddMessage(llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Content:    toolResult,
			})
		}
	}

	return finalResponse, nil
}

// buildProvider constructs the LLM provider for the given mode.
func (a *Agent) buildProvider(mode string) (llm.Provider, error) {
	modelID := a.state.EffectiveModelID(a.cfg)
	if modelID == "" {
		return nil, fmt.Errorf("no model configured for mode %q - set models.default or models.%s_mode in config", mode, mode)
	}

	def, err := a.cfg.FindModelDef(modelID)
	if err != nil {
		return nil, err
	}

	return llm.NewProvider(def.Provider, def.Model, def.APIKey, def.BaseURL, def.MaxTokens, def.Temperature)
}

// buildSystemPrompt constructs the full system prompt for the given mode.
func (a *Agent) buildSystemPrompt(mode string, activeSkills []*skills.Skill) string {
	var customFile, extra string
	switch mode {
	case "plan":
		customFile = a.cfg.Prompts.PlanFile
		extra = a.cfg.Prompts.PlanExtra
	default:
		customFile = a.cfg.Prompts.AgentFile
		extra = a.cfg.Prompts.AgentExtra
	}

	base := prompts.RenderWithFallback(mode, customFile, prompts.TemplateData{
		CWD:               a.state.GetCWD(),
		ExtraInstructions: extra,
	})

	if len(activeSkills) == 0 {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n---\n\n## Active Skills & Rules\n\n")
	for _, s := range activeSkills {
		sb.WriteString("### ")
		sb.WriteString(s.Name)
		sb.WriteString("\n\n")
		sb.WriteString(s.Content)
		sb.WriteString("\n\n")
	}

	return strings.TrimSpace(sb.String())
}

// buildMessages assembles the full message list for an LLM call.
func (a *Agent) buildMessages(systemPrompt string) []llm.Message {
	history := a.state.GetMessages()
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt,
	})
	msgs = append(msgs, history...)
	return msgs
}

// executeToolCall runs a single tool call and returns the result string.
func (a *Agent) executeToolCall(ctx context.Context, tc llm.ToolCall, env *tools.Env, mode string, registry *tools.Registry) (string, error) {
	a.log.Debug("executing tool", "name", tc.Name, "session", a.state.GetID())

	// Determine if permission is required.
	requiresPerm := false
	toolKind := toolKindFor(tc.Name)

	if tc.Name == "run_command" && env.RequirePermissionForCommands {
		cmd := extractCommand(tc.InputJSON)
		if !env.CommandAllowed(cmd) {
			requiresPerm = true
		}
	} else if (tc.Name == "write_file" || tc.Name == "apply_diff") && env.RequirePermissionForWrites {
		requiresPerm = true
	}

	if !env.RestrictToCWD && tools.ToolPathsEscapeCWD(tc.Name, tc.InputJSON, env.CWD) {
		requiresPerm = true
	}

	// Notify client about the pending tool call.
	if a.server != nil {
		_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
			"sessionUpdate": acp.UpdateTypeToolCall,
			"toolCallId":    tc.ID,
			"title":         toolTitle(tc),
			"kind":          toolKind,
			"status":        "pending",
		})
	}

	if requiresPerm && a.server != nil {
		permResult, err := a.server.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: a.state.GetID(),
			ToolCall: acp.PermissionToolCall{
				ToolCallID: tc.ID,
				Title:      toolTitle(tc),
				Kind:       toolKind,
				Status:     "pending",
			},
			Options: []acp.PermissionOption{
				{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
				{OptionID: "reject_once", Name: "Reject", Kind: "reject_once"},
			},
		})
		if err != nil || permResult == nil || permResult.Outcome == "cancelled" || permResult.OptionID == "reject_once" {
			if a.server != nil {
				_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
					"sessionUpdate": acp.UpdateTypeToolCallUpdate,
					"toolCallId":    tc.ID,
					"status":        "cancelled",
				})
			}
			return "Tool call rejected by user.", nil
		}
	}

	// Mark in-progress.
	if a.server != nil {
		_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
			"sessionUpdate": acp.UpdateTypeToolCallUpdate,
			"toolCallId":    tc.ID,
			"status":        "in_progress",
		})
	}

	result, err := registry.Execute(ctx, tc.Name, tc.InputJSON, env)

	// Notify client of completion.
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if a.server != nil {
		content := result
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
		}
		_ = a.server.SendSessionUpdate(a.state.GetID(), map[string]interface{}{
			"sessionUpdate": acp.UpdateTypeToolCallUpdate,
			"toolCallId":    tc.ID,
			"status":        status,
			"content": []acp.ToolCallResultItem{{
				Type:    "content",
				Content: acp.ContentBlock{Type: "text", Text: content},
			}},
		})
	}

	return result, err
}

// contentBlocksToText converts ACP content blocks to a plain text string.
func contentBlocksToText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "resource":
			if b.Resource != nil && b.Resource.Text != "" {
				parts = append(parts, fmt.Sprintf("[%s]\n%s", b.Resource.URI, b.Resource.Text))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// extractCommand extracts the command string from run_command args JSON.
func extractCommand(argsJSON string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return args.Command
}

// toolKindFor maps a tool name to its ACP kind string.
func toolKindFor(name string) string {
	switch name {
	case "read_file", "list_dir", "search_files":
		return "read"
	case "write_file", "apply_diff":
		return "write"
	case "run_command":
		return "run_command"
	case "switch_to_agent_mode":
		return "switch_mode"
	default:
		return "other"
	}
}

// toolTitle returns a human-readable title for a tool call.
func toolTitle(tc llm.ToolCall) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.InputJSON), &args); err != nil {
		return tc.Name
	}

	switch tc.Name {
	case "read_file", "write_file":
		if p, ok := args["path"].(string); ok {
			return fmt.Sprintf("%s: %s", tc.Name, p)
		}
	case "run_command":
		if c, ok := args["command"].(string); ok {
			return fmt.Sprintf("$ %s", c)
		}
	case "search_files":
		if p, ok := args["pattern"].(string); ok {
			return fmt.Sprintf("search: %s", p)
		}
	case "apply_diff":
		if p, ok := args["path"].(string); ok {
			return fmt.Sprintf("patch: %s", p)
		}
	}

	return tc.Name
}
