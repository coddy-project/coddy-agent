package session

import "github.com/EvilFreelancer/coddy-agent/internal/llm"

// TurnSend is the shape of the last request a session put on the wire: the
// model it went to, the system message that opened it, the tool definitions
// beside it and the input its provider was built from. A provider caches a
// request by its prefix, so a later call over the same conversation reads the
// history back instead of paying for it only when all four are the turn's own.
//
// It carries no history: the messages are read from the transcript when such a
// call is built, which is what keeps this record small and always current. The
// compaction summarizer is its one reader today
// (internal/agent/compact_cached.go). The record belongs to the live session,
// is replaced at every request and is never persisted.
type TurnSend struct {
	// Model is the models[].model the transport was built for, which is what
	// tells a summarizer that is the session's own model from one that is not.
	Model string
	// System is the first message of that request, byte for byte.
	System llm.Message
	// Tools are the definitions the request carried.
	Tools []llm.ToolDefinition
	// Input is what the provider was built from, the reasoning level and the
	// output bound included: a side call built from anything else differs from
	// the turn in something a provider keys its cache on.
	Input llm.ProviderInput
}

// SetTurnSend records the request shape of the call about to go out.
func (s *State) SetTurnSend(send TurnSend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	send.Tools = append([]llm.ToolDefinition(nil), send.Tools...)
	s.turnSend = &send
}

// TurnSend returns the last recorded request shape of this session. ok is
// false before the session has sent anything in this process, which is where a
// call that wants to repeat a cached prefix has nothing to repeat.
func (s *State) TurnSend() (TurnSend, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.turnSend == nil {
		return TurnSend{}, false
	}
	return *s.turnSend, true
}
