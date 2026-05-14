//go:build memory

package memory

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

//go:embed prompts/copilot.md
var beforeTurnSystemPrompt string

// BeforeTurnOutcome is the result of the single pre-main-agent memory copilot pass.
type BeforeTurnOutcome struct {
	Mode        string // "recall" or "persist"
	ContextText string // merged into Session memory for the main agent
	ReadPaths   []string
	Persist     PersistOutcome
}

// PersistOutcome is structured data when coddy_memory_save completed in the same pass.
type PersistOutcome struct {
	Saved        bool
	Scope        string
	RelativePath string
	Title        string
	Body         string
	Reason       string
	RawFinalText string
}

// RunBeforeTurnOptions configures streaming hooks for the unified memory pass.
type RunBeforeTurnOptions struct {
	OnPhaseStart func()
	OnStream     func(kind StreamKind, delta string)
}

// StreamKind discriminates streamed memory copilot content.
type StreamKind string

const (
	StreamKindText      StreamKind = "text"
	StreamKindReasoning StreamKind = "reasoning"
)

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
	return llm.NewProvider(llm.ProviderInput{
		Type:        rm.ProviderType,
		Model:       rm.Model,
		APIKey:      rm.APIKey,
		BaseURL:     rm.BaseURL,
		ProxyURL:    rm.ProxyURL,
		MaxTokens:   rm.MaxTokens,
		Temperature: rm.Temperature,
	})
}

type saveCapture struct {
	scopeLabel   string
	relativePath string
	title        string
	body         string
}

func timeNowMs() int64 { return time.Now().UnixMilli() }

func appendRecallReadPath(slice []string, p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return slice
	}
	for _, x := range slice {
		if x == p {
			return slice
		}
	}
	return append(slice, p)
}

func runCopilotStreamRound(ctx context.Context, prov llm.Provider, msgs []llm.Message, defs []llm.ToolDefinition, onStream func(kind StreamKind, delta string)) (*llm.Response, error) {
	resp, err := prov.Stream(ctx, msgs, defs, func(ch llm.StreamChunk) {
		if onStream == nil {
			return
		}
		if ch.TextDelta != "" {
			onStream(StreamKindText, ch.TextDelta)
		}
		if ch.ReasoningDelta != "" {
			onStream(StreamKindReasoning, ch.ReasoningDelta)
		}
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// RunBeforeTurn runs the unified memory copilot once before the main agent: either recall or persist, never both.
func RunBeforeTurn(ctx context.Context, log *slog.Logger, cfg *config.Config, cwd, userQuery, modelRef string, opts *RunBeforeTurnOptions) (BeforeTurnOutcome, int64, error) {
	out := BeforeTurnOutcome{Mode: "recall"}
	if !cfg.Memory.Enabled {
		return out, 0, nil
	}
	store, err := memstorage.NewStore(&cfg.Memory, cfg.Paths, cwd)
	if err != nil {
		return out, 0, err
	}
	prov, err := newCopilotProvider(cfg, modelRef)
	if err != nil {
		return out, 0, err
	}
	memTools := memtools.PersistTools(store, &cfg.Memory)
	toolDefs := memtools.ToolDefinitions(memTools)
	toolEnv := &tooling.Env{CWD: cwd}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.TrimSpace(beforeTurnSystemPrompt)},
		{Role: llm.RoleUser, Content: "User message for this turn:\n" + userQuery},
	}
	maxTurns := cfg.Memory.PersistMaxTurns
	if cfg.Memory.RecallMaxTurns > maxTurns {
		maxTurns = cfg.Memory.RecallMaxTurns
	}
	started := timeNowMs()
	if opts != nil && opts.OnPhaseStart != nil {
		opts.OnPhaseStart()
	}

	var readPaths []string
	var lastSave *saveCapture
	mutationSeen := false

	for step := 0; step < maxTurns; step++ {
		if ctx.Err() != nil {
			return out, timeNowMs() - started, ctx.Err()
		}
		var resp *llm.Response
		var err error
		if opts != nil && opts.OnStream != nil {
			resp, err = runCopilotStreamRound(ctx, prov, msgs, toolDefs, opts.OnStream)
		} else {
			resp, err = prov.Complete(ctx, msgs, toolDefs)
		}
		if err != nil {
			return out, timeNowMs() - started, err
		}
		if len(resp.ToolCalls) == 0 {
			finalText := strings.TrimSpace(resp.Content)
			out.ContextText = finalText
			out.Persist.RawFinalText = finalText
			out.Persist.Reason = finalText
			if lastSave != nil {
				out.Persist.Saved = true
				out.Persist.Scope = lastSave.scopeLabel
				out.Persist.RelativePath = lastSave.relativePath
				out.Persist.Title = lastSave.title
				out.Persist.Body = lastSave.body
				if log != nil {
					log.Info("memory saved", "path", lastSave.relativePath)
				}
			}
			if mutationSeen || out.Persist.Saved {
				out.Mode = "persist"
			}
			out.ReadPaths = readPaths
			return out, timeNowMs() - started, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			switch tc.Name {
			case memtools.NameSave, memtools.NameMkdir, memtools.NameDelete:
				mutationSeen = true
			}
			res, ex := memtools.Exec(ctx, memTools, tc.Name, tc.InputJSON, toolEnv)
			if tc.Name == memtools.NameRead && ex == nil {
				var ra struct {
					Path string `json:"path"`
				}
				if uerr := json.Unmarshal([]byte(tc.InputJSON), &ra); uerr == nil {
					readPaths = appendRecallReadPath(readPaths, ra.Path)
				}
			}
			if ex != nil {
				res = "error: " + ex.Error()
			} else if tc.Name == memtools.NameSave {
				var args struct {
					Title        string `json:"title"`
					Body         string `json:"body"`
					Scope        string `json:"scope"`
					RelativePath string `json:"relative_path"`
				}
				if uerr := json.Unmarshal([]byte(tc.InputJSON), &args); uerr == nil {
					written := strings.TrimPrefix(strings.TrimSpace(res), "saved as")
					written = strings.TrimSpace(written)
					body := strings.TrimSpace(args.Body)
					if len(body) > 900 {
						body = body[:900] + "\n..."
					}
					lastSave = &saveCapture{
						scopeLabel:   strings.ToLower(strings.TrimSpace(args.Scope)),
						relativePath: written,
						title:        strings.TrimSpace(args.Title),
						body:         body,
					}
				}
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: res})
		}
	}
	if log != nil {
		log.Warn("memory before-turn exceeded max turns")
	}
	dur := timeNowMs() - started
	if lastSave != nil {
		out.Persist.Saved = true
		out.Persist.Scope = lastSave.scopeLabel
		out.Persist.RelativePath = lastSave.relativePath
		out.Persist.Title = lastSave.title
		out.Persist.Body = lastSave.body
		out.Persist.Reason = "memory copilot stopped at max turns after a save"
		out.Persist.RawFinalText = out.Persist.Reason
		out.ContextText = out.Persist.Reason
		out.Mode = "persist"
	}
	out.ReadPaths = readPaths
	return out, dur, nil
}
