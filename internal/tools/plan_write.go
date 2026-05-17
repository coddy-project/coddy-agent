package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// PlanWriteTool writes a design plan file under the session bundle plans/ directory.
func PlanWriteTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "plan_write",
			Description: "Write or replace a design plan file at plans/<slug>.plan.md in the session bundle. " +
				"Use YAML frontmatter (name, overview, todos) plus a markdown body. " +
				"Each todo is either a plain string or an object with content (aliases title, description, label also work). " +
				"Publishes a design plan preview to the client.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "Plan identifier (lowercase letters, digits, hyphens)",
					},
					"content": map[string]interface{}{
						"type": "string",
						"description": "Full .plan.md file content including YAML frontmatter. " +
							"Example todos: [\"Step one\", \"Step two\"] or [{\"content\":\"Step one\",\"status\":\"pending\"}].",
					},
				},
				"required": []interface{}{"slug", "content"},
			},
		},
		RequiresPermission: false,
		Execute:            executePlanWrite,
	}
}

type planWriteArgs struct {
	Slug    string `json:"slug"`
	Content string `json:"content"`
}

func executePlanWrite(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[planWriteArgs](argsJSON)
	if err != nil {
		return "", err
	}
	slug := strings.TrimSpace(args.Slug)
	if err := plans.ValidateSlug(slug); err != nil {
		return "", err
	}
	content := args.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("plan_write: content must not be empty")
	}
	sd := strings.TrimSpace(env.SessionDir)
	if sd == "" {
		return "", fmt.Errorf("plan_write: session persistence is not available")
	}
	doc, err := plans.Write(sd, slug, content)
	if err != nil {
		return "", err
	}
	if env.PersistPlanDocument != nil {
		env.PersistPlanDocument(*doc)
	}
	if env.SendDesignPlanUpdate != nil {
		env.SendDesignPlanUpdate(*doc)
	}
	return fmt.Sprintf("wrote design plan %q (%d bytes)", slug, len(content)), nil
}

// SendDesignPlanUpdate emits a standard plan update with design-plan _meta.
func SendDesignPlanUpdate(env *tooling.Env, doc plans.Document) {
	if env.Sender == nil {
		return
	}
	_ = env.Sender.SendSessionUpdate(env.SessionID, acp.PlanUpdate{
		SessionUpdate: acp.UpdateTypePlan,
		Entries:       plans.EntriesFromTodos(doc.Todos),
		Meta:          plans.DesignPlanMeta(doc.Slug),
	})
}
