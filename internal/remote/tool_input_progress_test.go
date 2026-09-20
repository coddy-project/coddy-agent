package remote

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"testing"
)

func TestRemoteForwardsPendingToolInputProgress(t *testing.T) {
	sender := &collectSender{}
	stream := &turnStream{sender: sender, sessionID: "s"}
	err := stream.onFrame(sseFrame{event: "tool_call_update", data: `{"sessionUpdate":"tool_call_update","toolCallId":"w","status":"pending","_meta":{"coddy":{"toolInputProgress":{"path":"a.html","bytes":12,"lines":2,"argumentBytes":42,"preview":"<html>"}}}}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.updates) != 1 {
		t.Fatalf("updates: %v", sender.updates)
	}
	u, ok := sender.updates[0].(acp.ToolCallStatusUpdate)
	if !ok || u.Status != "pending" {
		t.Fatalf("update: %+v", sender.updates[0])
	}
	progress := u.Meta["coddy"].(map[string]interface{})["toolInputProgress"].(map[string]interface{})
	if progress["bytes"] != float64(12) || progress["preview"] != "<html>" {
		t.Fatalf("lost progress: %v", progress)
	}
}
