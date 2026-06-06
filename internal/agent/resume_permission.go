package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// ResumeAfterPermission executes a tool call that was approved via POST /permission after the HTTP
// stream ended or the server restarted, then continues the ReAct loop from persisted messages.
func (a *Agent) ResumeAfterPermission(ctx context.Context, toolCallID string, perm *acp.PermissionResult) (string, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return "", fmt.Errorf("toolCallId is required")
	}
	if perm == nil {
		return "", fmt.Errorf("permission result is nil")
	}
	tc, err := a.findPendingToolCall(toolCallID)
	if err != nil {
		return "", err
	}
	mode := a.state.GetMode()
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	toolEnv := a.buildToolEnv(mode, sd)
	if st := sessionStatePtr(a.state); st != nil {
		permission.RecordAllowAlways(st, tc.Name, tc.InputJSON, toolEnv.CWD, perm)
	}
	if sd != "" {
		_ = session.ClearPendingPermission(sd)
	}
	if perm.Outcome == "cancelled" || perm.OptionID == "reject" {
		toolResultMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    "permission denied by user",
			ToolCallID: tc.ID,
		}
		a.state.AddMessage(toolResultMsg)
		if sd != "" {
			_ = session.WriteToolCallResult(sd, tc.ID, toolResultMsg.Content)
			_ = session.MarkToolCallFinished(sd, tc.ID, tc.Name, toolKind(tc.Name), "cancelled")
		}
		return a.continueReAct(ctx, mode, toolEnv)
	}
	result, execErr := a.executeToolCall(ctx, tc, toolEnv, mode, a.state.GetID(), true)
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
	a.state.AddMessage(toolResultMsg)
	return a.continueReAct(ctx, mode, toolEnv)
}

func (a *Agent) findPendingToolCall(toolCallID string) (llm.ToolCall, error) {
	msgs := a.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == llm.RoleTool && strings.TrimSpace(m.ToolCallID) == toolCallID {
			return llm.ToolCall{}, fmt.Errorf("tool call %s already has a result", toolCallID)
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if strings.TrimSpace(tc.ID) == toolCallID {
				return tc, nil
			}
		}
	}
	return llm.ToolCall{}, fmt.Errorf("tool call %s not found in session history", toolCallID)
}

func (a *Agent) buildToolEnv(mode, sessionDir string) *tools.Env {
	return &tools.Env{
		CWD:              a.state.GetCWD(),
		PermissionMode:   effectivePermMode(a.state, a.cfg),
		CommandAllowlist: a.cfg.Tools.CommandAllowlist,
		SessionID:                    a.state.GetID(),
		SessionDir:                   sessionDir,
		ArchiveActiveMarkdown: func() error {
			if sessionDir == "" {
				return nil
			}
			return session.ArchiveActiveTodo(sessionDir)
		},
		WriteArchivedPlanMarkdown: func(md string) (string, error) {
			if sessionDir == "" {
				return "", nil
			}
			return session.WritePlanArchivedMarkdown(sessionDir, md)
		},
		Sender:  a.server,
		GetPlan: a.state.GetPlan,
		SetPlan: a.state.SetPlan,
		SetSessionMode: func(m string) error {
			a.state.SetMode(strings.TrimSpace(m))
			return nil
		},
		PersistPlanDocument: func(doc plans.Document) {
			a.state.AppendPlanDocument(doc)
		},
	}
}

// continueReAct runs the ReAct loop using messages already on the session (no new user turn).
func (a *Agent) continueReAct(ctx context.Context, mode string, toolEnv *tools.Env) (string, error) {
	userText := lastUserText(a.state.GetMessages())
	contextFiles := extractContextFiles(nil)
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
	provider, err := a.getProvider(mode)
	if err != nil {
		return string(acp.StopReasonRefused), fmt.Errorf("no LLM configured: %w", err)
	}
	messages := a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))
	maxTurns := a.cfg.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	toolEnv.SendDesignPlanUpdate = func(doc plans.Document) {
		tools.SendDesignPlanUpdate(toolEnv, doc)
	}
	return a.runReActLoop(ctx, mode, messages, toolDefs, provider, toolEnv, sd, userText, contextFiles, activeSkills, maxTurns)
}

func lastUserText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}
