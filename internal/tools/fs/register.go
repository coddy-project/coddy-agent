package fs

import (
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RegisterBuiltins registers all filesystem-backed tools via add.
func RegisterBuiltins(add func(*tooling.Tool)) {
	for _, ctor := range []func() *tooling.Tool{
		ReadFileTool,
		WriteFileTool,
		WriteTextFileTool,
		ListDirTool,
		SearchFilesTool,
		ApplyDiffTool,
		MkdirTool,
		RmdirTool,
		TouchTool,
		RemoveTool,
		MoveTool,
	} {
		add(ctor())
	}
}
