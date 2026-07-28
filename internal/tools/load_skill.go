package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// LoadSkillTool lets the model pull a catalogued skill's full instructions into
// the current turn on its own (model-driven auto-discovery), instead of waiting
// for an explicit /name invocation. It is registered only when
// skills.auto_discovery is enabled; the session wires Env.LoadSkillBody to the
// loaded skill set for the current cwd.
func LoadSkillTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "load_skill",
			Description: "Load the full instructions of a skill listed in the Slash commands catalog by its name " +
				"(e.g. \"code-review\"). Call this when the user's request matches a catalogued skill and you need " +
				"its detailed steps before acting. Returns the skill's markdown body.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Skill/command name from the catalog (with or without the leading slash).",
					},
				},
				"required": []interface{}{"name"},
			},
		},
		RequiresPermission: false,
		Execute:            executeLoadSkill,
	}
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

func executeLoadSkill(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[loadSkillArgs](argsJSON)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(strings.TrimSpace(args.Name), "/")
	if name == "" {
		return "", fmt.Errorf("load_skill: name is required")
	}
	if env == nil || env.LoadSkillBody == nil {
		return "", fmt.Errorf("load_skill: skill auto-discovery is not available in this session")
	}
	body, available, found := env.LoadSkillBody(name)
	if !found {
		sort.Strings(available)
		return "", fmt.Errorf("load_skill: unknown skill %q; available: %s", name, strings.Join(available, ", "))
	}
	return body, nil
}
