package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolSpawnAgent is the registry name of the subagent tool.
const ToolSpawnAgent = "spawn_agent"

// SpawnAgentTool lets the model delegate a self-contained task to a subagent:
// a child agent with its own context, its own session transcript and the role
// an operator wrote in a definition file. The run is a background task of this
// session, so the background tools observe and stop it; the tool itself only
// hands the request to the runtime hook the agent wires into the Env.
func SpawnAgentTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolSpawnAgent,
			Description: "Delegate a self-contained task to a subagent listed in the Subagents section. " +
				"The child starts with an empty context and sees none of this conversation, so the prompt must carry everything it needs. " +
				"By default the call waits and returns the child's final report; the user does not see that report, so restate what matters in your reply. " +
				"With background:true it returns a task id at once and you collect the report later with background_wait or background_output " +
				"(background_stop terminates it). Use it for work that would flood this context or for independent pieces that can run in parallel; " +
				"do not delegate a one-step task you can do directly.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent": map[string]interface{}{
						"type":        "string",
						"description": "Subagent name from the Subagents section (for example \"explore\" or \"general\")",
					},
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The task, self-contained: goal, relevant paths, constraints, and what the report must contain",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Three to five words naming the task; shown in the Tasks panel and as the child session title",
					},
					"background": map[string]interface{}{
						"type":        "boolean",
						"description": "Return the task id immediately instead of waiting for the report; collect it later with background_wait or background_output",
					},
					"expected_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Your honest estimate of how long the child needs; drives the status ticker and, when timeout_seconds is omitted, the hard timeout",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Hard limit for the run; omit to use the definition's or the configured default",
					},
					"notify_on_finish": map[string]interface{}{
						"type":        "boolean",
						"description": "For a background run: wake yourself with the outcome when it finishes, so you can end your turn now",
					},
				},
				"required": []interface{}{"agent", "prompt"},
			},
		},
		Execute: executeSpawnAgent,
	}
}

type spawnAgentArgs struct {
	Agent           string `json:"agent"`
	Prompt          string `json:"prompt"`
	Description     string `json:"description"`
	Background      bool   `json:"background"`
	ExpectedSeconds int    `json:"expected_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	NotifyOnFinish  bool   `json:"notify_on_finish"`
}

func executeSpawnAgent(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[spawnAgentArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if env == nil || env.SpawnAgent == nil {
		return "", fmt.Errorf("spawn_agent: subagents are not available in this session")
	}
	name := strings.TrimSpace(args.Agent)
	if name == "" {
		return "", fmt.Errorf("spawn_agent: agent is required")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", fmt.Errorf("spawn_agent: prompt is required")
	}
	return env.SpawnAgent(ctx, tooling.SpawnRequest{
		Agent:           name,
		Prompt:          args.Prompt,
		Description:     strings.TrimSpace(args.Description),
		Background:      args.Background,
		ExpectedSeconds: args.ExpectedSeconds,
		TimeoutSeconds:  args.TimeoutSeconds,
		NotifyOnFinish:  args.NotifyOnFinish,
	})
}
