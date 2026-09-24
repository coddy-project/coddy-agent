package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolCompactContext is the tool a model calls to fold its own history.
const ToolCompactContext = "compact_context"

// CompactContextTool lets the model compact the session itself instead of
// waiting for the automatic trigger or for the operator to type /compact. A
// model that can see its context filling - a long build log it just read, a
// large file it no longer needs - is the one that knows the work is safe to
// summarize, and calling the tool is cheaper than being cut off mid-task.
// The configured models are listed in the description, so "compact it with
// qwen" becomes the id that names that model.
func CompactContextTool(cfg *config.Config) *tooling.Tool {
	description := "Summarize the older part of this conversation so the session keeps fitting the model's context window. The most recent turns stay verbatim; everything before them becomes one summary. Call it when the context is close to full and the older history is no longer needed verbatim, or when the user asks for a compaction. When the user names the model that should write the summary, pass it as model; otherwise leave model out."
	var models []string
	if cfg != nil {
		for i := range cfg.Models {
			if id := cfg.Models[i].Model; id != "" {
				models = append(models, "- "+id)
			}
		}
	}
	if len(models) > 0 {
		description += "\n\nConfigured models:\n" + strings.Join(models, "\n")
	}
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        ToolCompactContext,
			Description: description,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"instructions": map[string]interface{}{
						"type":        "string",
						"description": "Optional guidance for the summary: what must survive the fold (files, decisions, pending steps).",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Optional summarizer for this one compaction, " + tooling.ModelChoiceRule + ": a configured model id from the list above, or a part of one that names exactly one. Omit it to use the configured summarizer.",
					},
				},
			},
		},
		RequiresPermission: false,
		Execute:            executeCompactContext,
	}
}

func executeCompactContext(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || env.CompactSession == nil {
		return "", fmt.Errorf("%s is not available in this runtime", ToolCompactContext)
	}
	var args struct {
		Instructions string `json:"instructions"`
		Model        string `json:"model"`
	}
	if s := strings.TrimSpace(argsJSON); s != "" && s != "{}" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	out, err := env.CompactSession(ctx, tooling.CompactRequest{
		Instructions: strings.TrimSpace(args.Instructions),
		Model:        strings.TrimSpace(args.Model),
	})
	if err != nil {
		return "", err
	}
	// The transcript the loop replays is shorter now, so the outgoing message
	// slice has to be rebuilt before the next model call (see react.go).
	env.ContextCompacted = true
	return out, nil
}
