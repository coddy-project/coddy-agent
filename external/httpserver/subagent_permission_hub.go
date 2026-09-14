//go:build http

package httpserver

// Permission prompts from a subagent whose parent turn has already ended.
//
// A detached run outlives the chat turn that started it, so its prompt has no
// stream to go to: the parent's sender is bound to a finished turn. The prompt
// is published here instead. The web UI finds it on the background task row the
// run belongs to and shows it in the chat of the parent session; a console
// attached over --remote hears about it on GET /coddy/events. Either answers
// through the ordinary POST /coddy/sessions/{id}/permission, addressed to the
// child session, which is the one actually waiting.
//
// Two properties make that work without a new route: the permission hub is
// process-wide and keyed by (session id, tool call id) rather than by a live
// turn, and the permission handler consults it before the read-only guard that
// rejects child sessions. Nothing is persisted: the waiter is a goroutine, so a
// prompt cannot outlive the process that raised it, and a record left on disk
// would only invite a resume that a read-only child transcript cannot have.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// detachedPermissionDTO is the JSON a client renders. The embedded params are
// the same shape the SSE "permission" event carries, so the SPA reuses its
// existing parser and preview; the rest is what a client needs on top of that
// to place the prompt and name who is asking.
type detachedPermissionDTO struct {
	acp.PermissionRequestParams
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	AskedAt         time.Time `json:"asked_at"`
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

// waitingDetachedPrompts lists every published prompt, oldest first, for the
// connect-time snapshot of the events stream.
func waitingDetachedPrompts() []*detachedPermissionDTO {
	detachedPromptsMu.Lock()
	out := make([]*detachedPermissionDTO, 0, len(detachedPrompts))
	for _, dto := range detachedPrompts {
		out = append(out, dto)
	}
	detachedPromptsMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].AskedAt.Before(out[j].AskedAt) })
	return out
}

// Phases of a subagent_permission event.
const (
	detachedPromptAsked   = "asked"
	detachedPromptSettled = "settled"
)

// subagentPermissionFrame renders one edge of a detached prompt as an SSE frame.
// An asked frame carries the request, because a client that shows no task rows
// has nowhere else to read it from; a settled frame only names the prompt, so a
// client that did not answer knows to take its copy down.
func subagentPermissionFrame(phase string, dto *detachedPermissionDTO) []byte {
	payload := map[string]interface{}{
		"object":          "coddy.subagent_permission",
		"phase":           phase,
		"parentSessionId": dto.ParentSessionID,
		"childSessionId":  dto.SessionID,
		"taskId":          dto.TaskID,
		"toolCallId":      dto.ToolCall.ToolCallID,
	}
	if phase == detachedPromptAsked {
		payload["agentName"] = dto.AgentName
		payload["askedAt"] = dto.AskedAt.UTC().Format(time.RFC3339Nano)
		payload["request"] = dto.PermissionRequestParams
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+40)
	frame = append(frame, "event: subagent_permission\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

func (s *Server) announceDetachedPrompt(phase string, dto *detachedPermissionDTO) {
	if s.events == nil {
		return
	}
	if frame := subagentPermissionFrame(phase, dto); frame != nil {
		s.events.publish(frame)
	}
}

// SetDetachedPrompts names the broker a turn this server builds itself hands its
// detached subagents. `coddy serve` passes its runtime, which offers the prompt
// to every surface of the process; nil (the default) means this server alone.
func (s *Server) SetDetachedPrompts(b agent.DetachedPermissionBroker) {
	s.detachedPrompts = b
}

func (s *Server) detachedPromptBroker() agent.DetachedPermissionBroker {
	if s.detachedPrompts != nil {
		return s.detachedPrompts
	}
	return s
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker: it
// publishes the prompt for the web UI and announces it to the other clients of
// this server, then blocks until the answer arrives through the permission
// endpoint, or ctx ends - the run's own context, or the runtime withdrawing the
// prompt because another surface answered it first.
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

	// Registered before the prompt is published, so an answer that arrives the
	// instant a client sees it already has somewhere to land.
	ch := registerPermissionWait(childID, toolCallID, "")
	defer unregisterPermissionWait(childID, toolCallID, "")

	params := req.Params
	params.SessionID = childID
	dto := &detachedPermissionDTO{
		PermissionRequestParams: params,
		ParentSessionID:         strings.TrimSpace(req.ParentSessionID),
		TaskID:                  strings.TrimSpace(req.TaskID),
		AgentName:               strings.TrimSpace(req.AgentName),
		AskedAt:                 time.Now().UTC(),
	}
	publishDetachedPrompt(childID, dto)
	s.announceDetachedPrompt(detachedPromptAsked, dto)
	defer func() {
		clearDetachedPrompt(childID, dto)
		s.announceDetachedPrompt(detachedPromptSettled, dto)
	}()
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
