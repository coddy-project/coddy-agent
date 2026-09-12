//go:build http

package httpserver

import (
	"context"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// runDirectYAMLCompletion runs one non-ReAct LLM call for a configured models[].model
// selector and appends the assistant message. toolDefs are the caller's own tools,
// offered to the model as they are: a call the model makes is streamed back as an
// OpenAI tool_calls delta and kept on the assistant message, and running it is the
// caller's business.
func (s *Server) runDirectYAMLCompletion(ctx context.Context, st *session.State, sessionID, yamlSel string, bridge *Sender, toolDefs []llm.ToolDefinition) (*llm.Response, error) {
	mk := s.makeLLMFromYAML
	if mk == nil {
		mk = defaultMakeLLMFromYAML
	}
	provider, err := mk(s.activeCfg(), yamlSel)
	if err != nil {
		return nil, err
	}
	msgs := st.GetMessages()
	// emit still means "the client asked for a stream" here: this path only ever receives
	// senders built by NewSender. A relay-only sender must not reach it, or a request that
	// asked for a plain JSON body would silently switch to the provider's streaming API.
	if bridge != nil && bridge.emit {
		// A model configured with stream: false answers this call in one piece after the
		// whole completion is generated, so the response stays silent for as long as the
		// model takes and an idle-timeout proxy would drop it. Same guard the ReAct path
		// arms; harmless for a model that is actually streaming.
		stopKeepalive := bridge.StartIdleKeepalive()
		defer stopKeepalive()
		toolCallIndex := 0
		resp, err := provider.Stream(ctx, msgs, toolDefs, func(chunk llm.StreamChunk) {
			if chunk.ToolCall != nil {
				_ = bridge.SendToolCall(toolCallIndex, *chunk.ToolCall)
				toolCallIndex++
			}
			if chunk.TextDelta != "" {
				_ = bridge.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeAgentMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: chunk.TextDelta},
				})
			}
			if chunk.ReasoningDelta != "" {
				_ = bridge.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeAgentMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: chunk.ReasoningDelta},
				})
			}
		})
		if err != nil {
			return nil, err
		}
		if resp != nil && (resp.InputTokens > 0 || resp.OutputTokens > 0) {
			_ = bridge.SendSessionUpdate(sessionID, acp.TokenUsageUpdate{
				SessionUpdate: acp.UpdateTypeTokenUsage,
				InputTokens:   resp.InputTokens,
				OutputTokens:  resp.OutputTokens,
				TotalTokens:   resp.InputTokens + resp.OutputTokens,
			})
		}
		st.AddMessage(directAssistantMessage(resp, yamlSel))
		return resp, nil
	}
	resp, err := provider.Complete(ctx, msgs, toolDefs)
	if err != nil {
		return nil, err
	}
	st.AddMessage(directAssistantMessage(resp, yamlSel))
	return resp, nil
}

// directAssistantMessage is the transcript row of a direct completion: the text
// and, when the model called the caller's tools, those calls, so the tool result
// the caller sends next has its call to answer.
func directAssistantMessage(resp *llm.Response, yamlSel string) llm.Message {
	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Model:     yamlSel,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// resolveDirectYAMLMaxTokens returns the max_tokens value to send to the LLM
// for a direct single-turn YAML model completion (non-agent, non-plan).
// The configured value is used as-is; zero or negative falls back to 0 so the
// provider applies its own internal default.
func resolveDirectYAMLMaxTokens(rm *config.ResolvedLLM) int {
	if rm == nil || rm.MaxTokens <= 0 {
		return 0
	}
	return rm.MaxTokens
}

func maxContextDefault(s *Server) int {
	maxCtx := 128000
	if s.activeCfg() != nil {
		if ent := s.activeCfg().FindModelEntry(strings.TrimSpace(s.activeCfg().Agent.Model)); ent != nil {
			if ent.MaxContextTokens > 0 {
				maxCtx = ent.MaxContextTokens
			}
		}
	}
	return maxCtx
}
