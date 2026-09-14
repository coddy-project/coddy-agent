package remote

// Permission prompts of background subagents running on the server.
//
// A subagent spawned with background: true outlives the turn that started it,
// so when it needs a permission there is no turn stream left to carry the
// prompt. The server announces it on GET /coddy/events instead, and a console
// attached over --remote shows it through the same modal as any other prompt -
// for the sessions this client opened, never for somebody else's - and posts
// the answer to the child session. The first answer from any surface wins: when
// a browser or a chat answers first, the server announces the prompt settled
// and the console takes its copy down.

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// detachedPromptsState tracks the prompts the server announced as waiting.
type detachedPromptsState struct {
	detachedMu sync.Mutex
	// detached is keyed by child session id and tool call id; a prompt stays
	// here from its asked frame to its settled frame, whether or not this
	// client showed it.
	detached   map[string]*detachedPrompt
	detachedWG sync.WaitGroup
}

type detachedPrompt struct {
	parentID string
	params   acp.PermissionRequestParams
	// cancel is set once the prompt is offered to the console, and takes the
	// modal down when the prompt is settled elsewhere.
	cancel context.CancelFunc
}

// detachedPromptFrame is the payload of a subagent_permission event.
type detachedPromptFrame struct {
	Phase           string                      `json:"phase"`
	ParentSessionID string                      `json:"parentSessionId"`
	ChildSessionID  string                      `json:"childSessionId"`
	ToolCallID      string                      `json:"toolCallId"`
	Request         acp.PermissionRequestParams `json:"request"`
}

func detachedPromptKey(childID, toolCallID string) string {
	return childID + "\x00" + toolCallID
}

// applyDetachedPromptEvent records an asked or settled prompt and offers an
// asked one when its parent is a session this client opened.
func (h *Handler) applyDetachedPromptEvent(data string) {
	var f detachedPromptFrame
	if json.Unmarshal([]byte(data), &f) != nil {
		return
	}
	childID := strings.TrimSpace(f.ChildSessionID)
	toolCallID := strings.TrimSpace(f.ToolCallID)
	if childID == "" || toolCallID == "" {
		return
	}
	key := detachedPromptKey(childID, toolCallID)
	switch f.Phase {
	case "asked":
		params := f.Request
		params.SessionID = childID
		params.ToolCall.ToolCallID = toolCallID
		p := &detachedPrompt{parentID: strings.TrimSpace(f.ParentSessionID), params: params}
		h.detachedMu.Lock()
		if _, seen := h.detached[key]; seen {
			// A reconnect replays what is still waiting; it is already here.
			h.detachedMu.Unlock()
			return
		}
		if h.detached == nil {
			h.detached = make(map[string]*detachedPrompt)
		}
		h.detached[key] = p
		h.detachedMu.Unlock()
		if h.knowsSession(p.parentID) {
			h.offerDetachedPrompt(key, p)
		}
	case "settled":
		h.detachedMu.Lock()
		p := h.detached[key]
		delete(h.detached, key)
		h.detachedMu.Unlock()
		if p != nil && p.cancel != nil {
			p.cancel()
		}
	}
}

// knowsSession reports whether this client opened the session: a prompt of a
// session it never showed belongs on somebody else's screen.
func (h *Handler) knowsSession(id string) bool {
	if id == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[id]
	return ok
}

// offerDetachedPromptsFor shows the prompts that were announced before this
// client opened their parent session.
func (h *Handler) offerDetachedPromptsFor(sessionID string) {
	type waiting struct {
		key string
		p   *detachedPrompt
	}
	var todo []waiting
	h.detachedMu.Lock()
	for key, p := range h.detached {
		if p.parentID == sessionID && p.cancel == nil {
			todo = append(todo, waiting{key: key, p: p})
		}
	}
	h.detachedMu.Unlock()
	for _, w := range todo {
		h.offerDetachedPrompt(w.key, w.p)
	}
}

// offerDetachedPrompt asks the console and posts the answer to the child
// session. It gives up without answering when the prompt is settled elsewhere
// or the client closes.
func (h *Handler) offerDetachedPrompt(key string, p *detachedPrompt) {
	h.mu.Lock()
	sender := h.sender
	h.mu.Unlock()
	if sender == nil {
		return
	}
	ctx, cancel := context.WithCancel(h.controlCtx)
	h.detachedMu.Lock()
	if p.cancel != nil || h.detached[key] != p {
		h.detachedMu.Unlock()
		cancel()
		return
	}
	p.cancel = cancel
	h.detachedWG.Add(1)
	h.detachedMu.Unlock()

	go func() {
		defer h.detachedWG.Done()
		defer cancel()
		res, err := sender.RequestPermission(ctx, p.params)
		if ctx.Err() != nil {
			return
		}
		optionID := "reject"
		if err == nil && res != nil && res.OptionID != "" {
			optionID = res.OptionID
		}
		answer := map[string]string{"toolCallId": p.params.ToolCall.ToolCallID, "optionId": optionID}
		path := "/coddy/sessions/" + url.PathEscape(p.params.SessionID) + "/permission"
		if perr := h.postJSON(ctx, path, answer, nil); perr != nil {
			if isStaleAnswer(perr) {
				h.log.Debug("remote subagent permission answer ignored, prompt already settled",
					"session", p.params.SessionID, "toolCallId", p.params.ToolCall.ToolCallID, "error", perr)
				return
			}
			h.log.Warn("remote subagent permission answer failed",
				"session", p.params.SessionID, "toolCallId", p.params.ToolCall.ToolCallID, "error", perr)
		}
	}()
}
