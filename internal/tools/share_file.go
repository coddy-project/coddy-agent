package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const ShareFileToolName = "share_file"

func ShareFileTool() *tooling.Tool {
	return &tooling.Tool{
		Definition:         llm.ToolDefinition{Name: ShareFileToolName, Description: "Copy one regular workspace file into this session as an immutable downloadable artifact.", InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Workspace-relative path of the file to share."}}, "required": []interface{}{"path"}}},
		RequiresPermission: true,
		Execute:            executeShareFile,
	}
}

func executeShareFile(_ context.Context, raw string, env *tooling.Env) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", err
	}
	if env == nil || env.SubagentDepth > 0 {
		return "", fmt.Errorf("share_file is unavailable in child sessions")
	}
	a, err := session.CaptureArtifact(env.SessionDir, env.CWD, in.Path)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(struct {
		Artifact session.Artifact `json:"artifact"`
	}{a})
	return string(b), nil
}
