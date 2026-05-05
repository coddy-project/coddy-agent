package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// AskUserApprovalTool asks the human for approval with a model-authored message (ACP/UI permission flow).
func AskUserApprovalTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "ask_user_approval",
			Description: "Ask the user for approval before a sensitive action. Provide a clear human-readable message explaining why approval is needed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "Explanation shown to the user in the permission dialog",
					},
				},
				"required": []string{"message"},
			},
		},
		AllowedInPlanMode:  false,
		RequiresPermission: false,
		Execute:            executeAskUserApproval,
	}
}

type askApprovalArgs struct {
	Message string `json:"message"`
}

func executeAskUserApproval(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[askApprovalArgs](argsJSON)
	if err != nil {
		return "", err
	}
	msg := strings.TrimSpace(args.Message)
	if msg == "" {
		return "", fmt.Errorf("message is required")
	}
	if env.Sender == nil {
		return "", fmt.Errorf("ask_user_approval requires a connected client")
	}
	toolCallID := fmt.Sprintf("ask_user_approval_%d", time.Now().UnixNano())
	res, err := env.Sender.RequestPermission(ctx, acp.PermissionRequestParams{
		SessionID: env.SessionID,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: toolCallID,
			Title:      "Approval",
			Kind:       "other",
			Status:     "pending",
			Content: []acp.ToolCallResultItem{
				{Type: "content", Content: acp.ContentBlock{Type: "text", Text: msg}},
			},
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "allow_always", Name: "Allow always", Kind: "allow_always"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	})
	if err != nil {
		return "", err
	}
	if res == nil || res.Outcome == "cancelled" || res.OptionID == "reject" {
		return "user denied approval", nil
	}
	return "user approved", nil
}
