//go:build cli

package cli

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestToolArgumentProgressBeforeExecution(t *testing.T) {
	a := newTestApp(t)
	send := func(u interface{}) { a.applyLoopMessage(updateMsg{update: u}) }
	named := acp.ToolCallUpdate{ToolCallID: "w1", Title: "write", Status: "pending"}
	send(named)
	send(acp.ToolCallStatusUpdate{ToolCallID: "w1", Status: "pending", Meta: map[string]interface{}{
		"coddy": map[string]interface{}{"toolInputProgress": map[string]interface{}{
			"path": "demo.html", "bytes": 42, "lines": 3, "preview": "<html>\n<body>\nHi", "argumentBytes": 80,
		}},
	}})
	if got := transcriptText(a); !strings.Contains(got, "42 bytes") || !strings.Contains(got, "demo.html") {
		t.Fatalf("missing pending progress: %s", got)
	}
	tb := a.toolBoxes["w1"]
	tb.SetExpanded(true)
	if !strings.Contains(transcriptText(a), "<html>") {
		t.Fatal("expanded progress has no preview")
	}
	send(named)
	if a.toolBoxes["w1"] != tb {
		t.Fatal("complete announcement replaced the pending card")
	}
	send(acp.ToolCallStatusUpdate{ToolCallID: "w1", Status: "completed"})
	if strings.Contains(transcriptText(a), "Generating") {
		t.Fatal("completed tool retains live progress")
	}
}

func TestToolDraftSanitizesTerminalControls(t *testing.T) {
	a := newTestApp(t)
	tb := newToolBox(a.theme, "w", "write", "edit", func(string) (string, bool) { t.Fatal("draft expansion must not read tool results"); return "", false })
	tb.SetInputProgress("a\x1b[2J.html", "hello\x1b[2Jworld\x1b]52;c;ZXZpbA==\x07", 20, 1, 40)
	tb.SetExpanded(true)
	if strings.Contains(tb.inputPath, "\x1b") || strings.Contains(tb.inputPreview, "\x1b") {
		t.Fatal("draft retains terminal control characters")
	}
	for _, width := range []int{30, 80, 140} {
		if len(tb.Render(width)) == 0 {
			t.Fatalf("no render at width %d", width)
		}
	}
	tb.SetStatus("cancelled", "", 0, 0)
	if tb.inputSummary != "" || tb.inputPreview != "" {
		t.Fatal("cancelled draft retained")
	}
}
