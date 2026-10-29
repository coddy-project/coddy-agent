package agent

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// toolCallIDs gives the calls of one model response ids that no other call of
// the session carries.
//
// The id comes from the provider, and everything kept about a call is found by
// it: the row a client draws and the permission prompt under it, the record
// under tool_calls/, the result paired with the call in the history. A model
// that writes its own ids ("functions.write:0") can hand out one an earlier call
// already carries, and the web UI then drew the new call, and the prompt waiting
// on it, on the row of the old one far up the transcript (issue #567). Such a
// call keeps the provider's id with the first free numeric suffix ("_2", "_3"),
// decided once per provider id and response, before anything sees the call:
// every chunk of a streamed call and the complete call carry the same id, and
// that is the id the history replays to the provider.
type toolCallIDs struct {
	history    map[string]bool
	sessionDir string
	// given maps a provider id to the id the chunks of this response carry.
	given map[string]string
	// taken holds the ids this response handed out.
	taken map[string]bool
}

// newToolCallIDs reads the ids the session already holds from the history it
// is about to send and, through the bundle at sessionDir (empty for a session
// that keeps no bundle), from the records of calls the history no longer has.
func newToolCallIDs(history []llm.Message, sessionDir string) *toolCallIDs {
	ids := &toolCallIDs{
		history:    make(map[string]bool),
		sessionDir: strings.TrimSpace(sessionDir),
		given:      make(map[string]string),
		taken:      make(map[string]bool),
	}
	for _, m := range history {
		for _, tc := range m.ToolCalls {
			ids.history[tc.ID] = true
		}
		if m.ToolCallID != "" {
			ids.history[m.ToolCallID] = true
		}
	}
	return ids
}

func (ids *toolCallIDs) inUse(id string) bool {
	if ids.taken[id] || ids.history[id] {
		return true
	}
	return ids.sessionDir != "" && session.ToolCallRecorded(ids.sessionDir, id)
}

// claim hands out the provider's id, or the first suffixed form of it no call
// of the session carries.
func (ids *toolCallIDs) claim(id string) string {
	out := id
	for n := 2; ids.inUse(out); n++ {
		out = fmt.Sprintf("%s_%d", id, n)
	}
	ids.taken[out] = true
	return out
}

// streamed returns the id every chunk of the call the provider sent under id
// carries. An empty id is left as it is: the pairing check refuses it.
func (ids *toolCallIDs) streamed(id string) string {
	if strings.TrimSpace(id) == "" {
		return id
	}
	if got, ok := ids.given[id]; ok {
		return got
	}
	got := ids.claim(id)
	ids.given[id] = got
	return got
}

func (ids *toolCallIDs) retag(tc *llm.ToolCall) *llm.ToolCall {
	if tc == nil {
		return nil
	}
	id := ids.streamed(tc.ID)
	if id == tc.ID {
		return tc
	}
	out := *tc
	out.ID = id
	return &out
}

// chunk returns the chunk with its calls under the ids they end up with. The
// provider's values are copied, never changed in place.
func (ids *toolCallIDs) chunk(c llm.StreamChunk) llm.StreamChunk {
	c.ToolCallNamed = ids.retag(c.ToolCallNamed)
	c.ToolCallDelta = ids.retag(c.ToolCallDelta)
	c.ToolCall = ids.retag(c.ToolCall)
	return c
}

// calls returns the response's calls under ids of their own: the first call
// under a provider id takes the id its chunks carried, and a later call of the
// same response under that id gets another one.
func (ids *toolCallIDs) calls(calls []llm.ToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]llm.ToolCall, len(calls))
	placed := make(map[string]bool, len(calls))
	for i, tc := range calls {
		out[i] = tc
		if strings.TrimSpace(tc.ID) == "" {
			continue
		}
		if placed[tc.ID] {
			out[i].ID = ids.claim(tc.ID)
			continue
		}
		placed[tc.ID] = true
		out[i].ID = ids.streamed(tc.ID)
	}
	return out
}
