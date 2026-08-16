package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// QuestionTool asks the user structured questions (ACP session/request_question or HTTP SSE + POST answer).
func QuestionTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "question",
			Description: "Ask the user one or more multiple-choice questions and wait for answers. Each question needs a prompt and at least one option label.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"questions": map[string]interface{}{
						"type":        "array",
						"description": "Questions to present in order",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"header": map[string]interface{}{
									"type":        "string",
									"description": "Optional short heading above the question",
								},
								"question": map[string]interface{}{
									"type":        "string",
									"description": "The question text",
								},
								"options": map[string]interface{}{
									"type": "array",
									"items": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"label":       map[string]interface{}{"type": "string"},
											"description": map[string]interface{}{"type": "string"},
										},
										"required": []interface{}{"label"},
									},
								},
								"multiple": map[string]interface{}{
									"type":        "boolean",
									"description": "When true, the user may pick more than one option for this question",
								},
								"custom": map[string]interface{}{
									"type":        "boolean",
									"description": "When true, allow free-text / custom input in addition to options",
								},
							},
							"required": []interface{}{"question", "options"},
						},
					},
				},
				"required": []interface{}{"questions"},
			},
		},
		RequiresPermission: false,
		Execute:            executeQuestion,
	}
}

type questionArgs struct {
	Questions json.RawMessage `json:"questions"`
}

// decodeQuestionPrompts tolerates the encodings models actually produce for
// the questions argument: the canonical JSON array, a single question object,
// and either of those double-encoded inside a JSON string.
func decodeQuestionPrompts(raw json.RawMessage, depth int) ([]acp.QuestionPrompt, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		var qs []acp.QuestionPrompt
		if err := json.Unmarshal(trimmed, &qs); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		return qs, nil
	case '{':
		var q acp.QuestionPrompt
		if err := json.Unmarshal(trimmed, &q); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		return []acp.QuestionPrompt{q}, nil
	case '"':
		if depth >= 1 {
			return nil, fmt.Errorf("parse args: questions is a string nested inside a string")
		}
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		return decodeQuestionPrompts(json.RawMessage(inner), depth+1)
	}
	return nil, fmt.Errorf("parse args: questions must be an array of question objects")
}

func executeQuestion(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[questionArgs](argsJSON)
	if err != nil {
		return "", err
	}
	questions, err := decodeQuestionPrompts(args.Questions, 0)
	if err != nil {
		return "", err
	}
	if len(questions) == 0 {
		return "", fmt.Errorf("questions must be non-empty")
	}
	for i := range questions {
		q := &questions[i]
		if strings.TrimSpace(q.Question) == "" {
			return "", fmt.Errorf("question %d: question text is required", i)
		}
		if len(q.Options) == 0 {
			return "", fmt.Errorf("question %d: at least one option is required", i)
		}
		for j, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" {
				return "", fmt.Errorf("question %d option %d: label is required", i, j)
			}
		}
	}
	if env.Sender == nil {
		return "", fmt.Errorf("question requires a connected client")
	}
	rid := fmt.Sprintf("q_%d", time.Now().UnixNano())
	toolCallID := strings.TrimSpace(env.ToolCallID)
	if toolCallID == "" {
		toolCallID = fmt.Sprintf("question_%d", time.Now().UnixNano())
	}
	res, err := env.Sender.RequestQuestion(ctx, acp.QuestionRequestParams{
		SessionID:  env.SessionID,
		RequestID:  rid,
		ToolCallID: toolCallID,
		Questions:  questions,
	})
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("no answer received")
	}
	out, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
