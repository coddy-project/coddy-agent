//go:build http

package httpserver

// Permission prompts from a subagent whose parent turn has already ended.
//
// A detached run outlives the chat turn that started it, so its prompt has no
// stream to go to: the parent's sender is bound to a finished turn. The prompt
// is published here instead, hangs on the background task row the run belongs
// to, and is answered through the ordinary POST /coddy/sessions/{id}/permission
// - addressed to the child session, which is the one actually waiting.
//
// Two properties make that work without a new route: the permission hub is
// process-wide and keyed by (session id, tool call id) rather than by a live
// turn, and the permission handler consults it before the read-only guard that
// rejects child sessions. Nothing is persisted: the waiter is a goroutine, so a
// prompt cannot outlive the process that raised it, and a record left on disk
// would only invite a resume that a read-only child transcript cannot have.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// detachedPermissionDTO is the JSON a client renders. The embedded params are
// the same shape the SSE "permission" event carries, so the SPA reuses its
// existing parser and preview; agent_name and asked_at are what a task row
// needs on top of that.
type detachedPermissionDTO struct {
	acp.PermissionRequestParams
	AgentName string    `json:"agent_name,omitempty"`
	AskedAt   time.Time `json:"asked_at"`
}

var (
	detachedPromptsMu sync.Mutex
	// Keyed by child session id: one child runs one turn and its tool calls
	// one at a time, so it has at most one prompt in flight.
	detachedPrompts = map[string]*detachedPermissionDTO{}
)

func publishDetachedPrompt(childSessionID string, dto *detachedPermissionDTO) {
	detachedPromptsMu.Lock()
	defer detachedPromptsMu.Unlock()
	detachedPrompts[childSessionID] = dto
}

// clearDetachedPrompt removes the child's prompt only while it is still the one
// this waiter published, so a late cleanup cannot drop a newer prompt.
func clearDetachedPrompt(childSessionID string, dto *detachedPermissionDTO) {
	detachedPromptsMu.Lock()
	defer detachedPromptsMu.Unlock()
	if detachedPrompts[childSessionID] == dto {
		delete(detachedPrompts, childSessionID)
	}
}

// pendingDetachedPermission returns the prompt a child is waiting on, or nil.
func pendingDetachedPermission(childSessionID string) *detachedPermissionDTO {
	id := strings.TrimSpace(childSessionID)
	if id == "" {
		return nil
	}
	detachedPromptsMu.Lock()
	defer detachedPromptsMu.Unlock()
	return detachedPrompts[id]
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker: it hangs
// the prompt on the child's task row and blocks until the answer arrives through
// the permission endpoint, or ctx - the run's own context - ends.
func (s *Server) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	childID := strings.TrimSpace(req.ChildSessionID)
	toolCallID := strings.TrimSpace(req.Params.ToolCall.ToolCallID)
	if childID == "" || toolCallID == "" {
		return nil, nil
	}
	// The child's own effective mode decides the short-circuit, exactly as it
	// does on the live path: a child narrowed to bypass is not asked at all.
	if strings.TrimSpace(req.Params.EffectivePermissionMode) == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}

	// Registered before the row is published, so an answer that arrives the
	// instant a client sees the prompt already has somewhere to land.
	ch := registerPermissionWait(childID, toolCallID, "")
	defer unregisterPermissionWait(childID, toolCallID, "")

	params := req.Params
	params.SessionID = childID
	dto := &detachedPermissionDTO{
		PermissionRequestParams: params,
		AgentName:               strings.TrimSpace(req.AgentName),
		AskedAt:                 time.Now().UTC(),
	}
	publishDetachedPrompt(childID, dto)
	defer clearDetachedPrompt(childID, dto)
	s.log.Info("detached subagent waits for permission",
		"parent", req.ParentSessionID, "child", childID, "task", req.TaskID,
		"agent", dto.AgentName, "toolCallId", toolCallID)

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		// The pool's timeout, an explicit stop and Drain's StopAll all cancel
		// the run, so a waiting task cannot outlive its deadline or hold up
		// shutdown.
		return nil, nil
	}
}

var _ agent.DetachedPermissionBroker = (*Server)(nil)
