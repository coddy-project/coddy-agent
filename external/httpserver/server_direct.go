//go:build http

package httpserver

import (
	"context"
	"fmt"
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
// caller's business. opts are the request's own generation options, applied to
// the provider built for this one call.
func (s *Server) runDirectYAMLCompletion(ctx context.Context, st *session.State, sessionID, yamlSel string, bridge *Sender, toolDefs []llm.ToolDefinition, opts llm.RequestOptions) (*llm.Response, error) {
	mk := s.makeLLMFromYAML
	if mk == nil {
		mk = defaultMakeLLMFromYAML
	}
	provider, err := mk(s.activeCfg(), yamlSel, opts)
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

// directRequestOptions reads the generation options of a direct completion
// request and checks them against the model and the provider type that
// serves it. max_tokens and max_completion_tokens are one cap under two
// names, so a request may carry both only when they agree. reasoning_effort
// must be a level the model offers, as GET /v1/models lists them; omitted,
// null or empty, the model's reasoning_default applies.
func directRequestOptions(cfg *config.Config, model string, req chatCompletionRequest) (llm.RequestOptions, error) {
	opts := llm.RequestOptions{MaxTokens: req.MaxTokens, Temperature: req.Temperature}
	if req.MaxCompletionTokens != nil {
		if req.MaxTokens != nil && *req.MaxTokens != *req.MaxCompletionTokens {
			return llm.RequestOptions{}, fmt.Errorf("max_tokens (%d) and max_completion_tokens (%d) disagree", *req.MaxTokens, *req.MaxCompletionTokens)
		}
		opts.MaxTokens = req.MaxCompletionTokens
	}
	var ent *config.ModelEntry
	providerType := ""
	if cfg != nil {
		if ent = cfg.FindModelEntry(model); ent != nil {
			if prov := cfg.FindProvider(ent.ProviderName()); prov != nil {
				providerType = prov.Type
			}
		}
	}
	level := ""
	if req.ReasoningEffort != nil {
		level = strings.TrimSpace(*req.ReasoningEffort)
	}
	switch {
	case level == "":
		if ent != nil {
			opts.ReasoningEffort = cfg.DefaultReasoningLevelFor(ent)
		}
	case reasoningLevelOffered(cfg, ent, level):
		opts.ReasoningEffort = level
	default:
		var levels []string
		if ent != nil {
			levels = cfg.ReasoningLevelsFor(ent)
		}
		if len(levels) == 0 {
			return llm.RequestOptions{}, fmt.Errorf("reasoning_effort %q: model %q offers no reasoning levels", level, model)
		}
		return llm.RequestOptions{}, fmt.Errorf("reasoning_effort %q is not offered by model %q (offered: %s)", level, model, strings.Join(levels, ", "))
	}
	return opts, opts.Validate(providerType)
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

// contextWindowFor is the context window GET /v1/models reports for modelRef:
// the one the session manager resolves for a session on that model, so the
// web UI ring and the automatic compaction trigger measure against the same
// number. An unconfigured model reads as the default.
func (s *Server) contextWindowFor(cfg *config.Config, modelRef string) int {
	var tokens int
	if s.mgr != nil {
		tokens, _ = s.mgr.ContextWindow(cfg, strings.TrimSpace(modelRef))
	} else if ent := cfg.FindModelEntry(strings.TrimSpace(modelRef)); ent != nil {
		tokens = ent.MaxContextTokens
	}
	if tokens <= 0 {
		return config.DefaultContextWindowTokens
	}
	return tokens
}
