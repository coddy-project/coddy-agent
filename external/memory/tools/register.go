//go:build memory

// Package memtools defines coddy_memory_* tools for the memory copilot sub-agent.
package memtools

import (
	"context"
	"fmt"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func toolByName(tools []*tooling.Tool, name string) *tooling.Tool {
	for _, t := range tools {
		if t != nil && t.Definition.Name == name {
			return t
		}
	}
	return nil
}

// Exec runs a memory tool by name against a list from PersistTools or RecallTools.
func Exec(ctx context.Context, tools []*tooling.Tool, name, argsJSON string, env *tooling.Env) (string, error) {
	t := toolByName(tools, name)
	if t == nil || t.Execute == nil {
		return "", fmt.Errorf("unknown memory tool %q", name)
	}
	if env == nil {
		env = &tooling.Env{}
	}
	return t.Execute(ctx, argsJSON, env)
}

// ExecTool runs one coddy_memory_* tool (convenience for tests; builds PersistTools each call).
func ExecTool(store *memstorage.Store, mem *config.MemoryConfig, name, inputJSON string) (string, error) {
	return Exec(context.Background(), PersistTools(store, mem), name, inputJSON, &tooling.Env{})
}

// PersistTools is the full memory copilot tool list (search through delete).
func PersistTools(store *memstorage.Store, mem *config.MemoryConfig) []*tooling.Tool {
	return []*tooling.Tool{
		memorySearchTool(store, mem),
		memoryListTool(store),
		memoryReadTool(store),
		memoryMkdirTool(store),
		memorySaveTool(store, mem),
		memoryDeleteTool(store),
	}
}

// RecallTools is the read-only subset (RECALL mode per system prompt).
func RecallTools(store *memstorage.Store, mem *config.MemoryConfig) []*tooling.Tool {
	return []*tooling.Tool{
		memorySearchTool(store, mem),
		memoryListTool(store),
		memoryReadTool(store),
	}
}

// ToolDefinitions maps wired tools to LLM schemas.
func ToolDefinitions(tools []*tooling.Tool) []llm.ToolDefinition {
	out := make([]llm.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			out = append(out, t.Definition)
		}
	}
	return out
}

// RecallToolDefinitions returns schemas for read-only recall tools.
func RecallToolDefinitions(store *memstorage.Store, mem *config.MemoryConfig) []llm.ToolDefinition {
	return ToolDefinitions(RecallTools(store, mem))
}

// PersistToolDefinitions returns schemas for the full persist-capable tool list.
func PersistToolDefinitions(store *memstorage.Store, mem *config.MemoryConfig) []llm.ToolDefinition {
	return ToolDefinitions(PersistTools(store, mem))
}
