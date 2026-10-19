package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
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
				"With background:true it returns a task id at once, and the finished run wakes you with its report by default, so you can end your turn; " +
				"background_wait or background_output collect it sooner and background_stop terminates it. Use it for work that would flood this context or for independent pieces that can run in parallel; " +
				"do not delegate a one-step task you can do directly. " +
				"A run that stopped before its report (a provider failure, its timeout, its turn limit) keeps its transcript: continue it with resume instead of starting a new subagent on the same task.",
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
						"description": "Return the task id immediately instead of waiting for the report; the finished run wakes you with it by default, or collect it sooner with background_wait or background_output",
					},
					"expected_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Your honest estimate of how long the child needs; drives the status ticker and, when timeout_seconds is omitted, the hard timeout",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Hard limit for the run; omit to use the definition's or the configured default",
					},
					"model": map[string]interface{}{
						"type": "string",
						"description": "A configured model id for the child (see switch_model for the list); omit to use the definition's model, then yours. " +
							"Pick a cheaper, faster model for a search or a routine task and a stronger one for a hard problem",
					},
					"reasoning": map[string]interface{}{
						"type":        "string",
						"description": "Reasoning level for the child's model (a level it offers, \"off\" or \"default\"); omit to use the definition's, then the model's default",
					},
					"notify_on_finish": map[string]interface{}{
						"type":        "boolean",
						"description": "For a background run: wake yourself with the outcome when it finishes (default true where available); set false explicitly to prevent a wake. A completed result you collect or a task you stop does not wake you again",
					},
					"isolation": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{"worktree"},
						"description": "\"worktree\": the child works in a git worktree of its own, on a new branch at the commit this session's checkout is on (uncommitted changes stay here), so its edits do not touch your files; " +
							"a definition can ask for it too. A worktree the run left nothing in is removed when it ends; one with work in it stays, and the result names its folder and branch",
					},
					"resume": map[string]interface{}{
						"type": "string",
						"description": "Continue a finished run of this session instead of starting a new subagent: its task id (bg_...) or its child session id (sess_...) from an earlier spawn_agent result. " +
							"The child keeps its transcript and takes prompt as its next message, so say only what it should do now (for example, go on from where it stopped). " +
							"agent must name the same subagent; a run still in flight cannot be resumed",
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
	Model           string `json:"model"`
	Reasoning       string `json:"reasoning"`
	Prompt          string `json:"prompt"`
	Description     string `json:"description"`
	Background      bool   `json:"background"`
	ExpectedSeconds int    `json:"expected_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	NotifyOnFinish  *bool  `json:"notify_on_finish"`
	Isolation       string `json:"isolation"`
	Resume          string `json:"resume"`
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
	isolation, err := subagents.ParseIsolation(args.Isolation)
	if err != nil {
		return "", fmt.Errorf("spawn_agent: %w", err)
	}
	return env.SpawnAgent(ctx, tooling.SpawnRequest{
		Agent:           name,
		Model:           strings.TrimSpace(args.Model),
		Reasoning:       strings.ToLower(strings.TrimSpace(args.Reasoning)),
		Prompt:          args.Prompt,
		Description:     strings.TrimSpace(args.Description),
		Background:      args.Background,
		ExpectedSeconds: args.ExpectedSeconds,
		TimeoutSeconds:  args.TimeoutSeconds,
		NotifyOnFinish:  args.NotifyOnFinish == nil || *args.NotifyOnFinish,
		Isolation:       isolation,
		Resume:          strings.TrimSpace(args.Resume),
	})
}
