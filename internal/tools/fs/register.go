package fs

import (
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RegisterBuiltins registers all filesystem-backed tools via add.
func RegisterBuiltins(add func(*tooling.Tool)) {
	for _, ctor := range []func() *tooling.Tool{
		ReadTool,
		GlobTool,
		GrepTool,
		EditTool,
		WriteTool,
		ApplyPatchTool,
		MkdirTool,
		RmdirTool,
		TouchTool,
		RemoveTool,
		MoveTool,
	} {
		add(ctor())
	}
}
