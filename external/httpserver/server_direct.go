//go:build http

package httpserver

import (
	"context"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// runDirectYAMLCompletion runs one non-ReAct LLM call for a configured models[].model selector and appends the assistant message.
func (s *Server) runDirectYAMLCompletion(ctx context.Context, st *session.State, sessionID, yamlSel string, bridge *Sender) (*llm.Response, error) {
	mk := s.makeLLMFromYAML
	if mk == nil {
		mk = defaultMakeLLMFromYAML
	}
	provider, err := mk(s.cfg, yamlSel)
	if err != nil {
		return nil, err
	}
	msgs := st.GetMessages()
	var toolDefs []llm.ToolDefinition
	if bridge != nil && bridge.stream {
		resp, err := provider.Stream(ctx, msgs, toolDefs, func(chunk llm.StreamChunk) {
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
		out := strings.TrimSpace(resp.Content)
		st.AddMessage(llm.Message{
			Role:      llm.RoleAssistant,
			Content:   out,
			Model:     yamlSel,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		return resp, nil
	}
	resp, err := provider.Complete(ctx, msgs, toolDefs)
	if err != nil {
		return nil, err
	}
	out := strings.TrimSpace(resp.Content)
	st.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   out,
		Model:     yamlSel,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return resp, nil
}

func maxContextDefault(s *Server) int {
	maxCtx := 128000
	if s.cfg != nil {
		if ent := s.cfg.FindModelEntry(strings.TrimSpace(s.cfg.Agent.Model)); ent != nil {
			if ent.MaxContextTokens > 0 {
				maxCtx = ent.MaxContextTokens
			}
		}
	}
	return maxCtx
}
