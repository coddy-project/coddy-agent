package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const recallSystem = `You are the memory copilot for a coding agent. You never speak to the end user directly.
Your job is to retrieve useful long-term facts before the main assistant answers.

You may only use the provided memory tools (coddy_memory_*). Paths are always scope:relative where scope is global or project.
Global memory uses memory.dir from config when set, otherwise $CODDY_HOME/memory (often ~/.coddy/memory). Project memory is always <session cwd>/memory.

Rules:
- Call coddy_memory_search first unless you already know the exact path to read.
- Prefer short factual bullets in your final reply. Do not expose raw tool JSON.
- If nothing is relevant, reply with a single line: (no memory hits)
- When you finish gathering, answer in plain text without further tool calls.`

const judgeSystem = `You are a strict memory curator for a coding agent. You decide whether to persist a distilled fact from one assistant turn.
Reply with a single JSON object only, no markdown fences. Schema:
{"save":false,"title":"","body":"","scope":"global","reason":"..."}
or when save is true use scope "global" or "project". Body must be markdown, at most 900 characters, no secrets or one-off tokens.
Set save true only if the fact is reusable across future sessions and not already trivial chat noise.`

func clampProviderMax(rm *config.ResolvedLLM, cap int) {
	if rm == nil || cap <= 0 {
		return
	}
	if rm.MaxTokens <= 0 || rm.MaxTokens > cap {
		rm.MaxTokens = cap
	}
}

func newCopilotProvider(cfg *config.Config, modelRef string) (llm.Provider, error) {
	ref := strings.TrimSpace(modelRef)
	if ref == "" {
		ref = strings.TrimSpace(cfg.Agent.Model)
	}
	rm, err := cfg.ResolveLLM(ref)
	if err != nil {
		return nil, err
	}
	cap := cfg.Memory.CopilotMaxTokens
	clampProviderMax(rm, cap)
	return llm.NewProvider(rm.ProviderType, rm.Model, rm.APIKey, rm.BaseURL, rm.MaxTokens, rm.Temperature)
}

// RunRecall runs the recall sub-agent and returns text for the main prompt memory section.
func RunRecall(ctx context.Context, log *slog.Logger, cfg *config.Config, cwd, userQuery, modelRef string) (string, error) {
	store, err := NewStore(&cfg.Memory, cfg.Paths, cwd)
	if err != nil {
		return "", err
	}
	if !store.HasAnyFiles() {
		return "", nil
	}
	prov, err := newCopilotProvider(cfg, modelRef)
	if err != nil {
		return "", err
	}
	tools := ToolDefinitions()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: recallSystem},
		{Role: llm.RoleUser, Content: "User message for this turn:\n" + userQuery},
	}
	max := cfg.Memory.RecallMaxTurns
	for step := 0; step < max; step++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		resp, err := prov.Complete(ctx, msgs, tools)
		if err != nil {
			return "", err
		}
		if len(resp.ToolCalls) == 0 {
			out := strings.TrimSpace(resp.Content)
			if out == "" {
				return "", nil
			}
			return out, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			res, ex := execTool(store, &cfg.Memory, tc.Name, tc.InputJSON)
			if ex != nil {
				res = "error: " + ex.Error()
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: res})
		}
	}
	if log != nil {
		log.Warn("memory recall exceeded max turns")
	}
	return "", nil
}

type judgeResult struct {
	Save   bool   `json:"save"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

// RunPersist optionally writes a new memory file after an LLM-as-judge step.
func RunPersist(ctx context.Context, log *slog.Logger, cfg *config.Config, cwd, modelRef, userQuery, assistantReply string) error {
	assistantReply = strings.TrimSpace(assistantReply)
	if assistantReply == "" {
		return nil
	}
	prov, err := newCopilotProvider(cfg, modelRef)
	if err != nil {
		return err
	}
	userPayload := fmt.Sprintf("User:\n%s\n\nAssistant:\n%s\n", userQuery, assistantReply)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: judgeSystem},
		{Role: llm.RoleUser, Content: userPayload},
	}
	resp, err := prov.Complete(ctx, msgs, nil)
	if err != nil {
		return err
	}
	raw := extractJSONObject(resp.Content)
	var jr judgeResult
	if err := json.Unmarshal([]byte(raw), &jr); err != nil {
		if log != nil {
			log.Warn("memory judge parse failed", "error", err)
		}
		return nil
	}
	if !jr.Save {
		return nil
	}
	scope := strings.ToLower(strings.TrimSpace(jr.Scope))
	if scope != "global" && scope != "project" {
		scope = "global"
	}
	store, err := NewStore(&cfg.Memory, cfg.Paths, cwd)
	if err != nil {
		return err
	}
	body := strings.TrimSpace(jr.Body)
	if len(body) > 900 {
		body = body[:900] + "\n..."
	}
	if _, err := store.Write(scope, jr.Title, body); err != nil {
		return err
	}
	if log != nil {
		log.Info("memory saved", "scope", scope, "reason", jr.Reason)
	}
	return nil
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		s = strings.TrimSpace(s[i+3:])
		if j := strings.Index(s, "\n"); j >= 0 && strings.HasPrefix(strings.TrimSpace(s[:j]), "json") {
			s = strings.TrimSpace(s[j+1:])
		}
		if k := strings.Index(s, "```"); k >= 0 {
			s = strings.TrimSpace(s[:k])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "{}"
	}
	return s[start : end+1]
}
