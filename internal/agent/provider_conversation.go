package agent

// What the loop tells a provider about the session it serves: the session's
// id, workspace and modes, and a way to ask the person. The acp provider uses
// it to keep one ACP session per Coddy session and to forward the agent's own
// permission requests to the surface that answers Coddy's.

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// providerConversation describes the session to the provider of one Stream
// call. It goes on that call's context only: helpers that run under the
// turn's context (compaction, titles, the goal judge) are not part of the
// session's conversation with the provider.
func (a *Agent) providerConversation(mode string, env *tools.Env) llm.Conversation {
	permMode := effectivePermMode(a.state, a.cfg)
	if env != nil && env.PermissionMode != "" {
		permMode = env.PermissionMode
	}
	restricted := false
	if st := sessionStatePtr(a.state); st != nil {
		if r := st.GetTurnRestriction(); r != nil && r.AllowedTools != nil {
			restricted = true
		}
	}
	return llm.Conversation{
		SessionID:      a.state.GetID(),
		CWD:            a.state.GetCWD(),
		Mode:           mode,
		PermissionMode: permMode,
		Restricted:     restricted,
		AskPermission: func(ctx context.Context, req llm.PermissionRequest) (llm.PermissionAnswer, error) {
			return a.askProviderPermission(ctx, mode, permMode, req)
		},
	}
}

// providerToolPrefix opens the title of a provider's permission request, so
// the person tells the agent's tool from one of Coddy's own.
const providerToolPrefix = "Agent: "

// providerToolKind maps an ACP tool kind onto the kinds Coddy's surfaces draw
// its own tool rows with (session.ToolKind).
func providerToolKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "read", "search", "fetch":
		return "read"
	case "edit", "delete", "move":
		return "write"
	case "execute":
		return "run_command"
	default:
		return "other"
	}
}

// askProviderPermission puts a provider's permission request in front of the
// person, through the same prompt Coddy's own tools use. The request is
// shown as a tool row of its own first, so an editor that renders a prompt
// only for a call it has seen shows this one, and stamped with the session's
// permission mode, so every surface relays it live and none stores it as a
// Coddy tool call to resume after a restart.
func (a *Agent) askProviderPermission(ctx context.Context, mode, permMode string, req llm.PermissionRequest) (llm.PermissionAnswer, error) {
	sessionID := a.state.GetID()
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "a tool"
	}
	if !strings.HasPrefix(title, providerToolPrefix) {
		// An agent that is another Coddy names its own agent's request so
		// already: the person reads the prefix once, however deep the chain.
		title = providerToolPrefix + title
	}
	kind := providerToolKind(req.Kind)
	body := strings.TrimSpace(req.Body)
	if body == "" {
		body = title
	}
	options := make([]acp.PermissionOption, 0, len(req.Options))
	for _, o := range req.Options {
		options = append(options, acp.PermissionOption{OptionID: o.ID, Name: o.Name, Kind: o.Kind})
	}
	toolCallID := "acp_" + req.ToolCallID
	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
		SessionUpdate: acp.UpdateTypeToolCall,
		ToolCallID:    toolCallID,
		Title:         title,
		Kind:          kind,
		Status:        "pending",
	})
	// Notification hooks learn that a prompt waits for the person, as they
	// do for Coddy's own tools.
	a.runNotificationHooks(ctx, mode, hookNotificationPermissionPrompt, llm.ToolCall{ID: toolCallID, Name: "acp_agent_tool", InputJSON: "{}"}, body)
	answered := waitingFor(&pendingPermissions)
	res, err := a.server.RequestPermission(ctx, acp.PermissionRequestParams{
		SessionID: sessionID,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: toolCallID,
			Title:      title,
			Kind:       kind,
			Status:     "pending",
			Content: []acp.ToolCallResultItem{
				{Type: "content", Content: acp.ContentBlock{Type: "text", Text: body}},
			},
		},
		Options:                 options,
		EffectivePermissionMode: permMode,
		SessionPermissionMode:   permMode,
	})
	answered()
	// The surfaces answer in the vocabulary of Coddy's own gate: a shortcut
	// approval may carry no option id at all (a plan run's sender), and a
	// refusal is the reject id or a cancelled outcome.
	answer := llm.PermissionAnswer{Cancelled: true}
	approved := err == nil && permission.Approved(res)
	if approved {
		id := res.OptionID
		if id == "" {
			id = "allow"
		}
		answer = llm.PermissionAnswer{OptionID: id}
	} else if err == nil && res != nil && res.OptionID != "" && res.Outcome != "cancelled" {
		answer = llm.PermissionAnswer{OptionID: res.OptionID}
	}
	status := "cancelled"
	if approved {
		status = "completed"
	}
	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    toolCallID,
		Status:        status,
	})
	return answer, err
}
