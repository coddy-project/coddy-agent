package agent

import "testing"

// TestPreviewServerIsAnAgentModeTool pins where preview_server is offered: it
// opens a port and starts a task, which is not reading the workspace, so plan
// and ask never see it.
func TestPreviewServerIsAnAgentModeTool(t *testing.T) {
	if !ToolSetForMode("agent").Allows("preview_server") {
		t.Fatal("agent mode should offer preview_server")
	}
	for _, mode := range []string{"plan", "ask"} {
		if ToolSetForMode(mode).Allows("preview_server") {
			t.Fatalf("%s mode should not offer preview_server", mode)
		}
	}
}
